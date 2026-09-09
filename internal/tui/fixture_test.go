package tui

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/pg"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// uiConfig is the world the frames are rendered against: a protected
// connection, a guarded one, an unreachable one, a set, a rule, a storage
// remote beside the local directory, and a retention policy on each — one of
// everything the interface has to be able to say something about.
//
// The remote is not decoration. A config with no remotes has one destination
// and the snapshot form asks nothing, so the destination toggles would have no
// frame at all — which is the arrangement in which a screen exists and nobody
// has ever seen it.
//
// Parsed rather than assembled, so it is the config loader's own value. A
// config typed as a struct literal here would be a world that does not exist:
// the loader fills in defaults, resolves storage paths and records warnings,
// and a fixture that skipped all three would render screens nobody can reach.
const uiConfig = `
connections:
  prd:
    dsn: "service=prd"
    protected: true
  qat:
    dsn: "service=qat"
    guarded: true
  scratch: "postgres://127.0.0.1/postgres"

storage:
  retention: { daily: 2 }
  remotes:
    - name: snapshots
      container: pg-snapshots
      retention: { daily: 7, weekly: 4 }

databases:
  exclude: [postgres]

sets:
  - name: claims
    database: product-development
    description: claims and everything a claim points at
    include: ["claims.*"]

rules:
  - table: quotes.quote
    where: "created_at > now() - interval '30 days'"
    why: "20 GB of jsonb payloads"
`

// A fixed world, so a captured frame says the same thing tomorrow.
//
// pgctl already had fixtures — app_test.go's model() and withSnapshot() — but
// they are built on time.Now(), which is right for an assertion about
// behaviour and fatal for a golden: "14h ago" becomes "15h ago" while you are
// at lunch. Everything here hangs off epoch, and the model's clock is pinned to
// it, so the only thing that can change a frame is a change to the interface.
//
// The one thing a fixture cannot do is catch what the author did not think of.
// pgctl's own header-overflow bug was invisible in its first capture because
// the fixture shortened the config path to "pgctl.yaml", and only a real
// temp-dir path was long enough to overflow — which is why the live capture in
// screenshot_probe_test.go is kept rather than replaced.
var epoch = time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC)

// errUnreachable is what a probe that could not connect carries. The real one
// comes out of libpq and names a host and a port; the frames only need it to be
// non-nil and to read like a connection failure.
var errUnreachable = errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")

// fixtureModel is the model with a world already in it, at a fixed instant.
func fixtureModel(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(uiConfig), dir)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	// A short, stable path. The real one is a temp directory whose name changes
	// every run, and a golden cannot hold that — the live capture is what
	// renders a path long enough to overflow the header.
	cfg.Source = "pgctl.yaml"
	// Relative, as a real config's is, and resolved by the engine against the
	// directory the config was loaded from — which is the temp dir. An absolute
	// path here leaked into a frame: the local destination's help says where the
	// snapshot would be kept, and under test that was a path with the test's
	// name and a run counter in it, so the golden changed on every run.
	// The path a real config writes, so a frame quoting it reads like one.
	cfg.Storage.Dir = ".pgctl/snapshots"

	m := New(engine.New(cfg))
	m.Now(epoch)
	m.SetSize(132, 38)

	m.probes["prd"] = &engine.Probe{
		Connection: "prd", Reachable: true, ServerVersion: 170004,
		Host: "prd.example", Port: 5432, User: "mbpiadmin",
		ProbedAt: epoch.Add(-30 * time.Second),
		Databases: []engine.DatabaseInfo{
			{Name: "product-development", Bytes: 11 << 30},
			{Name: "claims", Bytes: 48 << 20},
			{Name: "quote", Bytes: 20 << 30},
		},
	}
	m.probes["qat"] = &engine.Probe{
		Connection: "qat", Reachable: true, ServerVersion: 170004,
		Host: "qat.example", Port: 5432, User: "mbpiadmin",
		ProbedAt:  epoch.Add(-time.Minute),
		Databases: []engine.DatabaseInfo{{Name: "product-development", Bytes: 9 << 30}},
	}
	// One unreachable environment, because the marker in the Connections panel
	// answers "can I reach it" before the name answers "which is it", and a
	// capture where every row is reachable never shows that.
	m.probes["scratch"] = &engine.Probe{Connection: "scratch", Err: errUnreachable}

	m.setInfo[setKey("prd", "product-development", "claims")] = &setSummary{
		members: []string{"claims.policy_claim", "claims.claim_detail"},
		added:   []string{"operations.policy_contract", "public.address"},
	}
	m.liveTable["prd/product-development"] = []pg.TableInfo{
		{Name: "claims.policy_claim", Bytes: 180 << 20, EstimatedRows: 483187},
		{Name: "quotes.quote", Bytes: 20 << 30, EstimatedRows: 146597},
		{Name: "audit.logged_actions", Bytes: 4 << 30, EstimatedRows: 9_100_000},
	}
	return m
}

