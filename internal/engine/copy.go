package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/klauspost/compress/zstd"

	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// copyFiltered produces the data pg_dump cannot: the rows of a table that
// carries a `where:` rule.
//
// Binary COPY, not CSV or text. A text dump renders every value as characters —
// a jsonb column costs several times its stored size, which is where "querying
// a table and writing a file is just huge" comes from. Binary is the on-disk
// representation, and zstd on top of it gets the rest.
//
// The price of binary is that the file has no header naming its columns, so the
// column list goes in the manifest and the load uses it. A column added
// upstream between dump and load then fails loudly on type mismatch rather than
// shifting every value one place to the left.
func (e *Engine) copyFiltered(ctx context.Context, target *Target, m *snapshot.Manifest,
	dir string, report Reporter) error {

	conn, err := target.Connect(ctx, m.Database)
	if err != nil {
		return err
	}
	defer conn.Close(ctx) //nolint:errcheck // nothing useful to do with a close failure

	if err := os.MkdirAll(filepath.Join(dir, snapshot.FilteredDir), 0o700); err != nil {
		return fmt.Errorf("create filtered directory: %w", err)
	}

	for i, entry := range m.Tables {
		if entry.File == "" {
			continue
		}
		columns, err := tableColumns(ctx, conn, entry.Name)
		if err != nil {
			return err
		}
		if len(columns) == 0 {
			return fmt.Errorf("%s has no columns", entry.Name)
		}

		rows, err := copyTableOut(ctx, conn, filepath.Join(dir, filepath.FromSlash(entry.File)),
			entry.Name, columns, entry.Where, e.cfg.Defaults.Compression)
		if err != nil {
			return err
		}

		m.Tables[i].Columns = columns
		m.Tables[i].Rows = rows
		report.table("filtered copy", entry.Name, fmt.Sprintf("%d rows where %s", rows, entry.Where))
	}
	return nil
}

// copyTableOut streams one filtered table to a zstd-compressed file and returns
// the row count.
func copyTableOut(ctx context.Context, conn *pgx.Conn, path, table string,
	columns []string, where, compression string) (int64, error) {

	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // the meaningful error comes from Sync below

	enc, err := zstd.NewWriter(f, zstd.WithEncoderLevel(zstdLevel(compression)))
	if err != nil {
		return 0, fmt.Errorf("open zstd writer: %w", err)
	}

	// The predicate is configuration written by the team and goes in verbatim;
	// the identifiers around it are quoted because they come from the catalog
	// and a table called "order" is legal.
	sql := fmt.Sprintf("COPY (SELECT %s FROM %s WHERE %s) TO STDOUT (FORMAT binary)",
		quoteIdentifiers(columns), quoteTable(table), where)

	tag, err := conn.PgConn().CopyTo(ctx, enc, sql)
	if err != nil {
		enc.Close() //nolint:errcheck // already failing; this only releases the encoder
		return 0, fmt.Errorf("copy %s out: %w", table, err)
	}
	if err := enc.Close(); err != nil {
		return 0, fmt.Errorf("finish %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return 0, fmt.Errorf("sync %s: %w", path, err)
	}
	return tag.RowsAffected(), nil
}

// zstdLevel maps a pg_dump --compress value onto the Go encoder's levels, so
// that the sidecars and the archive are compressed comparably. The mapping is
// coarse because the two implementations' level numbers do not correspond.
func zstdLevel(compression string) zstd.EncoderLevel {
	_, level, _ := strings.Cut(compression, ":")
	switch level {
	case "1", "2", "3":
		return zstd.SpeedDefault
	case "":
		return zstd.SpeedDefault
	case "19", "20", "21", "22":
		return zstd.SpeedBestCompression
	default:
		return zstd.SpeedBetterCompression
	}
}

// tableColumns returns a table's live columns in attribute order — the order a
// binary COPY writes and reads them in.
func tableColumns(ctx context.Context, conn *pgx.Conn, table string) ([]string, error) {
	const q = `
SELECT a.attname
  FROM pg_attribute a
  JOIN pg_class c ON c.oid = a.attrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname || '.' || c.relname = $1
   AND a.attnum > 0
   AND NOT a.attisdropped
 ORDER BY a.attnum`

	rows, err := conn.Query(ctx, q, table)
	if err != nil {
		return nil, fmt.Errorf("read columns of %s: %w", table, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func quoteTable(qualified string) string {
	schema, table, ok := strings.Cut(qualified, ".")
	if !ok {
		return quoteIdentifier(qualified)
	}
	return quoteIdentifier(schema) + "." + quoteIdentifier(table)
}

func quoteIdentifier(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quoteIdentifiers(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = quoteIdentifier(n)
	}
	return strings.Join(out, ", ")
}

// loadFilteredSidecars loads the binary COPY files back.
//
// The column list comes from the manifest, not from the target: binary COPY
// carries no column names, so the only correct reading of the file is the one it
// was written with. A column added to the target since the dump therefore fails
// on a type or count mismatch instead of shifting every value one place along.
func (e *Engine) loadFilteredSidecars(ctx context.Context, plan *Plan, dir string,
	entries []snapshot.TableEntry, report Reporter) error {

	if len(entries) == 0 {
		return nil
	}
	conn, err := plan.Target.Connect(ctx, plan.Snapshot.Database)
	if err != nil {
		return err
	}
	defer conn.Close(ctx) //nolint:errcheck // nothing useful to do with a close failure

	report.step("filtered load", fmt.Sprintf("%d tables from COPY sidecars", len(entries)))
	for _, entry := range entries {
		if len(entry.Columns) == 0 {
			return fmt.Errorf("%s has a sidecar but no recorded column list, so it cannot be loaded",
				entry.Name)
		}
		path := filepath.Join(dir, filepath.FromSlash(entry.File))
		rows, err := copyTableIn(ctx, conn, path, entry.Name, entry.Columns)
		if err != nil {
			return err
		}
		report.table("filtered load", entry.Name, fmt.Sprintf("%d rows", rows))
	}
	return nil
}

// copyTableIn streams one zstd-compressed binary COPY file into its table.
func copyTableIn(ctx context.Context, conn *pgx.Conn, path, table string, columns []string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only

	dec, err := zstd.NewReader(f)
	if err != nil {
		return 0, fmt.Errorf("open zstd reader for %s: %w", path, err)
	}
	defer dec.Close()

	sql := fmt.Sprintf("COPY %s (%s) FROM STDIN (FORMAT binary)",
		quoteTable(table), quoteIdentifiers(columns))
	tag, err := conn.PgConn().CopyFrom(ctx, dec.IOReadCloser(), sql)
	if err != nil {
		return 0, fmt.Errorf("copy %s in: %w", table, err)
	}
	return tag.RowsAffected(), nil
}
