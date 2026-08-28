package engine

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"

	"github.com/richarddavenport/pgctl/internal/config"
)

// Target is a resolved place to read from or write to: one database on one
// connection.
//
// The connection is a libpq DSN and nothing more. pgx resolves it — including
// ~/.pg_service.conf, ~/.pgpass and every PG* variable — and pg_dump and
// pg_restore are handed the same string, so both halves of an operation connect
// by exactly the same rules and pgctl never holds a password.
type Target struct {
	Conn     config.Connection
	Database string

	// resolved is what the DSN turned out to mean, for display. Nothing here
	// is used to connect; Connect re-resolves the DSN so that pgx and libpq
	// cannot drift apart.
	Host string
	Port int
	User string
}

// Resolve turns a connection name and a database into something connectable.
func Resolve(_ context.Context, cfg *config.Config, _, name, database string) (*Target, error) {
	conn, ok := cfg.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown connection %q", name)
	}

	t := &Target{Conn: conn, Database: database}
	// Resolving once up front turns "unknown host" and "no such service" into
	// an error naming the connection, rather than one that surfaces from
	// inside a subprocess minutes later.
	parsed, err := pgx.ParseConfig(t.DSN(database))
	if err != nil {
		return nil, fmt.Errorf("connection %q: %w", name, err)
	}
	t.Host, t.Port, t.User = parsed.Host, int(parsed.Port), parsed.User
	if t.Database == "" {
		t.Database = parsed.Database
	}
	return t, nil
}

// DSN is the connection string for one database on this connection.
//
// A database name is appended as a keyword rather than substituted, because the
// DSN may be a service name or a URI and pgctl has no business rewriting
// either. libpq resolves a later keyword over an earlier one, so this works for
// every form.
func (t *Target) DSN(database string) string {
	dsn := t.Conn.DSN
	if database == "" {
		return dsn
	}
	if dsn == "" {
		return "dbname=" + quoteDSNValue(database)
	}
	// A URI cannot take a trailing keyword, so its database goes in the query
	// string, which libpq accepts for every connection parameter.
	if isURI(dsn) {
		sep := "?"
		if containsRune(dsn, '?') {
			sep = "&"
		}
		return dsn + sep + "dbname=" + database
	}
	return dsn + " dbname=" + quoteDSNValue(database)
}

func isURI(dsn string) bool {
	return hasPrefix(dsn, "postgres://") || hasPrefix(dsn, "postgresql://")
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// quoteDSNValue quotes a keyword/value DSN value if it needs it.
func quoteDSNValue(v string) string {
	needs := v == ""
	for _, c := range v {
		if c == ' ' || c == '\'' || c == '\\' {
			needs = true
		}
	}
	if !needs {
		return v
	}
	out := []rune{'\''}
	for _, c := range v {
		if c == '\'' || c == '\\' {
			out = append(out, '\\')
		}
		out = append(out, c)
	}
	return string(append(out, '\''))
}

// Jobs is the parallelism to use against this target.
func (t *Target) Jobs(def config.Defaults) int {
	if t.Conn.Jobs > 0 {
		return t.Conn.Jobs
	}
	return def.Jobs
}

// MaintenanceDB is the database to connect to for work that cannot be done from
// inside the database being replaced.
func (t *Target) MaintenanceDB() string {
	if t.Conn.MaintenanceDB != "" {
		return t.Conn.MaintenanceDB
	}
	return "postgres"
}

// Connect opens a connection to the target's database. Pass an empty database
// to reach the maintenance database instead.
func (t *Target) Connect(ctx context.Context, database string) (*pgx.Conn, error) {
	if database == "" {
		database = t.MaintenanceDB()
	}
	cfg, err := pgx.ParseConfig(t.DSN(database))
	if err != nil {
		return nil, fmt.Errorf("connection %q: %w", t.Conn.Name, err)
	}

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		// pgx puts the whole connection string in its error, password
		// included. Report where we were going and not how we got in.
		return nil, fmt.Errorf("connect to %s as %s: %w",
			describe(cfg.Host, int(cfg.Port), database), cfg.User, redact(err, cfg.Password))
	}
	return conn, nil
}

// SubprocessEnv is the environment pg_dump and pg_restore run with.
//
// gssencmode=disable is not an optimisation. libpq is linked against krb5 and
// attempts a GSSAPI-encrypted connection before anything else; where a Kerberos
// lookup blackholes rather than refuses — a sandbox, a corporate network, a VPN
// with no KDC route — that attempt blocks with no timeout, and pg_dump hangs
// producing nothing. pgctl authenticates the way libpq does and has no use for
// Kerberos, so the attempt buys nothing and can cost everything.
//
// Nothing else is set: the DSN carries the connection, and ~/.pgpass carries
// the password, so there is no PGPASSWORD to leak into a process listing.
func (t *Target) SubprocessEnv(base []string) []string {
	if os.Getenv("PGGSSENCMODE") != "" {
		return base
	}
	return append(base, "PGGSSENCMODE=disable")
}

func describe(host string, port int, database string) string {
	if host == "" {
		return database
	}
	return fmt.Sprintf("%s:%d/%s", host, port, database)
}

// String describes the target for a log line or a confirmation prompt.
func (t *Target) String() string {
	return fmt.Sprintf("%s (%s)", t.Conn.Name, describe(t.Host, t.Port, t.Database))
}
