package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

const uiConfig = `
protect: [prd]

environments:
  - name: prd
    guarded: true
  - name: qat
    guarded: true
  - name: scratch

postgres:
  prd: { host: prd.example }
  qat: { host: qat.example }
  scratch: { host: 127.0.0.1 }

storage:
  kind: local
  retention: { daily: 7, weekly: 4 }

databases:
  - name: product-development
  - name: claims

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

func model(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(uiConfig), dir)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.Source = filepath.Join(dir, "pgctl.yaml")
	cfg.Storage.Dir = filepath.Join(dir, "snapshots")

	m := New(engine.New(cfg))
	m.width, m.height = 140, 44
	return m
}

// press sends keys, discarding the commands: nothing under test needs a
// command to run, and running them would reach for a database.
func press(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "left":
			msg = tea.KeyMsg{Type: tea.KeyLeft}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.Update(msg)
	}
}

func withSnapshot(t *testing.T, m *Model) *snapshot.Manifest {
	t.Helper()
	id := "prd/product-development/20260828T030000Z"
	dir := snapshot.Path(m.cfg.Storage.Dir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	man := &snapshot.Manifest{
		ID:          id,
		Environment: "prd",
		Database:    "product-development",
		StartedAt:   time.Now().Add(-14 * time.Hour),
		FinishedAt:  time.Now().Add(-13 * time.Hour),
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
	m.entries = []*engine.Entry{{Manifest: man, Local: true}}
	return man
}

func TestPanelsAreAHierarchy(t *testing.T) {
	m := model(t)
	withSnapshot(t, m)

	// prd is first. Its snapshot is listed.
	if got := len(m.snapshots()); got != 1 {
		t.Fatalf("prd has %d snapshots, want 1", got)
	}
	// Moving to another environment must change what the snapshots panel is
	// about, or the hierarchy the layout implies is a lie.
	press(t, m, "down")
	if env, _ := m.selectedEnv(); env.Name != "qat" {
		t.Fatalf("cursor moved to %q, want qat", env.Name)
	}
	if got := len(m.snapshots()); got != 0 {
		t.Errorf("qat shows %d snapshots, want none — they belong to prd", got)
	}

	// And so must the database selection.
	press(t, m, "up", "2", "down")
	if db, _ := m.selectedDatabase(); db.Name != "claims" {
		t.Fatalf("database cursor is on %q, want claims", db.Name)
	}
	if got := len(m.snapshots()); got != 0 {
		t.Errorf("the claims database shows %d snapshots of product-development", got)
	}
	if got := len(m.sets()); got != 0 {
		t.Errorf("the claims database shows %d sets declared for product-development", got)
	}
}

func TestProtectedEnvironmentIsNeverAnApplyTarget(t *testing.T) {
	m := model(t)
	withSnapshot(t, m)

	targets, _ := m.applyTargets()
	for _, name := range targets {
		if name == "prd" {
			t.Fatal("prd is offered as an apply target")
		}
	}
	if len(targets) != 2 {
		t.Errorf("targets = %v, want qat and scratch", targets)
	}
}

func TestApplyFormOffersEveryScopeTheCLIHas(t *testing.T) {
	m := model(t)
	withSnapshot(t, m)
	m.focus = panelSnapshots

	press(t, m, "a")
	if m.action == nil {
		t.Fatal("a did not open the apply form")
	}
	scope := m.action.field("scope")
	if scope == nil {
		t.Fatal("the apply form has no scope field")
	}
	want := []string{"whole database", "set:claims", "tables"}
	if strings.Join(scope.options, ",") != strings.Join(want, ",") {
		t.Errorf("scope options = %v, want %v", scope.options, want)
	}
	for _, key := range []string{"target", "scope", "tables", "widen"} {
		if m.action.field(key) == nil {
			t.Errorf("the apply form has no %q field", key)
		}
	}
}

func TestFieldsThatCannotApplyAreDisabledNotHidden(t *testing.T) {
	m := model(t)
	withSnapshot(t, m)
	m.focus = panelSnapshots
	press(t, m, "a")

	// Scope starts at "whole database", where widening means nothing and a
	// table list is ignored.
	m.syncAction()
	if !m.action.field("widen").disabled {
		t.Error("widen is enabled for a whole-database apply")
	}
	if !m.action.field("tables").disabled {
		t.Error("the table list is enabled when the scope is not `tables`")
	}

	// Choosing the set enables widening, because now it decides something.
	m.action.cursor = 1
	press(t, m, "right")
	if m.action.value("scope") != "set:claims" {
		t.Fatalf("scope = %q after one step", m.action.value("scope"))
	}
	if m.action.field("widen").disabled {
		t.Error("widen is still disabled for a set-level apply")
	}

	// A disabled field is skipped by the cursor but stays on screen, so the
	// form's shape does not change under the operator.
	view := m.viewForm()
	if !strings.Contains(view, "Tables") {
		t.Error("the disabled table field vanished from the form")
	}
}

func TestSnapshotFormDefaultsToTheDatabaseInFocus(t *testing.T) {
	m := model(t)
	// The panel selection is a statement of intent: someone looking at one
	// database rarely means all six.
	press(t, m, "2", "down")
	if db, _ := m.selectedDatabase(); db.Name != "claims" {
		t.Fatalf("selected database is %q", db.Name)
	}

	press(t, m, "n")
	if m.action == nil {
		t.Fatal("n did not open the snapshot form")
	}
	if got := m.action.chosenDatabases(); len(got) != 1 || got[0] != "claims" {
		t.Errorf("databases = %v, want just claims", got)
	}

	// And every database is still reachable from the form.
	f := m.action.field("databases")
	if len(f.options) != 2 {
		t.Errorf("the form offers %d databases, want both declared ones", len(f.options))
	}
	m.action.cursor = 1
	press(t, m, "a")
	if got := m.action.chosenDatabases(); len(got) != 2 {
		t.Errorf("`a` selected %d databases, want all of them", len(got))
	}
	press(t, m, "n")
	if got := m.action.chosenDatabases(); len(got) != 0 {
		t.Errorf("`n` left %d databases selected, want none", len(got))
	}
}

func TestSnapshotFormDisablesUploadWhenThereIsNowhereToUploadTo(t *testing.T) {
	m := model(t)
	press(t, m, "n")
	push := m.action.field("push")
	if !push.disabled {
		t.Error("upload is offered with local storage configured")
	}
	if push.reason == "" {
		t.Error("the disabled upload field does not say why")
	}
}

func TestFilterNarrowsOnlyTheFocusedPanel(t *testing.T) {
	m := model(t)
	withSnapshot(t, m)

	press(t, m, "/", "q", "a", "t", "enter")
	if got := len(m.environments()); got != 1 {
		t.Errorf("the filter matched %d environments, want qat alone", got)
	}
	// The databases panel is not focused, so it keeps everything: a filter
	// that emptied every panel would read as data loss.
	if got := len(m.databases()); got != 2 {
		t.Errorf("the filter also narrowed the databases panel to %d", got)
	}
	press(t, m, "esc")
	if got := len(m.environments()); got != 3 {
		t.Errorf("esc left %d environments, want the filter cleared", got)
	}
}

func TestIncompleteSnapshotCannotBeApplied(t *testing.T) {
	m := model(t)
	man := withSnapshot(t, m)
	man.FinishedAt = time.Time{}
	m.focus = panelSnapshots

	press(t, m, "a")
	if m.action != nil {
		t.Error("an unfinished snapshot opened the apply form")
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "did not finish") {
		t.Errorf("err = %v, want a refusal naming the reason", m.err)
	}
}

func TestSnapshotDetailShowsWhatIsInIt(t *testing.T) {
	m := model(t)
	withSnapshot(t, m)
	m.focus = panelSnapshots
	m.now = time.Now()

	manifest := m.viewSnapshotTab(0, 90)
	for _, want := range []string{"1.9 GB", "zstd:3", "PostgreSQL", "filtered", "no data"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("the manifest tab does not mention %q:\n%s", want, manifest)
		}
	}

	tables := m.viewSnapshotTab(1, 90)
	for _, want := range []string{"quotes.quote", "20.0 GB", "4k rows", "audit.logged_actions", "none"} {
		if !strings.Contains(tables, want) {
			t.Errorf("the tables tab does not mention %q:\n%s", want, tables)
		}
	}

	warnings := m.viewSnapshotTab(2, 90)
	if !strings.Contains(warnings, "matches no table") {
		t.Errorf("the warnings tab hides the warning:\n%s", warnings)
	}
}

func TestPruneRefusesWithoutAPolicy(t *testing.T) {
	m := model(t)
	m.cfg.Storage.Retention.Daily = 0
	m.cfg.Storage.Retention.Weekly = 0

	press(t, m, "p")
	if m.action != nil {
		t.Error("prune opened with no retention configured")
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "retention") {
		t.Errorf("err = %v, want an explanation about retention", m.err)
	}
}

func TestHelpListsEveryActionKey(t *testing.T) {
	m := model(t)
	press(t, m, "?")
	if !m.showHelp {
		t.Fatal("? did not open the help")
	}
	help := m.viewHelp()
	for _, key := range []string{"n", "a", "m", "p", "x", "r", "/"} {
		if !strings.Contains(help, key) {
			t.Errorf("the help does not document %q", key)
		}
	}
	press(t, m, "j")
	if m.showHelp {
		t.Error("a keypress did not close the help")
	}
}

func TestViewRendersWithoutData(t *testing.T) {
	// The first frame is drawn before any probe or listing has come back, and
	// it must not panic on the empty state.
	m := model(t)
	m.now = time.Now()
	view := m.View()
	for _, want := range []string{"Environments", "Databases", "Snapshots", "Sets", "Runs"} {
		if !strings.Contains(view, want) {
			t.Errorf("the first frame is missing the %s panel", want)
		}
	}
	if !strings.Contains(view, "none — press n") {
		t.Error("the empty snapshots panel does not say what to do")
	}
}

func TestRendersBeforeAWindowSizeArrives(t *testing.T) {
	// A real terminal sends a size immediately; a pty with none attached never
	// does. A UI that waits for one renders nothing at all under `script`.
	m := model(t)
	m.width, m.height = 0, 0
	m.now = time.Now()

	view := m.View()
	if strings.Contains(view, "starting") {
		t.Errorf("the UI is still waiting for a size:\n%s", view)
	}
	if !strings.Contains(view, "Environments") {
		t.Errorf("nothing rendered without a window size:\n%s", view)
	}
}
