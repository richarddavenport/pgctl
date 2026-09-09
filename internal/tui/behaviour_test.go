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
// focusField puts the form's cursor on a field by key.
//
// By key, never by index: a form gains and loses fields — the apply form grew a
// databases multi-select between target and scope — and a test that counted
// would then be testing a different field while still passing.
func focusField(t *testing.T, m *Model, key string) {
	t.Helper()
	for i, f := range m.action.fields {
		if f.key == key {
			m.action.cursor = i
			return
		}
	}
	t.Fatalf("the form has no %q field", key)
}

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

	// The database selection does not touch the Snapshots panel at all now: a
	// snapshot is a RUN, covering every database taken at one instant, so
	// panel 3 is about the connection and nothing below it.
	press(t, m, "up", "2", "down")
	if db, _ := m.selectedDatabase(); db.Name != "claims" {
		t.Fatalf("database cursor is on %q, want claims", db.Name)
	}
	if got := m.snapshotCount(panelSnapshots); got != 1 {
		t.Errorf("panel 3 shows %d snapshots of prd, want its one run", got)
	}

	// Sets ARE per database, and stay that way: a set names tables in one
	// database and means nothing against another.
	if got := len(m.sets()); got != 0 {
		t.Errorf("the claims database shows %d sets declared for product-development", got)
	}
}

