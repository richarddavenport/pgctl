package engine

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/richarddavenport/pgctl/internal/pg"
)

// Probe is what pgctl can find out about an environment without being asked to
// do anything to it: whether it answers, what it is running, and what is on it.
//
// Everything here is a question the operator would otherwise answer by opening
// psql in another window before deciding what to move.
type Probe struct {
	Environment string

	// Reachable and Err record the outcome. An unreachable environment is a
	// normal state to display, not an error that stops the UI.
	Reachable bool
	Err       error

	ServerVersion int
	User          string
	Host          string
	Port          int

	Databases []DatabaseInfo

	// ProbedAt lets a display say how stale the numbers are.
	ProbedAt time.Time
}

// DatabaseInfo is one database on a server.
type DatabaseInfo struct {
	Name string
	// Bytes is the on-disk size, which includes indexes and is therefore much
	// larger than the snapshot a dump of it produces.
	Bytes int64
	// Declared records whether pgctl's config knows about this database. One
	// that exists on the server but is not declared cannot be snapshotted, and
	// saying so is more useful than hiding it.
	Declared bool
}

// ProbeEnvironment connects to an environment and reports what is there.
//
// Failures come back inside the Probe rather than as an error return: a
// dashboard showing four environments should show the three that answered and
// say why the fourth did not.
func (e *Engine) ProbeEnvironment(ctx context.Context, envName string) Probe {
	p := Probe{Environment: envName, ProbedAt: time.Now()}

	target, err := Resolve(ctx, e.cfg, e.root, envName, "")
	if err != nil {
		p.Err = err
		return p
	}
	p.User, p.Host, p.Port = target.User, target.Host, target.Port

	conn, err := target.Connect(ctx, target.MaintenanceDB())
	if err != nil {
		p.Err = err
		return p
	}
	defer conn.Close(ctx) //nolint:errcheck // nothing useful to do with a close failure

	const q = `
SELECT d.datname, pg_database_size(d.oid), current_setting('server_version_num')::int
  FROM pg_database d
 WHERE NOT d.datistemplate
 ORDER BY 1`

	rows, err := conn.Query(ctx, q)
	if err != nil {
		p.Err = fmt.Errorf("list databases: %w", err)
		return p
	}
	defer rows.Close()

	declared := map[string]bool{}
	for _, d := range e.cfg.Databases {
		declared[d.Name] = true
	}
	for rows.Next() {
		var info DatabaseInfo
		if err := rows.Scan(&info.Name, &info.Bytes, &p.ServerVersion); err != nil {
			p.Err = err
			return p
		}
		info.Declared = declared[info.Name]
		p.Databases = append(p.Databases, info)
	}
	if err := rows.Err(); err != nil {
		p.Err = err
		return p
	}

	p.Reachable = true
	return p
}

// LiveTables reads a database's tables as they are now, so an operator can
// decide what to move before taking a snapshot of anything.
func (e *Engine) LiveTables(ctx context.Context, envName, database string) ([]pg.TableInfo, error) {
	target, err := Resolve(ctx, e.cfg, e.root, envName, database)
	if err != nil {
		return nil, err
	}
	conn, err := target.Connect(ctx, database)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx) //nolint:errcheck // nothing useful to do with a close failure

	db, _ := e.database(database)
	cat, err := pg.Introspect(ctx, conn, db.ExcludeSchemas)
	if err != nil {
		return nil, err
	}
	// Largest first: the reason to look at this list is almost always to find
	// out what is big.
	sort.Slice(cat.Tables, func(i, j int) bool { return cat.Tables[i].Bytes > cat.Tables[j].Bytes })
	return cat.Tables, nil
}

// LiveCatalog reads the whole referential structure, for the views that show
// foreign keys and load order against a live target.
func (e *Engine) LiveCatalog(ctx context.Context, envName, database string) (*pg.Catalog, error) {
	target, err := Resolve(ctx, e.cfg, e.root, envName, database)
	if err != nil {
		return nil, err
	}
	conn, err := target.Connect(ctx, database)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx) //nolint:errcheck // nothing useful to do with a close failure

	db, _ := e.database(database)
	return pg.Introspect(ctx, conn, db.ExcludeSchemas)
}

// SetMembers resolves a set against a live database, returning its members and
// the tables closing it would add — the two numbers that decide whether a set
// is usable as declared.
func (e *Engine) SetMembers(ctx context.Context, envName, database, setName string) (members, added []string, err error) {
	set, ok := e.cfg.LookupSet(setName)
	if !ok {
		return nil, nil, fmt.Errorf("unknown set %q", setName)
	}
	cat, err := e.LiveCatalog(ctx, envName, database)
	if err != nil {
		return nil, nil, err
	}
	for _, t := range cat.Tables {
		if set.Matches(t.Name) {
			members = append(members, t.Name)
		}
	}
	sort.Strings(members)
	return members, cat.Graph.MissingParents(members), nil
}
