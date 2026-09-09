package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/richarddavenport/tuikit/app"
	"github.com/richarddavenport/tuikit/comp"
	"github.com/richarddavenport/tuikit/harness"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/pg"
)

// press sends keys through a runner, so every press REDRAWS.
//
// Not optional since the cursors became components: a comp.List records a Move
// and resolves it when it DRAWS, because that is the only moment it knows which
// rows exist — so a test that pressed ↓ and then asked what was selected got
// the answer from before the keystroke. Bubble Tea draws between every message
// anyway; a helper that did not was the only thing lying about it.
func press(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	harness.Press(run(m, m.width, m.height), keys...)
}

// loaded is the fixture world with a snapshot in it, which is the state the
// interface is in for all but the first second of a session.
func loadedModel(t *testing.T) *Model {
	t.Helper()
	m := fixtureModel(t)
	_ = fixtureSnapshot(t, m)
	return m
}

func TestPanelsAreAHierarchy(t *testing.T) {
	m := loadedModel(t)

	// prd is first. Its snapshot is listed.
	if got := len(m.snapshots()); got != 1 {
		t.Fatalf("prd has %d snapshots, want 1", got)
	}
	// Moving to another connection must change what the snapshots panel is
	// about, or the hierarchy the layout implies is a lie.
	press(t, m, "down")
	if conn, _ := m.selectedConn(); conn.Name != "qat" {
		t.Fatalf("cursor moved to %q, want qat", conn.Name)
	}
	if got := len(m.snapshots()); got != 0 {
		t.Errorf("qat shows %d snapshots, want none — they belong to prd", got)
	}

	// The database selection reorders the Snapshots panel and does NOT empty
	// it. Emptying it is what this test used to assert, and it was the bug: on
	// a server with ten databases the cursor lands on the first alphabetically
	// and every snapshot anyone takes is of another one, so the panel read
	// "none" beside a detail pane reading "1 snapshot, newest just now".
	press(t, m, "up", "2", "down")
	if db, _ := m.selectedDatabase(); db.Name != "claims" {
		t.Fatalf("database cursor is on %q, want claims", db.Name)
	}
	if got := m.snapshotCount(); got != 1 {
		t.Errorf("the claims database hides %d of prd's snapshots", 1-got)
	}

	// Sets ARE per database, and stay that way: a set names tables in one
	// database and means nothing against another.
	if got := len(m.sets()); got != 0 {
		t.Errorf("the claims database shows %d sets declared for product-development", got)
	}
}

// The selected database is FIRST in the Snapshots panel, which is what is left
// of the hierarchy once the panel stopped filtering.
func TestTheSelectedDatabasesSnapshotsComeFirst(t *testing.T) {
	m := loadedModel(t)
	// A second database with a newer snapshot, so "first" cannot be an accident
	// of the order they were listed in.
	newer := *m.entries[0].Manifest
	newer.ID = "prd/hasura/20260828T090000Z"
	newer.Database = "hasura"
	newer.StartedAt = m.entries[0].Manifest.StartedAt.Add(time.Hour)
	m.entries = append(m.entries, &engine.Entry{Manifest: &newer, At: []string{config.LocalStorage}})

	// With hasura selected it leads, and it would anyway — it is the newer.
	press(t, m, "2", "down", "down")
	if db, _ := m.selectedDatabase(); db.Name != "quote" {
		t.Fatalf("database cursor is on %q, want quote", db.Name)
	}
	// quote has no snapshots at all, so neither group is the selected one and
	// the newest leads.
	rows := m.snapshots()
	if rows[0].heading != "hasura" {
		t.Errorf("groups lead with %q, want the newest (hasura)", rows[0].heading)
	}

	// Selecting product-development, whose snapshot is OLDER, puts it first.
	// The fixture lists databases as the server reports them, so g is
	// product-development rather than the alphabetical first.
	press(t, m, "g")
	if db, _ := m.selectedDatabase(); db.Name != "product-development" {
		t.Fatalf("database cursor is on %q after g, want product-development", db.Name)
	}
	rows = m.snapshots()
	if rows[0].heading != "product-development" {
		t.Errorf("groups lead with %q, want the selected product-development",
			rows[0].heading)
	}

	// And the headings are not selectable, so the cursor and the detail pane
	// cannot disagree about which snapshot is in front of you.
	press(t, m, "3")
	entry, ok := m.selectedSnapshot()
	if !ok {
		t.Fatal("nothing is selected in a panel with two snapshots in it")
	}
	if entry.Manifest.Database != "product-development" {
		t.Errorf("selected %q, want the first snapshot under the leading heading",
			entry.Manifest.ID)
	}
}