// Two panels, two questions: what was taken FROM here, and what can be put ON
// here.
//
// One list cannot answer both, which is what made the apply path hard to find:
// standing on qat, the Snapshots panel is empty and correct — nothing is ever
// taken from qat — and a reader looking for something to restore there found
// nothing at all.
func TestSnapshotsAreWhatWasTakenAndRestorableIsWhatCanBePut(t *testing.T) {
	m := loadedModel(t)
	// A second connection's snapshot, so "somebody else's" has a member.
	qatRun := *m.entries[0].Manifest
	qatRun.ID = "qat/product-development/20260828T060000Z"
	qatRun.Connection = "qat"
	qatRun.StartedAt = m.entries[0].Manifest.StartedAt.Add(-time.Hour)
	m.setEntries(append(m.entries, &engine.Entry{
		Manifest: &qatRun, At: []string{config.LocalStorage},
	}))

	// On prd: its own run in Snapshots, qat's in Restorable.
	if got := m.snapshotCount(panelSnapshots); got != 1 {
		t.Errorf("prd's Snapshots panel shows %d, want its own run", got)
	}
	if got := m.snapshotCount(panelRestorable); got != 1 {
		t.Errorf("prd's Restorable panel shows %d, want qat's run", got)
	}
	// Grouped by where they came from, since that is the fact that distinguishes
	// them and the row cannot hold a connection name as well as a date.
	rows := m.restorable()
	if len(rows) == 0 || rows[0].heading != "qat" {
		t.Fatalf("the Restorable panel does not lead with a source heading: %+v", rows)
	}

	// On qat: nothing was taken from it, and prd's run is what can go on it.
	press(t, m, "j")
	if conn, _ := m.selectedConn(); conn.Name != "qat" {
		t.Fatalf("cursor is on %q, want qat", conn.Name)
	}
	if got := m.snapshotCount(panelSnapshots); got != 1 {
		t.Errorf("qat's Snapshots panel shows %d, want the one taken from qat", got)
	}
	if got := m.snapshotCount(panelRestorable); got != 1 {
		t.Errorf("qat's Restorable panel shows %d, want prd's run", got)
	}

	// A snapshot is the RUN, so selecting one selects every database of it.
	press(t, m, "4")
	run, ok := m.selectedSnapshot()
	if !ok {
		t.Fatal("nothing selected in a Restorable panel with a row in it")
	}
	if run.Connection != "prd" {
		t.Errorf("selected a run from %q, want prd's", run.Connection)
	}
	if len(run.Databases()) != 1 {
		t.Errorf("the run covers %v; the fixture takes one database", run.Databases())
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
	if len(targets) != 3 {
		t.Errorf("targets = %v, want every connection that is not protected", targets)
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
	// The cursor is put on the field BY KEY: the apply form gained a databases
	// field between target and scope, and an index would have moved with it.
	focusField(t, m, "scope")
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

// The snapshot form asks WHICH databases, defaulted to the panel's.
//
// A reversal, recorded as decision 24. The form used to take panel 2's cursor
// as the answer, and in use that was unanswerable: two lists of databases were
// on screen with two cursors — panel 2, and the Connections pane's own
// Databases tab — and `n` acted on one while the reader was looking at the
// other. The duplicate tab is gone AND the form asks, because a snapshot
// covering several databases is a thing people want and no single cursor says
// it.
//
// The connection is still not asked: panel 1 is the only statement of which
// server this is, and the field that used to ask offered one server's name
// beside another server's database names.
func TestTheSnapshotFormDefaultsToThePanelsDatabase(t *testing.T) {
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
	if f := m.action.field("connection"); f != nil {
		t.Error("the snapshot form asks for a connection; panel 1 is that")
	}
	if m.action.conn != "prd" {
		t.Errorf("the form recorded connection %q", m.action.conn)
	}

	f := m.action.field("databases")
	if f == nil {
		t.Fatal("the snapshot form does not ask which databases")
	}
	if len(f.options) != len(m.databaseNames()) {
		t.Errorf("the form offers %d databases, want all %d the server has",
			len(f.options), len(m.databaseNames()))
	}
	// Defaulted to the panel's, and to that one alone: looking at one database
	// is a statement of intent, and six is rarely what somebody means.
	if got := m.chosenDatabases(); len(got) != 1 || got[0] != "claims" {
		t.Errorf("databases = %v, want just the panel's claims", got)
	}
	// And the cursor starts on it, so space unchooses what you were looking at
	// rather than something else.
	if f.options[f.choice] != "claims" {
		t.Errorf("the cursor starts on %q, want the panel's claims", f.options[f.choice])
	}

	// Moving the panel cursor moves the default.
	press(t, m, "esc", "up", "n")
	if got := m.chosenDatabases(); len(got) != 1 || got[0] != "product-development" {
		t.Errorf("databases = %v after moving the cursor", got)
	}

	// `a` takes all of them, which is what the nightly does.
	press(t, m, "a")
	if got := m.chosenDatabases(); len(got) != len(m.databaseNames()) {
		t.Errorf("`a` chose %v, want every database", got)
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

	// ONE field for the destinations — a choice with every one in it, local
	// first, in config order. Not a toggle each: "local no / snapshots yes" is
	// two settings a reader has to combine themselves, and this is one question.
	f := m.action.field("destinations")
	if f == nil || f.kind != fieldMulti {
		t.Fatal("the form does not offer the destinations as one choice")
	}
	if strings.Join(f.options, ",") != "local,snapshots" {
		t.Errorf("options = %v, want every destination, local first", f.options)
	}
	if got := m.chosenDestinations(); len(got) != 0 {
		t.Errorf("%v is pre-chosen; nothing should be", got)
	}

	// Enter with nothing chosen is a refusal that says how to answer, and the
	// form stays open on it. The cursor goes to the destinations field first,
	// since the databases field is answered by default.
	press(t, m, "tab")
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
	if f := m.action.field("destinations"); f != nil {
		t.Error("a destination was asked for when there is only one place to go")
	}
	if !strings.Contains(m.action.explain, "stay on this machine") {
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
	if got := len(m.connections()); got != len(m.cfg.All()) {
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

// The keymap is the lazygit family's, because a family is worth more than any
// one tool's preference.
//
// h and l between the panels, j and k within one, [ and ] through the detail
// pane's tabs, tab across the divide. It replaces J/K for panels and tab-then-
// cycle for tabs, which were this tool's own inventions — and tuikit decision
// 42's argument for reserving four keys is the same argument one level up: all
// of the value is in being the same everywhere.
func TestTheKeymapMovesLikeItsFamily(t *testing.T) {
	m := loadedModel(t)

	// l and h step through the panels.
	press(t, m, "l")
	if m.focus != panelDatabases {
		t.Errorf("l left focus on panel %d, want the next one", m.focus)
	}
	press(t, m, "h")
	if m.focus != panelConnections {
		t.Errorf("h left focus on panel %d, want the previous one", m.focus)
	}
	// And they clamp rather than wrapping: a panel column is a hierarchy read
	// downwards, so falling off the end and reappearing at the top would move a
	// reader somewhere they did not ask to be.
	press(t, m, "h", "h")
	if m.focus != panelConnections {
		t.Errorf("h wrapped past the first panel to %d", m.focus)
	}

	// j and k move within the focused panel.
	press(t, m, "j")
	if m.cursor(panelConnections) != 1 {
		t.Errorf("j left the cursor on row %d", m.cursor(panelConnections))
	}
	press(t, m, "k")
	if m.cursor(panelConnections) != 0 {
		t.Errorf("k left the cursor on row %d", m.cursor(panelConnections))
	}

	// [ and ] cycle the detail pane's tabs WITHOUT focusing it: which tab the
	// pane shows is a question about what you are reading, and flipping it
	// while the cursor stays on the row you are choosing is the ordinary way to
	// use it.
	tabs := len(m.paneTabs())
	press(t, m, "]")
	if m.paneFocus {
		t.Error("] moved focus into the pane; it should only change the tab")
	}
	if m.tabs[panelConnections] != 1 {
		t.Errorf("] selected tab %d, want the next one", m.tabs[panelConnections])
	}
	press(t, m, "[")
	if m.tabs[panelConnections] != 0 {
		t.Errorf("[ selected tab %d, want the previous one", m.tabs[panelConnections])
	}
	// The strip cycles, which is what the chevrons around it say.
	press(t, m, "[")
	if m.tabs[panelConnections] != tabs-1 {
		t.Errorf("[ from the first tab selected %d, want the last of %d",
			m.tabs[panelConnections], tabs)
	}

	// tab crosses the divide, both ways.
	press(t, m, "tab")
	if !m.paneFocus {
		t.Error("tab did not focus the detail pane")
	}
	press(t, m, "tab")
	if m.paneFocus {
		t.Error("tab did not come back out of the detail pane")
	}

	// Moving to another panel means working in it, so it takes focus out of the
	// pane — the same thing 1-5 have always done.
	press(t, m, "tab", "l")
	if m.paneFocus {
		t.Error("l left focus in the detail pane")
	}
	if m.focus != panelDatabases {
		t.Errorf("l from inside the pane went to panel %d", m.focus)
	}
}

// A filter that is applied SAYS SO, wherever focus has gone since.
//
// Reported as "the filter might have gotten stuck". It had not: the panel was
// still filtered, correctly, and had stopped saying so — the title asked
// panelFocused, which is false the moment `tab` crosses into the detail pane,
// while the filtering itself keys off which panel is current. Rows missing with
// nothing on screen explaining why is what stuck looks like, and under the
// keymap `tab` is pressed constantly.
func TestAnAppliedFilterKeepsSayingSo(t *testing.T) {
	m := loadedModel(t)
	press(t, m, "/", "q", "a", "t", "enter")
	if got := len(m.connections()); got != 1 {
		t.Fatalf("the filter matched %d connections", got)
	}

	// In the panel: the title carries it.
	if title := m.panelTitle(panelConnections); !strings.Contains(title, "/qat") {
		t.Errorf("title = %q, does not say a filter is on", title)
	}

	// And after crossing into the detail pane, where it still applies.
	press(t, m, "tab")
	if got := len(m.connections()); got != 1 {
		t.Errorf("the filter stopped applying when focus moved: %d rows", got)
	}
	if title := m.panelTitle(panelConnections); !strings.Contains(title, "/qat") {
		t.Errorf("title = %q while the pane has focus; the filter is still on", title)
	}

	// The footer offers the way out, and offers it first so a narrow terminal
	// keeps it — fitHints drops from the right.
	frame := harness.Strip(run(m, 80, 24).View())
	if !strings.Contains(frame, "esc clear /qat") {
		t.Errorf("the footer does not say how to clear the filter:\n%s", frame)
	}
}

// A filter that matches nothing blames the filter, not the data.
//
// "none", "unreachable" and "none on prd — press n" are all answers about the
// world, and the reader had created this state two keystrokes ago.
func TestAFilterThatMatchesNothingSaysThat(t *testing.T) {
	m := loadedModel(t)
	press(t, m, "2", "/", "z", "z")

	if got := m.emptyPanel(panelDatabases); !strings.Contains(got, "nothing matches") {
		t.Errorf("the empty databases panel says %q", got)
	}
	// The panels that are NOT filtered keep their own empty states: the filter
	// applies to one panel, and so does its explanation.
	press(t, m, "esc", "4", "/", "z", "z")
	if got := m.emptyPanel(panelDatabases); strings.Contains(got, "nothing matches") {
		t.Errorf("an unfiltered panel blamed the filter: %q", got)
	}
}
