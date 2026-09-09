package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/pg"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// TestCaptureScreens writes the UI's frames to files, in colour, for
// documentation.
//
// It renders through the real view code and fills the model from a real
// database, so what comes out is what the tool shows rather than a drawing of
// what it might. Skipped unless a destination is given:
//
//	PGCTL_SCREENSHOT_DIR=/tmp/shots \
//	PGCTL_SCREENSHOT_CONFIG=./pgctl.yaml \
//	PGCTL_SCREENSHOT_CONNECTION=devstack \
//	go test ./internal/tui/ -run CaptureScreens
func TestCaptureScreens(t *testing.T) {
	dir := os.Getenv("PGCTL_SCREENSHOT_DIR")
	if dir == "" {
		t.Skip("set PGCTL_SCREENSHOT_DIR to capture frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// A test has no terminal, so lipgloss would strip every colour. Forcing the
	// profile is what makes a captured frame look like the real one.
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)

	m := captureModel(t)
	m.width, m.height = 132, 38

	shot := func(name string) {
		t.Helper()
		m.now = time.Date(2026, 8, 31, 9, 14, 3, 0, time.UTC)
		path := filepath.Join(dir, name+".ansi")
		if err := os.WriteFile(path, []byte(run(m, m.width, m.height).View()), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	// 1. Where you land: connections, and what each one is.
	m.focus, m.paneFocus = panelConnections, false
	shot("01-connections")

	m.tabs[panelConnections] = 1
	shot("02-connection-databases")

	m.tabs[panelConnections] = 2
	shot("03-connection-config")

	// 2. A database's live tables, with the rule each will get.
	m.focus, m.tabs[panelDatabases] = panelDatabases, 0
	shot("04-database-tables")

	m.tabs[panelDatabases] = 1
	shot("05-database-rules")

	// 3. A snapshot, inside and out.
	m.focus, m.tabs[panelSnapshots] = panelSnapshots, 0
	shot("06-snapshot-manifest")

	m.tabs[panelSnapshots] = 1
	shot("07-snapshot-tables")

	m.tabs[panelSnapshots] = 2
	shot("08-snapshot-warnings")

	// 4. A set, and what closing it would drag in.
	m.focus, m.tabs[panelSets] = panelSets, 0
	shot("09-set-members")

	m.tabs[panelSets] = 1
	shot("10-set-closure")

	// 5. The forms.
	m.focus = panelConnections
	m.openSnapshot()
	shot("11-form-snapshot")

	m.action = nil
	m.focus = panelSnapshots
	m.openApply()
	m.action.cursor = 1
	press(t, m, "right") // scope: the claims set
	shot("12-form-apply")

	// 6. The plan, which is the last thing before a destructive act.
	m.action.stage = stagePlan
	m.action.plan = &engine.RunPlan{
		Run:    m.snaps[len(m.snaps)-1],
		Target: "qat",
		Plans:  []*engine.Plan{capturePlan(m)},
	}
	// Half typed, because a guarded target's phrase is a FIELD now and the
	// half-typed state is the one worth looking at.
	if f := m.action.field("confirm"); f != nil {
		f.text, f.caret = "qa", 2
	}
	shot("13-plan-guarded")

	// 7. Something running.
	m.action = nil
	m.focus = panelRuns
	m.runs = append(m.runs, captureRun())
	m.lists[panelRuns].Reset()
	shot("14-running")

	m.tabs[panelRuns] = 1
	shot("14b-running-log")
	m.tabs[panelRuns] = 0

	m.runs[len(m.runs)-1].running = false
	m.runs[len(m.runs)-1].endedAt = m.now
	shot("15-run-finished")

	// 8. The keys.
	m.showHelp = true
	shot("16-help")
	m.showHelp = false

	// 9. Filtering.
	m.focus, m.filtering = panelDatabases, false
	m.filter.Text, m.filter.Cursor = "cl", 2
	shot("17-filter")

	entries, _ := os.ReadDir(dir)
	t.Logf("wrote %d frames to %s", len(entries), dir)
}

// captureModel builds a model from a real config and, where a connection is
// given, real data read from that server.
func captureModel(t *testing.T) *Model {
	t.Helper()

	cfg, err := config.Load(os.Getenv("PGCTL_SCREENSHOT_CONFIG"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Source = "pgctl.yaml"
	m := New(engine.New(cfg))

	name := os.Getenv("PGCTL_SCREENSHOT_CONNECTION")
	if name == "" {
		t.Fatal("set PGCTL_SCREENSHOT_CONNECTION to the connection to read")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Probe every declared connection, so the list shows the real mix of
	// reachable and not.
	for _, conn := range cfg.All() {
		p := m.engine.ProbeConnection(ctx, conn.Name)
		m.probes[conn.Name] = &p
	}

	probe := m.probes[name]
	if probe == nil || !probe.Reachable {
		t.Fatalf("%s is not reachable, so there is nothing real to capture", name)
	}
	// Put the readable connection first under the cursor.
	for i, conn := range cfg.All() {
		if conn.Name == name {
			m.lists[panelConnections].Select(i)
		}
	}

	// The largest database, which is the one worth showing.
	database, largest := probe.Databases[0].Name, probe.Databases[0].Bytes
	for _, db := range probe.Databases {
		if db.Bytes > largest {
			database, largest = db.Name, db.Bytes
		}
	}
	for i, db := range probe.Databases {
		if db.Name == database {
			m.lists[panelDatabases].Select(i)
		}
	}

	tables, err := m.engine.LiveTables(ctx, name, database)
	if err != nil {
		t.Fatalf("read tables: %v", err)
	}
	m.liveTable[liveKey(name, database)] = tables

	if len(cfg.Sets) > 0 {
		for _, set := range cfg.Sets {
			if set.Database != database {
				continue
			}
			members, added, err := m.engine.SetMembers(ctx, name, database, set.Name)
			m.setInfo[setKey(name, database, set.Name)] = &setSummary{
				members: members, added: added, err: err,
			}
		}
	}

	m.setEntries([]*engine.Entry{captureSnapshot(name, database, tables)})
	return m
}

// captureSnapshot describes a snapshot of the tables that were just read, so
// the sizes and row counts in the frames are the real ones.
func captureSnapshot(connection, database string, tables []pg.TableInfo) *engine.Entry {
	man := &snapshot.Manifest{
		SchemaVersion: snapshot.SchemaVersion,
		ID:            snapshot.NewID(connection, database, time.Date(2026, 8, 31, 3, 0, 0, 0, time.UTC)),
		Connection:    connection,
		Database:      database,
		StartedAt:     time.Date(2026, 8, 31, 3, 0, 0, 0, time.UTC),
		FinishedAt:    time.Date(2026, 8, 31, 3, 3, 22, 0, time.UTC),
		ServerVersion: 170004,
		PgDumpVersion: "17.8",
		Compression:   "zstd:3",
		Jobs:          8,
		Bytes:         2_040_893_635,
		Warnings: []string{
			"13 trigger(s) on retained tables call functions in excluded schema(s) — " +
				"restoring this snapshot will fail on every CREATE TRIGGER. First: " +
				"notify_hasura_user_profile_insert_INSERT on auth.user_profile calls " +
				"hdb_catalog.notify_hasura_user_profile_insert_INSERT. Keep the schema and " +
				"exclude its data with a rule instead.",
			`rule "operations.renamed_away" matches no table in this database`,
		},
	}
	var source int64
	for _, t := range tables {
		entry := snapshot.TableEntry{
			Name:        t.Name,
			Data:        config.DataAll,
			SourceBytes: t.Bytes,
			SourceRows:  t.EstimatedRows,
		}
		switch t.Name {
		case "quotes.quote":
			entry.Data = config.DataFiltered
			entry.Where = "created_at > now() - interval '30 days'"
			entry.Why = "20 GB of jsonb payloads; recent quotes are enough to work with"
			entry.Rows = 4_182
		case "audit.logged_actions":
			entry.Data = config.DataNone
			entry.Why = "audit history of another environment is not useful in this one"
		}
		source += t.Bytes
		man.Tables = append(man.Tables, entry)
	}
	return &engine.Entry{Manifest: man, At: []string{config.LocalStorage, "snapshots"}}
}

// capturePlan is a plan of the shape a set-level apply produces, with the load
// order that came out of the real schema on this machine.
func capturePlan(m *Model) *engine.Plan {
	run, _ := m.selectedSnapshot()
	member := run.Members[0].Manifest
	return &engine.Plan{
		Snapshot: member,
		Target: &engine.Target{
			Conn:     config.Connection{Name: "qat", Guarded: true},
			Database: member.Database,
		},
		Selection: make([]string, 38),
		Added: []string{
			"operations.remittance", "operations.remittance_status_type", "ory.identity",
			"public.account", "public.address", "public.contact", "shared.type",
		},
		Bytes:       2_469_606_195,
		DropFKs:     make([]pg.FK, 60),
		BlockingFKs: make([]pg.FK, 21),
		DropIndexes: make([]pg.Index, 31),
		TriggerTables: []pg.TriggerTable{
			{Table: "claims.policy_claim", Triggers: make([]string, 12)},
		},
		Order: pg.Plan{Layers: []pg.Layer{
			{"claims.document", "claims.invoice_document_type", "ory.identity", "public.contact", "shared.type_group"},
			{"claims.invoice", "operations.remittance", "public.account", "public.address", "shared.type"},
			{"claims.policy_claim", "claims.service_center"},
			{"claims.claim_service_center", "claims.policy_claim_concern", "claims.tax_rate"},
			{"claims.claim_detail"},
			{"claims.claim_detail_as400", "claims.remittance_claim_detail"},
		}},
		Warnings: []string{
			"operations.cancellation and operations.policy_contract reference each other, " +
				"so they load as one group with their foreign keys deferred",
		},
	}
}

func captureRun() *runRecord {
	started := time.Date(2026, 8, 31, 9, 12, 41, 0, time.UTC)
	r := &runRecord{
		kind:      "apply → qat",
		explain:   "Replacing 38 tables on qat with the contents of prd/product-development/20260831T030000Z.",
		startedAt: started,
		running:   true,
	}
	step := func(s, msg string) {
		r.events = append(r.events, engine.Event{Kind: engine.EventStep, Step: s, Message: msg})
	}
	step("fetch", "prd/product-development/20260831T030000Z from azure blob pgsnapshots/pg-snapshots (40 files for 38 tables)")
	r.events = append(r.events, engine.Event{Kind: engine.EventWarning,
		Message: "terminated 4 connection(s) to product-development"})
	step("prepare", "dropping 81 foreign keys and 31 indexes")
	step("prepare", "disabling user triggers on 33 tables")
	step("prepare", "truncating 38 tables")
	step("load", "pg_restore 42 entries, 8 jobs")
	step("rebuild", "recreating 31 indexes")
	r.add(engine.Event{Kind: engine.EventProgress, Step: "rebuild",
		Message: "idx_contact_name_trgm on public.contact"})
	return r
}
