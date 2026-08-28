package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// testConfig points pgctl at a local cluster, with a filtered rule on the
// table that motivated filtering in the first place.
const testConfig = `
postgres:
  local:
    host: 127.0.0.1
    port: 5432

defaults:
  jobs: 4

storage:
  kind: local
  dir: snapshots

databases:
  - name: product-development
    excludeSchemas: [hdb_catalog, audit]

sets:
  - name: claims
    database: product-development
    include: ["claims.*"]

rules:
  - table: quotes.quote
    where: "created_at > now() - interval '7 days'"
    why: "20 GB of jsonb payloads; recent quotes are enough to work with"
`

// dumpEnv is the environment a local test dump runs against. There is no
// secrets file, so the user comes from the process environment via the
// credentials key the test config names.
func testEngine(t *testing.T, storage string) *Engine {
	t.Helper()
	if os.Getenv("PGCTL_TEST_DSN") == "" {
		t.Skip("set PGCTL_TEST_DSN to run against a real server")
	}
	cfg, err := config.Parse([]byte(testConfig), t.TempDir())
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.Storage.Dir = storage
	e := New(cfg)
	e.root = t.TempDir()
	return e
}

// TestDumpAgainstRealServer takes a real snapshot: pg_dump in directory format
// plus a filtered COPY sidecar, and a manifest describing both.
func TestDumpAgainstRealServer(t *testing.T) {
	storage := t.TempDir()
	e := testEngine(t, storage)

	// With no secrets file, credentials come from the process environment
	// under the configured keys.
	if os.Getenv("POSTGRES_USERNAME") == "" {
		t.Skip("set POSTGRES_USERNAME to the user to connect as")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	var events []Event
	report := Reporter(func(e Event) { events = append(events, e) })

	at := time.Date(2026, 8, 28, 3, 0, 0, 0, time.UTC)
	m, err := e.Dump(ctx, DumpRequest{
		Connection: "local",
		Database:   "product-development",
		At:         at,
	}, report)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	if m.ID != "local/product-development/20260828T030000Z" {
		t.Errorf("ID = %q", m.ID)
	}
	if !m.Complete() {
		t.Error("manifest has no FinishedAt")
	}
	if m.Bytes == 0 {
		t.Error("snapshot reports no size")
	}
	if m.PgDumpVersion == "" {
		t.Error("pg_dump version not recorded")
	}
	if len(m.ForeignKeys) == 0 {
		t.Error("source foreign keys not recorded")
	}

	// Excluded schemas must be absent from the manifest entirely: a table
	// listed but not dumped would make a later restore plan around data that
	// is not there.
	for _, tbl := range m.Tables {
		if got := tbl.Name; len(got) > 12 && got[:12] == "hdb_catalog." {
			t.Errorf("excluded schema present: %s", got)
		}
	}

	// The filtered table's sidecar must exist, and the manifest must know
	// which columns it holds — without them the file cannot be loaded.
	filtered := m.Filtered()
	if len(filtered) != 1 {
		t.Fatalf("Filtered = %v, want quotes.quote alone", filtered)
	}
	q := filtered[0]
	if q.Name != "quotes.quote" {
		t.Errorf("filtered table = %q", q.Name)
	}
	if len(q.Columns) == 0 {
		t.Error("filtered table recorded no column list")
	}
	dir := snapshot.Path(storage, m.ID)
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(q.File))); err != nil {
		t.Errorf("sidecar missing: %v", err)
	}

	// pg_dump's own output, and a manifest that round-trips.
	if _, err := os.Stat(filepath.Join(dir, snapshot.DumpDir, "toc.dat")); err != nil {
		t.Errorf("pg_dump table of contents missing: %v", err)
	}
	back, err := snapshot.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if back.ID != m.ID || len(back.Tables) != len(m.Tables) {
		t.Errorf("manifest did not round-trip")
	}

	t.Logf("%d tables, %s on disk, %d rows in %s",
		len(m.Tables), humanBytes(m.Bytes), q.Rows, q.Name)
	for _, ev := range events {
		if ev.Kind == EventWarning {
			t.Logf("warning: %s", ev.Message)
		}
	}
}
