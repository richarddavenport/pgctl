package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Catalog is what pgctl needs to know about a live database: which tables
// exist, how big they are, and how they reference each other.
type Catalog struct {
	// ServerVersion is the numeric server version (170004 for 17.4).
	ServerVersion int
	Tables        []TableInfo
	FKs           []FK
	Graph         *Graph
}

// TableInfo is one table, with the size figures a plan reports so that an
// operator can see what they are about to move before they move it.
type TableInfo struct {
	// Name is schema-qualified.
	Name string
	// Bytes is the total relation size: heap, indexes and TOAST. TOAST is why
	// quotes.quote is 20 GB at 146,000 rows, so leaving it out would make the
	// number that matters the number that is missing.
	Bytes int64
	// EstimatedRows comes from the planner's statistics, not a count — an
	// exact count of a 20 GB table costs a full scan to tell an operator
	// something an estimate already told them.
	EstimatedRows int64
	// Partitioned tables are dumped and loaded as their parent; their
	// partitions are not listed separately.
	Partitioned bool
}

// systemSchemas are never a candidate for anything. hdb_catalog is Hasura's
// own bookkeeping: it is recreated by applying metadata, and its event and log
// tables are both enormous and worthless in another environment.
var systemSchemas = []string{"pg_catalog", "information_schema", "pg_toast"}

// Introspect reads the structure of the connected database.
func Introspect(ctx context.Context, conn *pgx.Conn, excludeSchemas []string) (*Catalog, error) {
	cat := &Catalog{}
	// current_setting rather than SHOW: SHOW returns text, and the version is
	// compared numerically.
	const versionQuery = `SELECT current_setting('server_version_num')::int`
	if err := conn.QueryRow(ctx, versionQuery).Scan(&cat.ServerVersion); err != nil {
		return nil, fmt.Errorf("read server version: %w", err)
	}

	excluded := append(append([]string{}, systemSchemas...), excludeSchemas...)

	tables, err := readTables(ctx, conn, excluded)
	if err != nil {
		return nil, err
	}
	fks, err := readFKs(ctx, conn, excluded)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(tables))
	for _, t := range tables {
		names = append(names, t.Name)
	}
	cat.Tables = tables
	cat.FKs = fks
	cat.Graph = NewGraph(names, fks)
	return cat, nil
}

// relkind 'r' is an ordinary table and 'p' a partitioned one. Partitions
// themselves are relispartition and excluded: they move with their parent, and
// listing them would make a set's table count meaningless.
const tablesQuery = `
SELECT n.nspname || '.' || c.relname,
       pg_total_relation_size(c.oid),
       GREATEST(c.reltuples, 0)::bigint,
       c.relkind = 'p'
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE c.relkind IN ('r', 'p')
   AND NOT c.relispartition
   AND n.nspname <> ALL($1::text[])
 ORDER BY 1`