func TestProtectedConnectionIsNeverAnApplyTarget(t *testing.T) {
	m := loadedModel(t)

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
	m := loadedModel(t)
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
	for _, key := range []string{"target", "scope", "tables", "widen", "confirm"} {
		if m.action.field(key) == nil {
			t.Errorf("the apply form has no %q field", key)
		}
	}
}

func TestFieldsThatCannotApplyAreDisabledNotHidden(t *testing.T) {
	m := loadedModel(t)
	m.focus = panelSnapshots
	press(t, m, "a")

	// Scope starts at "whole database", where widening means nothing and a
	// table list is ignored.
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
	frame := harness.Strip(run(m, m.width, m.height).View())
	if !strings.Contains(frame, "Tables") {
		t.Errorf("the disabled table field vanished from the form:\n%s", frame)
	}

	// And the cursor steps over it: from Scope the next field is Widen, not
	// the table list it would land on if disabled meant "still in the way".
	before := m.action.cursor
	press(t, m, "down")
	if got := m.action.fields[m.action.cursor].key; got == "tables" {
		t.Errorf("the cursor moved from %d onto the disabled %q field", before, got)
	}
}

// The guarded target's phrase is a field, and it appears with the CHOICE rather
// than at the end.
//
// qat is guarded and scratch is not, so changing the target changes whether the
// phrase is required — and an operator scanning the form sees which of the two
// they picked instead of finding out after the plan.
func TestTheGuardedTargetsPhraseFollowsTheTarget(t *testing.T) {
	m := loadedModel(t)
	m.focus = panelSnapshots
	press(t, m, "a")

	confirm := m.action.field("confirm")
	if target := m.action.value("target"); target != "qat" {
		t.Fatalf("the form opened on target %q, want qat", target)
	}
	if confirm.disabled {
		t.Error("qat is guarded and the phrase field is disabled")
	}

	// The next target is scratch, which is not guarded.
	press(t, m, "right")
	if target := m.action.value("target"); target == "qat" {
		t.Fatal("→ did not change the target")
	}
	if !m.action.field("confirm").disabled {
		t.Errorf("%s is not guarded and the phrase is still required",
			m.action.value("target"))
	}
	if m.action.field("confirm").reason == "" {
		t.Error("the disabled phrase field does not say why")
	}
}

// A plan against a guarded target is not applied until the phrase matches, and
// the refusal names what is missing rather than failing silently.
func TestAGuardedTargetIsNotAppliedWithoutItsName(t *testing.T) {
	m := loadedModel(t)
	m.focus = panelSnapshots
	r := run(m, 132, 38)
	fixturePlan(m)
	if m.action == nil || m.action.plan == nil {
		t.Fatal("the plan fixture did not open a plan")
	}

	harness.Press(r, "enter")
	if m.active != nil {
		t.Fatal("enter applied a plan to a guarded target with no phrase typed")
	}
	if m.action == nil {
		t.Fatal("the form closed instead of asking for the phrase")
	}
	if m.action.stage != stageForm {
		t.Errorf("stage = %v, want the form back so the phrase can be typed", m.action.stage)
	}
	if m.action.err == nil || !strings.Contains(m.action.err.Error(), "guarded") {
		t.Errorf("err = %v, want a refusal naming the guard", m.action.err)
	}
}