// fixtureSnapshot writes a manifest to the model's storage and loads it, at
// fixed times, and returns it for a test that needs to change one fact about
// it — an unfinished snapshot, say, which is a refusal rather than a screen.
func fixtureSnapshot(t *testing.T, m *Model) *snapshot.Manifest {
	t.Helper()
	id := "prd/product-development/20260828T030000Z"
	// StorageDir, not cfg.Storage.Dir: the config's is relative and the engine
	// is what resolves it. Writing to the unresolved one put a `snapshots`
	// directory in the working tree.
	dir := snapshot.Path(m.engine.StorageDir(), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	man := &snapshot.Manifest{
		ID:          id,
		Connection:  "prd",
		Database:    "product-development",
		StartedAt:   epoch.Add(-14 * time.Hour),
		FinishedAt:  epoch.Add(-14*time.Hour + 202*time.Second),
		Bytes:       2_040_893_635,
		Compression: "zstd:3",
		Jobs:        4,
		Tables: []snapshot.TableEntry{
			{Name: "claims.policy_claim", Data: config.DataAll, SourceBytes: 180 << 20, SourceRows: 483187},
			{Name: "quotes.quote", Data: config.DataFiltered, SourceBytes: 20 << 30, SourceRows: 146597, Rows: 4200},
			{Name: "audit.logged_actions", Data: config.DataNone},
		},
		Warnings: []string{"rule \"operations.gone\" matches no table in this database"},
	}
	if err := snapshot.Write(dir, man); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	m.entries = []*engine.Entry{{Manifest: man, At: []string{config.LocalStorage}}}
	return man
}

// fixtureRun is one finished operation and one still going, so the Runs panel,
// the step list and the log all have something to draw.
//
// Built from EVENTS rather than from steps, because that is what the model gets:
// the engine names a phase on everything it reports and run.go derives the steps
// from those names. A fixture that typed the steps out would be testing a
// renderer against a world invented three hundred lines away — and the whole
// point of the step list is that nothing here decides what the phases are.
func fixtureRun(m *Model) *runRecord {
	at := epoch.Add(-4 * time.Minute)
	ev := func(kind engine.EventKind, step, table, msg string, after time.Duration) engine.Event {
		return engine.Event{Kind: kind, Step: step, Table: table, Message: msg, At: at.Add(after)}
	}

	// Built through add(), the same door the engine's reporter comes through:
	// it routes a progress event to a different field from the rest, and a
	// fixture that assigned events directly would render a step list nothing
	// can produce. Decision 51, one level down.
	done := &runRecord{
		id:        "snapshot prd-0",
		kind:      "snapshot prd",
		explain:   "Copying product-development from prd. Nothing is written to prd.",
		total:     0,
		startedAt: at,
		endedAt:   at.Add(202 * time.Second),
		summary:   "snapshotted product-development from prd",
	}
	for _, e := range []engine.Event{
		ev(engine.EventStep, "dump", "", "pg_dump --format=directory --jobs=4", 0),
		ev(engine.EventTable, "dump", "claims.policy_claim", "180 MB", 30*time.Second),
		ev(engine.EventStep, "filtered copy", "", "1 table has a row filter", 150*time.Second),
		ev(engine.EventTable, "filtered copy", "quotes.quote", "4,200 rows", 152*time.Second),
		ev(engine.EventWarning, "filtered copy", "", `rule "operations.gone" matches no table in this database`, 160*time.Second),
		ev(engine.EventStep, "manifest", "", "2,040,893,635 bytes", 200*time.Second),
		ev(engine.EventDone, "manifest", "", "snapshot complete", 202*time.Second),
	} {
		done.add(e)
	}

	running := &runRecord{
		id:        "apply → qat-1",
		kind:      "apply → qat",
		explain:   "Replacing 38 tables on qat with the contents of prd/product-development/20260828T030000Z.",
		total:     38,
		startedAt: epoch.Add(-101 * time.Second),
		running:   true,
		cancel:    func() {},
	}
	for _, e := range []engine.Event{
		ev(engine.EventStep, "drop constraints", "", "81 foreign keys, 31 indexes", 100*time.Second),
		ev(engine.EventStep, "disable triggers", "", "111 user triggers on 33 tables", 110*time.Second),
		ev(engine.EventStep, "truncate", "", "children before parents", 120*time.Second),
		ev(engine.EventStep, "load", "", "42 archive entries, 4 jobs", 130*time.Second),
		ev(engine.EventTable, "load", "claims.policy_claim", "483,187 rows", 140*time.Second),
		ev(engine.EventTable, "load", "claims.claim_detail", "1.2M rows", 150*time.Second),
		ev(engine.EventProgress, "load", "", "504 MB of 1.9 GB", 155*time.Second),
	} {
		running.add(e)
	}

	m.runs = []*runRecord{done, running}
	m.active = running
	return running
}

// fixturePlan is a plan waiting to be confirmed: a set-level apply, widened,
// with a ring in its load order and a warning on it.
//
// The numbers are the ones the real thing produced against a 212-table copy of
// product-development — 38 tables, 81 foreign keys, 31 indexes, 111 triggers,
// two genuine rings — because a plan screen rendered against three tables and
// no cycles is a plan screen nobody has read.
func fixturePlan(m *Model) {
	m.openApply()
	if m.action == nil {
		return
	}
	m.action.stage = stagePlan
	m.action.plan = &engine.Plan{
		Snapshot: m.entries[0].Manifest,
		Target:   &engine.Target{Conn: config.Connection{Name: "qat", Guarded: true}},
		Local:    true,
		Selection: []string{
			"claims.policy_claim", "claims.claim_detail", "operations.policy_contract",
			"operations.cancellation", "operations.policy_contract_quote", "public.address",
		},
		Added: []string{"operations.policy_contract", "public.address"},
		Order: pg.Plan{
			Layers: []pg.Layer{
				{"public.address"},
				{"operations.cancellation", "operations.policy_contract", "operations.policy_contract_quote"},
				{"claims.policy_claim"},
				{"claims.claim_detail"},
			},
			Cycles: []pg.Layer{
				{"operations.cancellation", "operations.policy_contract", "operations.policy_contract_quote"},
			},
		},
		DropFKs:       make([]pg.FK, 81),
		DropIndexes:   make([]pg.Index, 31),
		TriggerTables: []pg.TriggerTable{{Table: "claims.policy_claim", Triggers: make([]string, 12)}},
		Bytes:         2_469_606_195,
		// The engine's own words, copied from plan.go's DataNone branch rather
		// than invented here. The first version of this fixture carried a
		// plausible-sounding warning about a filtered table with no sidecar,
		// which the engine does not emit and never has — a fixture asserting a
		// world that does not exist, which is what decision 51 is about, and it
		// sat in a golden looking right.
		Warnings: []string{
			"audit.logged_actions carries no data in this snapshot, " +
				"so applying it empties the table",
		},
	}
}

// fixtureManyTables gives the selected database more tables than any terminal
// can show, which is the ordinary case and was not in the fixture.
//
// 224 is the real number from a copy of product-development. The fixture had
// three, so no frame ever overflowed, no scrollbar was ever drawn, and the
// column arithmetic that a scrollbar changes was never exercised — which is how
// a listing three columns too wide passed 80 goldens twice over.
func fixtureManyTables(m *Model) {
	tables := make([]pg.TableInfo, 0, 224)
	for i := range 224 {
		// Names as long as the real ones, because the widest name is what the
		// flexible column is sized from.
		tables = append(tables, pg.TableInfo{
			Name:          fmt.Sprintf("operations.policy_contract_detail_%03d", i),
			Bytes:         int64(224-i) << 20,
			EstimatedRows: int64(224-i) * 6_000,
		})
	}
	m.liveTable["prd/product-development"] = tables
}

// fixtureTwoDatabases gives the connection snapshots of a second database, so
// the Snapshots panel is grouped.
//
// The fixture had one database's worth, which is the arrangement in which the
// panel's grouping never appears — and the bug it hides is the one a real
// estate hit on the first try: ten databases, every snapshot of one of them.
func fixtureTwoDatabases(m *Model) {
	first := m.entries[0].Manifest
	for _, db := range []string{"hasura", "ory"} {
		man := *first
		man.Database = db
		man.ID = "prd/" + db + "/20260828T030000Z"
		man.StartedAt = first.StartedAt.Add(-2 * time.Hour)
		man.FinishedAt = man.StartedAt.Add(41 * time.Second)
		man.Bytes = 467 << 20
		m.entries = append(m.entries, &engine.Entry{Manifest: &man, At: []string{config.LocalStorage, "snapshots"}})
	}
}
