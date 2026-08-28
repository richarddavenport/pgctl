package engine

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/richarddavenport/pgctl/internal/config"
)

// The DSN is the seam where pgx and pg_dump have to agree. Every form libpq
// accepts has to survive having a database name attached to it, because that is
// what pgctl does to reach one database on a connection.
func TestDSNCarriesTheDatabaseInEveryForm(t *testing.T) {
	cases := []struct {
		name     string
		dsn      string
		database string
		want     string
	}{
		{"service", "service=qat", "claims", "service=qat dbname=claims"},
		{"keywords", "host=db port=5433", "claims", "host=db port=5433 dbname=claims"},
		{"uri", "postgres://db/postgres", "claims", "postgres://db/postgres?dbname=claims"},
		{"uri with query", "postgres://db/postgres?sslmode=require", "claims",
			"postgres://db/postgres?sslmode=require&dbname=claims"},
		{"empty", "", "claims", "dbname=claims"},
		{"no database", "service=qat", "", "service=qat"},
		// A database name with a space is legal and would otherwise end the
		// keyword.
		{"quoted", "service=qat", "my db", "service=qat dbname='my db'"},
	}
	for _, c := range cases {
		target := &Target{Conn: config.Connection{Name: "test", DSN: c.dsn}}
		if got := target.DSN(c.database); got != c.want {
			t.Errorf("%s: DSN(%q) = %q, want %q", c.name, c.database, got, c.want)
		}
	}
}

// Whatever pgctl builds, libpq's own parser has to accept — otherwise pgx
// connects and pg_dump does not, or the reverse, and the failure appears
// minutes into an operation.
func TestBuiltDSNsParse(t *testing.T) {
	for _, dsn := range []string{
		"service=qat", "host=db port=5433", "postgres://db/postgres",
		"postgres://db/postgres?sslmode=require", "",
	} {
		target := &Target{Conn: config.Connection{Name: "test", DSN: dsn}}
		built := target.DSN("product-development")
		cfg, err := pgx.ParseConfig(built)
		if err != nil {
			// A service that does not exist locally is a resolution failure,
			// not a syntax one, and is not what this test is about.
			if dsn == "service=qat" {
				continue
			}
			t.Errorf("pgx rejected %q (built from %q): %v", built, dsn, err)
			continue
		}
		if cfg.Database != "product-development" {
			t.Errorf("%q resolved to database %q", built, cfg.Database)
		}
	}
}

func TestUnknownConnectionIsNamed(t *testing.T) {
	cfg, err := config.Parse([]byte("connections:\n  qat: \"host=localhost\"\n"), t.TempDir())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Resolve(t.Context(), cfg, ".", "prd", "db"); err == nil {
		t.Error("resolving an undeclared connection succeeded")
	}
}

func TestGSSAPIIsDisabledForSubprocesses(t *testing.T) {
	// libpq attempts a GSSAPI connection first, and where a Kerberos lookup
	// blackholes it blocks with no timeout — pg_dump then hangs producing
	// nothing. This cost hours to find once.
	target := &Target{Conn: config.Connection{Name: "qat"}}
	env := target.SubprocessEnv(nil)

	var found bool
	for _, kv := range env {
		if kv == "PGGSSENCMODE=disable" {
			found = true
		}
		if len(kv) > 11 && kv[:11] == "PGPASSWORD=" {
			t.Error("a password was put in the subprocess environment; ~/.pgpass is the place for it")
		}
	}
	if !found {
		t.Error("PGGSSENCMODE=disable is not set for subprocesses")
	}
}

func TestAnExplicitGSSModeIsRespected(t *testing.T) {
	// Someone who actually uses Kerberos must be able to say so.
	t.Setenv("PGGSSENCMODE", "prefer")
	target := &Target{Conn: config.Connection{Name: "qat"}}
	for _, kv := range target.SubprocessEnv(nil) {
		if kv == "PGGSSENCMODE=disable" {
			t.Error("pgctl overrode an explicit PGGSSENCMODE")
		}
	}
}