// The forms do not ask what the panels already said.
//
// Which connection and which database are the panel selections, and a form that
// asked again would be a second way to say the same thing — two ways that can
// disagree. The connection field went first (it offered one server's name beside
// another server's databases); the databases multi-select went the same way, and
// this is what holds both closed.
func TestTheSnapshotFormDoesNotAskWhatThePanelsSaid(t *testing.T) {
	m := loadedModel(t)
	press(t, m, "2", "down")
	db, _ := m.selectedDatabase()
	if db.Name != "claims" {
		t.Fatalf("selected database is %q", db.Name)
	}

	press(t, m, "n")
	if m.action == nil {
		t.Fatal("n did not open the snapshot form")
	}
	for _, key := range []string{"connection", "databases"} {
		if f := m.action.field(key); f != nil {
			t.Errorf("the snapshot form asks for %q; the panel selection is that", key)
		}
	}
	if m.action.conn == "" || m.action.database != "claims" {
		t.Errorf("the form recorded %q/%q, want the panel's prd/claims",
			m.action.conn, m.action.database)
	}
	// And it says so, because a form that acts on something it does not name is
	// worse than one that asks.
	if !strings.Contains(m.action.title, "claims") {
		t.Errorf("title = %q, does not name the database it will snapshot", m.action.title)
	}

	// Moving the panel cursor is how you change it, so the next form is about
	// the next database.
	press(t, m, "esc", "up", "n")
	if m.action.database != "product-development" {
		t.Errorf("after moving the cursor the form is about %q", m.action.database)
	}
}

// A connection with no databases to act on is a refusal that says which of the
// three reasons it is.
//
// "Nothing selected" covers a connection never reached, one being reached right
// now, and one that answered by failing — and they need different things done
// about them.
func TestSnapshotRefusesWhenThereIsNoDatabaseAndSaysWhy(t *testing.T) {
	m := loadedModel(t)
	// scratch is the unreachable one in the fixture.
	press(t, m, "1", "G")
	if conn, _ := m.selectedConn(); conn.Name != "scratch" {
		t.Fatalf("cursor is on %q, want scratch", conn.Name)
	}

	press(t, m, "n")
	if m.action != nil {
		t.Error("the snapshot form opened for a connection with no databases")
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "unreachable") {
		t.Errorf("err = %v, want the refusal to name the reason", m.err)
	}

	// A connection nobody has reached yet is a different answer.
	m2 := loadedModel(t)
	m2.probes = map[string]*engine.Probe{}
	press(t, m2, "n")
	if m2.err == nil || !strings.Contains(m2.err.Error(), "not been reached") {
		t.Errorf("err = %v, want the not-reached-yet reason", m2.err)
	}
}

// The snapshot form asks WHERE, with nothing pre-ticked.
//
// There is no default destination once a remote is declared: a snapshot is
// gigabytes, and whether it lands in a shared account a colleague restores from
// is not something to infer from silence. So an unanswered form is refused
// rather than defaulted to local.
func TestTheSnapshotFormAsksWhereWithNoDefault(t *testing.T) {
	m := loadedModel(t)
	press(t, m, "n")
	if m.action == nil {
		t.Fatal("n did not open the snapshot form")
	}

	// ONE field — a choice with every destination in it, local first, in config
	// order. Not a toggle each: "local no / snapshots yes" is two settings a
	// reader has to combine themselves, and this is one question.
	if len(m.action.fields) != 1 || m.action.fields[0].kind != fieldMulti {
		t.Fatalf("%d fields, want one multi-select", len(m.action.fields))
	}
	f := m.action.fields[0]
	if strings.Join(f.options, ",") != "local,snapshots" {
		t.Errorf("options = %v, want every destination, local first", f.options)
	}
	if got := m.chosenDestinations(); len(got) != 0 {
		t.Errorf("%v is pre-chosen; nothing should be", got)
	}

	// Enter with nothing chosen is a refusal that says how to answer, and the
	// form stays open on it.
	press(t, m, "enter")
	if m.active != nil {
		t.Fatal("enter started a snapshot with no destination")
	}
	if m.action == nil {
		t.Fatal("the form closed without doing anything")
	}
	if m.action.err == nil || !strings.Contains(m.action.err.Error(), "space") {
		t.Errorf("err = %v, want a refusal that says which key chooses", m.action.err)
	}

	// The arrows move within the list and space chooses, so the remote alone is
	// reachable — the "do not fill my laptop" answer, where local is simply not
	// among them.
	press(t, m, "down", "space")
	if got := m.chosenDestinations(); len(got) != 1 || got[0] != "snapshots" {
		t.Errorf("destinations = %v, want the remote alone", got)
	}
	// And `a` takes everything, for the "keep it here and upload it" case.
	press(t, m, "a")
	if got := m.chosenDestinations(); len(got) != 2 {
		t.Errorf("destinations = %v, want both", got)
	}
}

