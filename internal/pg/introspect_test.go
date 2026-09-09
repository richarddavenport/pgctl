package pg

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestIntrospectAgainstRealServer exercises the catalog queries against a live
// PostgreSQL. The queries read pg_catalog directly — the one part of pgctl
// whose correctness a unit test cannot establish — so this runs whenever a
// server is offered and skips when one is not.
//
//	PGCTL_TEST_DSN=postgres://user:pw@localhost:5432/db go test ./internal/pg/
func TestIntrospectAgainstRealServer(t *testing.T) {
	dsn := os.Getenv("PGCTL_TEST_DSN")
	if dsn == "" {
		t.Skip("set PGCTL_TEST_DSN to run against a real server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	cat, err := Introspect(ctx, conn, []string{"hdb_catalog"})
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	if cat.ServerVersion < 160000 {
		t.Fatalf("server version %d: pgctl needs 16 or newer for zstd", cat.ServerVersion)
	}
	// An empty cluster is a mis-aimed test rather than a broken query, and the
	// message has to say which: CI pointed this at a `postgres:17` service
	// container's own `postgres` database and read "no tables found" as if the
	// catalog query had failed.
	if len(cat.Tables) == 0 {
		t.Fatal("no tables in PGCTL_TEST_DSN's database — this test reads a " +
			"populated one; internal/pg/testdata/seed.sql builds the smallest " +
			"schema that exercises it")
	}
	if len(cat.FKs) == 0 {
		t.Fatal("tables but no foreign keys: either the constraint query is " +
			"wrong or the database has none, and only one of those is a bug")
	}

	// Every foreign key must name tables the table query also returned,
	// otherwise a plan built from this catalog would order around a table it
	// cannot see.
	for _, fk := range cat.FKs {
		if !cat.Graph.Has(fk.Child) {
			t.Errorf("fk %s references unknown child %s", fk.Name, fk.Child)
		}
		if !cat.Graph.Has(fk.Parent) {
			t.Errorf("fk %s references unknown parent %s", fk.Name, fk.Parent)
		}
	}

	// A closure of the whole database is the whole database, and it must be
	// orderable — a cycle here is a real finding about the schema, not a bug.
	all := cat.Graph.Tables()
	plan := cat.Graph.LoadOrder(all)
	if len(plan.Flat()) != len(all) {
		t.Errorf("LoadOrder covered %d of %d tables", len(plan.Flat()), len(all))
	}
	t.Logf("%d tables, %d foreign keys, %d load layers, %d cycle groups",
		len(cat.Tables), len(cat.FKs), len(plan.Layers), len(plan.Cycles))
	for _, c := range plan.Cycles {
		t.Logf("cycle group: %v", c)
	}

	idx, err := Indexes(ctx, conn, all)
	if err != nil {
		t.Fatalf("Indexes: %v", err)
	}
	t.Logf("%d droppable secondary indexes", len(idx))
}