func readTables(ctx context.Context, conn *pgx.Conn, excluded []string) ([]TableInfo, error) {
	rows, err := conn.Query(ctx, tablesQuery, excluded)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	var out []TableInfo
	for rows.Next() {
		var t TableInfo
		if err := rows.Scan(&t.Name, &t.Bytes, &t.EstimatedRows, &t.Partitioned); err != nil {
			return nil, fmt.Errorf("scan table: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// conparentid = 0 skips the per-partition copies PostgreSQL creates for a
// constraint declared on a partitioned table: they name the same relationship
// and dropping the parent's drops them all.
const fksQuery = `
SELECT con.conname,
       cn.nspname || '.' || child.relname,
       pn.nspname || '.' || parent.relname,
       pg_get_constraintdef(con.oid),
       con.convalidated,
       (SELECT array_agg(a.attname ORDER BY k.ord)
          FROM unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord)
          JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum),
       (SELECT array_agg(a.attname ORDER BY k.ord)
          FROM unnest(con.confkey) WITH ORDINALITY AS k(attnum, ord)
          JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.attnum)
  FROM pg_constraint con
  JOIN pg_class child ON child.oid = con.conrelid
  JOIN pg_namespace cn ON cn.oid = child.relnamespace
  JOIN pg_class parent ON parent.oid = con.confrelid
  JOIN pg_namespace pn ON pn.oid = parent.relnamespace
 WHERE con.contype = 'f'
   AND con.conparentid = 0
   AND cn.nspname <> ALL($1::text[])
   AND pn.nspname <> ALL($1::text[])
 ORDER BY 1`

func readFKs(ctx context.Context, conn *pgx.Conn, excluded []string) ([]FK, error) {
	rows, err := conn.Query(ctx, fksQuery, excluded)
	if err != nil {
		return nil, fmt.Errorf("list foreign keys: %w", err)
	}
	defer rows.Close()

	var out []FK
	for rows.Next() {
		var fk FK
		var validated bool
		if err := rows.Scan(&fk.Name, &fk.Child, &fk.Parent, &fk.Def, &validated,
			&fk.Columns, &fk.ParentColumns); err != nil {
			return nil, fmt.Errorf("scan foreign key: %w", err)
		}
		fk.NotValid = !validated
		out = append(out, fk)
	}
	return out, rows.Err()
}

// TriggerTables returns the given tables that carry user triggers, with the
// trigger names, so a caller can disable and re-enable them around a bulk load.
//
// Internal triggers — the ones implementing foreign keys and deferred
// constraints — are excluded deliberately. They are not disabled and could not
// be without superuser.
func TriggerTables(ctx context.Context, conn *pgx.Conn, tables []string) ([]TriggerTable, error) {
	const q = `
SELECT n.nspname || '.' || c.relname,
       array_agg(t.tgname ORDER BY t.tgname)
  FROM pg_trigger t
  JOIN pg_class c ON c.oid = t.tgrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE NOT t.tgisinternal
   AND t.tgenabled <> 'D'
   AND n.nspname || '.' || c.relname = ANY($1::text[])
 GROUP BY 1
 ORDER BY 1`

	rows, err := conn.Query(ctx, q, tables)
	if err != nil {
		return nil, fmt.Errorf("list triggers: %w", err)
	}
	defer rows.Close()

	var out []TriggerTable
	for rows.Next() {
		var t TriggerTable
		if err := rows.Scan(&t.Table, &t.Triggers); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TriggerTable is one table's user triggers. Only triggers that are currently
// enabled are listed, so re-enabling never turns on something an operator had
// deliberately switched off.
type TriggerTable struct {
	Table    string
	Triggers []string
}

// Extensions returns the extensions installed in the connected database, with
// their versions.
func Extensions(ctx context.Context, conn *pgx.Conn) ([]Extension, error) {
	const q = `SELECT extname, extversion FROM pg_extension ORDER BY 1`
	rows, err := conn.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list extensions: %w", err)
	}
	defer rows.Close()

	var out []Extension
	for rows.Next() {
		var e Extension
		if err := rows.Scan(&e.Name, &e.Version); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AvailableExtensions returns the extensions the connected server could install
// — what is on its disk, not what is loaded.
func AvailableExtensions(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	const q = `SELECT name FROM pg_available_extensions`
	rows, err := conn.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list available extensions: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// Extension is one installed extension.
type Extension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Indexes reads the secondary indexes on the given tables, so that a
// set-level apply can drop them before loading and rebuild them after.
//
// Constraint-backed indexes — primary keys and unique constraints — are
// excluded. A primary key cannot be dropped while a foreign key from outside
// the selection depends on it, and those foreign keys are precisely the ones a
// set-level apply leaves in place. Rebuilding the secondary indexes is most of
// the win and none of the risk.
func Indexes(ctx context.Context, conn *pgx.Conn, tables []string) ([]Index, error) {
	const q = `
SELECT i.indexrelid::regclass::text,
       n.nspname || '.' || t.relname,
       pg_get_indexdef(i.indexrelid)
  FROM pg_index i
  JOIN pg_class t ON t.oid = i.indrelid
  JOIN pg_namespace n ON n.oid = t.relnamespace
 WHERE NOT i.indisprimary
   AND NOT EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conindid = i.indexrelid)
   AND n.nspname || '.' || t.relname = ANY($1::text[])
 ORDER BY 1`

	rows, err := conn.Query(ctx, q, tables)
	if err != nil {
		return nil, fmt.Errorf("list indexes: %w", err)
	}
	defer rows.Close()

	var out []Index
	for rows.Next() {
		var idx Index
		if err := rows.Scan(&idx.Name, &idx.Table, &idx.Def); err != nil {
			return nil, fmt.Errorf("scan index: %w", err)
		}
		out = append(out, idx)
	}
	return out, rows.Err()
}

// Index is one secondary index, with the definition needed to recreate it.
type Index struct {
	Name  string
	Table string
	Def   string
}

// DanglingTriggers reports triggers on tables that pgctl is keeping whose
// functions live in a schema it is excluding.
//
// This is a restore failure caught at dump time. Excluding a schema excludes
// its functions, but a trigger on a retained table still references one, and
// pg_restore fails on every CREATE TRIGGER — after it has spent an hour loading
// the data. MBPNetwork's audit schema is exactly this shape: every audited
// table in claims and operations calls audit.if_modified_func.
func DanglingTriggers(ctx context.Context, conn *pgx.Conn, excludeSchemas []string) ([]DanglingTrigger, error) {
	if len(excludeSchemas) == 0 {
		return nil, nil
	}
	const q = `
SELECT n.nspname || '.' || c.relname,
       t.tgname,
       pn.nspname || '.' || p.proname
  FROM pg_trigger t
  JOIN pg_class c ON c.oid = t.tgrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_proc p ON p.oid = t.tgfoid
  JOIN pg_namespace pn ON pn.oid = p.pronamespace
 WHERE NOT t.tgisinternal
   AND pn.nspname = ANY($1::text[])
   AND NOT (n.nspname = ANY($1::text[]))
 ORDER BY 1, 2`

	rows, err := conn.Query(ctx, q, excludeSchemas)
	if err != nil {
		return nil, fmt.Errorf("check triggers against excluded schemas: %w", err)
	}
	defer rows.Close()

	var out []DanglingTrigger
	for rows.Next() {
		var d DanglingTrigger
		if err := rows.Scan(&d.Table, &d.Trigger, &d.Function); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DanglingTrigger is one trigger whose function is about to be left behind.
type DanglingTrigger struct {
	Table    string
	Trigger  string
	Function string
}