// With no remote declared there is one place a snapshot can go, so the form
// asks nothing at all — and says why in its own words rather than offering a
// disabled toggle.
func TestTheSnapshotFormAsksNothingWhenThereIsOneDestination(t *testing.T) {
	m := loadedModel(t)
	m.cfg.Storage.Remotes = nil

	press(t, m, "n")
	if len(m.action.fields) != 0 {
		t.Errorf("%d fields offered for a single destination", len(m.action.fields))
	}
	if !strings.Contains(m.action.explain, "stays on this machine") {
		t.Errorf("explain = %q, does not say where it goes", m.action.explain)
	}
	if got := m.chosenDestinations(); len(got) != 1 || got[0] != config.LocalStorage {
		t.Errorf("destinations = %v, want local", got)
	}
}

func TestFilterNarrowsOnlyTheFocusedPanel(t *testing.T) {
	m := loadedModel(t)

	// The databases panel, narrowed to one of its three.
	press(t, m, "2", "/", "c", "l", "enter")
	if got := len(m.databases()); got != 1 {
		t.Errorf("the filter matched %d databases, want claims alone", got)
	}
	// The connections panel is not focused, so it keeps everything: a filter
	// that emptied every panel would read as data loss.
	if got := len(m.connections()); got != 3 {
		t.Errorf("the filter also narrowed the connections panel to %d", got)
	}
	press(t, m, "esc")
	if got := len(m.databases()); got != 3 {
		t.Errorf("esc left %d databases, want the filter cleared", got)
	}
}

// The filter has a caret, so a typo is corrected where it happened.
//
// The version this replaces could only append and delete from the end, which
// meant a mistyped connection name was retyped from the mistake onwards.
func TestTheFilterCaretCanBeMoved(t *testing.T) {
	m := loadedModel(t)
	press(t, m, "/", "q", "t", "left", "a")
	if m.filter.Text != "qat" {
		t.Errorf("filter = %q, want qat — the caret did not move", m.filter.Text)
	}
	if got := len(m.connections()); got != 1 {
		t.Errorf("the filter matched %d connections, want qat alone", got)
	}
}

func TestIncompleteSnapshotCannotBeApplied(t *testing.T) {
	m := fixtureModel(t)
	man := fixtureSnapshot(t, m)
	// No finish time: snapshot.Manifest.Complete() is "has a finish time", and
	// an interrupted dump is the case it exists for. The engine refuses it and
	// so does the form, before opening.
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
	m := loadedModel(t)
	m.focus = panelSnapshots

	manifest := paneText(m.viewSnapshotTab(0, 90))
	for _, want := range []string{"1.9 GB", "zstd:3", "PostgreSQL", "filtered", "no data"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("the manifest tab does not mention %q:\n%s", want, manifest)
		}
	}

	tables := paneText(m.viewSnapshotTab(1, 90))
	for _, want := range []string{"quotes.quote", "20.0 GB", "4k rows", "audit.logged_actions", "none"} {
		if !strings.Contains(tables, want) {
			t.Errorf("the tables tab does not mention %q:\n%s", want, tables)
		}
	}

	warnings := paneText(m.viewSnapshotTab(2, 90))
	if !strings.Contains(warnings, "matches no table") {
		t.Errorf("the warnings tab hides the warning:\n%s", warnings)
	}
}

func TestPruneRefusesWithoutAPolicy(t *testing.T) {
	m := loadedModel(t)
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

// The help documents every key that acts, and it SCROLLS.
//
// The full list is longer than a 24-line terminal, and a help screen that shows
// two thirds of itself with no way to reach the rest is the least useful thing
// on the screen.
func TestHelpListsEveryActionKeyAndScrolls(t *testing.T) {
	m := loadedModel(t)
	press(t, m, "?")
	if !m.showHelp {
		t.Fatal("? did not open the help")
	}

	documented := map[string]bool{}
	for _, section := range KeySections() {
		for _, k := range section.Keys {
			documented[k.Key] = true
		}
	}
	for _, key := range []string{"n", "a", "m", "p", "x", "r", "/", "ctrl+p"} {
		if !documented[key] {
			t.Errorf("the help does not document %q", key)
		}
	}

	m.SetSize(80, 24)
	press(t, m, "j")
	if !m.showHelp {
		t.Error("j closed the help; it should scroll it")
	}
	if m.helpOffset == 0 {
		t.Error("j did not scroll the help")
	}
	press(t, m, "k")
	if m.helpOffset != 0 {
		t.Errorf("k left the help at offset %d, want 0", m.helpOffset)
	}
	press(t, m, "x")
	if m.showHelp {
		t.Error("a key that is not a scroll did not close the help")
	}
	if m.helpOffset != 0 {
		t.Error("closing the help did not reset its scroll")
	}
}

// The command directory lists what cannot run, with the reason.
//
// Hiding a refused command answers "why is apply not here" with nothing. This
// is where a reader finds out that the snapshot they are looking at did not
// finish, before pressing the key that says so.
func TestTheCommandDirectoryExplainsWhatItCannotDo(t *testing.T) {
	m := fixtureModel(t) // no snapshots at all
	press(t, m, "ctrl+p")
	if !m.showCommand {
		t.Fatal("ctrl+p did not open the directory")
	}

	var apply, snapshot bool
	for _, g := range m.commandGroups() {
		for _, item := range g.Items {
			switch item.Key {
			case "a":
				apply = true
				if item.Runnable() {
					t.Error("apply is offered with no snapshot selected")
				}
				if !strings.Contains(item.Hint, "no snapshot") {
					t.Errorf("the refused apply says %q, which does not say why", item.Hint)
				}
			case "n":
				snapshot = true
				if !item.Runnable() {
					t.Errorf("snapshot is refused: %q", item.Hint)
				}
			}
		}
	}
	if !apply || !snapshot {
		t.Error("the directory does not list the operations")
	}

	// Running an item is pressing its key, so a command cannot mean something
	// different from the key beside it in the list.
	press(t, m, "enter")
	if m.showCommand {
		t.Error("enter on a runnable item left the directory open")
	}
	if m.action == nil {
		t.Error("enter did not run the selected command")
	}
}

// q leaves, and what leaving COSTS is pgctl's business.
//
// tuikit reserves four keys and q is one of them: it means leave, everywhere.
// A running restore makes leaving expensive — the engine's onFailure hooks are
// what bring an environment back up — so there is a question in front of it,
// and answering the question is what cancels.
func TestLeavingWhileSomethingRunsAsksFirst(t *testing.T) {
	m := loadedModel(t)
	rec := fixtureRun(m)
	cancelled := false
	rec.cancel = func() { cancelled = true }

	press(t, m, "q")
	if !m.leaving {
		t.Fatal("q did not ask about the running operation")
	}
	if cancelled {
		t.Error("q cancelled the run before the question was answered")
	}

	press(t, m, "esc")
	if m.leaving {
		t.Error("esc left the question open")
	}
	if cancelled {
		t.Error("esc cancelled the run")
	}

	press(t, m, "q", "y")
	if !cancelled {
		t.Error("answering yes did not cancel the run")
	}
}

func TestViewRendersWithoutData(t *testing.T) {
	// The first frame is drawn before any probe or listing has come back, and
	// it must not panic on the empty state.
	m := fixtureModel(t)
	view := harness.Strip(run(m, m.width, m.height).View())
	for _, want := range []string{"Connections", "Databases", "Snapshots", "Sets", "Runs"} {
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
	m := fixtureModel(t)
	m.width, m.height = 0, 0

	// No SetSize at all, which is the actual pty case: Bubble Tea never sends a
	// WindowSizeMsg, so the runner keeps its own default and the model never
	// hears a size. Forcing the runner to 0x0 instead would test a canvas of no
	// cells, which is not a state a terminal can be in.
	view := harness.Strip(app.New(m, app.WithChrome(Chrome)).View())
	if !strings.Contains(view, "Connections") {
		t.Errorf("nothing rendered without a window size:\n%s", view)
	}
}

// A table keeps its last column when a scrollbar appears.
//
// The scrollbar takes a column and comp.List takes two more for the cursor
// marker, so a table laid out against the pane's full width is drawn three
// columns narrower — and the canvas CLIPS rather than erroring, so the only
// symptom is a header reading SNAPSH and a data column with its end shaved off.
// Nothing in the goldens caught it, because the fixture had three tables and
// never overflowed.
func TestATableKeepsItsLastColumnWhenAScrollbarAppears(t *testing.T) {
	m := loadedModel(t)
	fixtureManyTables(m)
	r := run(m, 132, 38)
	harness.Press(r, "2", "tab")

	frame := harness.Strip(r.View())
	if !strings.Contains(frame, "SNAPSHOT") {
		t.Errorf("the last column's header is cut off — the table was laid out "+
			"wider than the rows it was drawn into:\n%s", frame)
	}
	// And the bar is actually there, or the test is asserting about a state it
	// never reached.
	if _, ok := r.Canvas().Region(comp.Region(regScroll)); !ok {
		t.Error("no scrollbar was drawn, so this proves nothing")
	}
}

// The Rules tab says which rule wins.
//
// The engine's rule is "the last match applies", and a list of rules cannot show
// that — it reads as four independent statements. A rule whose every match
// belongs to a later rule decides nothing, which is what a general pattern
// followed by a broader one silently becomes.
func TestTheRulesTabNamesTheRuleThatDecidesNothing(t *testing.T) {
	m := loadedModel(t)
	// audit.* would blank the whole schema; the rule after it takes every table
	// audit.* matched, so the first decides nothing at all.
	m.cfg.Rules = []config.Rule{
		{Table: "audit.*", Data: config.DataNone, Why: "an append-only log"},
		{Table: "audit.log*", Why: "…except this one, apparently"},
	}
	m.liveTable["prd/product-development"] = []pg.TableInfo{
		{Name: "audit.logged_actions", Bytes: 1 << 30},
	}
	m.focus = panelDatabases

	rules := paneText(m.viewDatabaseTab(1, 90))
	if !strings.Contains(rules, "LAST one applies") {
		t.Errorf("the tab does not say how two matching rules resolve:\n%s", rules)
	}
	if !strings.Contains(rules, "decides nothing") {
		t.Errorf("the shadowed rule is not called out:\n%s", rules)
	}

	// The general-then-exception shape the docs recommend: the broad rule keeps
	// the tables the narrow one does not name, so both decide something and
	// neither is flagged. This must not fire on every config with two rules.
	m.cfg.Rules = []config.Rule{
		{Table: "audit.*", Data: config.DataNone, Why: "an append-only log"},
		{Table: "audit.retention_policy", Why: "config the app reads at boot"},
	}
	m.liveTable["prd/product-development"] = []pg.TableInfo{
		{Name: "audit.logged_actions", Bytes: 1 << 30},
		{Name: "audit.retention_policy", Bytes: 8 << 10},
	}
	if rules := paneText(m.viewDatabaseTab(1, 90)); strings.Contains(rules, "decides nothing") {
		t.Errorf("a rule that wins is reported as shadowed:\n%s", rules)
	}
}
