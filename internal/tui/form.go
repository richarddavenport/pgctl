package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/tuikit/app"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
)

// An action is a modal form: the questions an operation needs answered, on one
// screen, with every option the command line exposes.
//
// A wizard that asks one question per screen was the first design and the
// complaint against it was right — you cannot see what you have chosen, you
// cannot change an earlier answer without starting again, and the options the
// CLI has simply are not there. A form shows every field at once, with the
// cursor moving between them.
type actionKind int

const (
	actionSnapshot actionKind = iota
	actionApply
	actionMove
	actionPrune
	actionDelete
)

// fieldKind is how one row of a form behaves.
//
// All four are comp.Field's: text, choice, toggle, and a text field with a Must
// phrase. There was a fifth — multi, any number of a list — for the databases a
// snapshot covered, drawn by hand because comp.Field picks exactly one. It is
// gone: the panel selection is which database, so the form does not ask. That
// deleted pgctl's side of tuikit issue 49's multi-select half, and the issue is
// commented to say so.
type fieldKind int

const (
	fieldChoice  fieldKind = iota // one of a list
	fieldToggle                   // on or off
	fieldText                     // typed
	fieldConfirm                  // the connection's name, typed exactly
)

// formField is one row.
type formField struct {
	key   string
	label string
	kind  fieldKind
	help  string

	options []string
	labels  []string
	choice  int
	on      bool
	text    string

	// caret is where typing lands in a text field, as a rune index. The form
	// draws it and app.EditAt moves it; without one, a typo in a table list is
	// corrected by deleting back to it.
	caret int

	// disabled fields are shown but skipped, so a form's shape does not change
	// as options are chosen — a field that vanishes is one the operator has to
	// re-find.
	disabled bool
	reason   string
}

// actionModel is the open form.
type actionModel struct {
	kind    actionKind
	title   string
	explain string

	// conn is the connection the form was opened on, and it is NOT a field.
	//
	// The panels are a hierarchy: being on a row of Connections, with a
	// database selected under it, is already a statement of which server this
	// is about. A form that asked again would be a second way to say the same
	// thing — and the two could disagree, which they did. The Databases
	// multi-select is built from the panel's connection at open, and nothing
	// recomputed it when a Connection field changed, so choosing a different
	// server left you offering that server the previous one's database names.
	//
	// database is the same fact one level down, for the same reason: panel 2
	// says which one, so the form does not ask. See openSnapshot.
	conn     string
	database string

	fields []formField
	cursor int

	// stage separates filling the form from reading the plan it produced.
	stage actionStage
	plan  *engine.Plan

	// plannedAt is when the plan was asked for, so the wait can say how long it
	// has been waiting. A plan against a large target reads its whole foreign
	// key catalog, and the difference between "working" and "hung" is a number.
	plannedAt time.Time

	err error
}

type actionStage int

const (
	stageForm actionStage = iota
	stagePlanning
	stagePlan
)

func (a *actionModel) field(key string) *formField {
	for i := range a.fields {
		if a.fields[i].key == key {
			return &a.fields[i]
		}
	}
	return nil
}

func (a *actionModel) value(key string) string {
	f := a.field(key)
	if f == nil {
		return ""
	}
	switch f.kind {
	case fieldChoice:
		if f.choice < len(f.options) {
			return f.options[f.choice]
		}
	case fieldText, fieldConfirm:
		return f.text
	case fieldToggle:
		if f.on {
			return "true"
		}
	}
	return ""
}

func (a *actionModel) enabled(key string) bool {
	f := a.field(key)
	return f != nil && f.on
}

// newChoice builds a single-choice field. Every one of them is driven the same
// way, so the hint is the same and belongs here rather than at each call.
func newChoice(key, label string, options, labels []string) formField {
	return formField{key: key, label: label, kind: fieldChoice,
		options: options, labels: labels, help: "← → to change"}
}

// newToggle builds a toggle, off.
//
// Off, not a parameter: every toggle in every form here starts off, and the one
// that did not — "upload to storage", pre-ticked when a remote was configured —
// was the default this change deleted. A parameter that every caller passes
// false is a parameter claiming a choice nobody makes.
func newToggle(key, label, help string) formField {
	return formField{key: key, label: label, kind: fieldToggle, help: help}
}

func newText(key, label, help string) formField {
	return formField{key: key, label: label, kind: fieldText, help: help}
}

// openSnapshot builds the snapshot form: WHERE the snapshot should go, and
// nothing else.
//
// Which database is not a question, and that is the same argument
// actionModel.conn makes one level up. The panels are a hierarchy: a row of
// Connections with a database selected under it is already a complete statement
// of what this is about, and a form that asked again would be a second way to
// say the same thing — two ways that can disagree, which is how the connection
// field came to offer one server's name beside another server's databases.
//
// Where it GOES is the opposite case: no panel says it, the config declares
// several possibilities, and the answer is different from one run to the next —
// this one goes to the shared account so a colleague can restore it, the next
// stays here because it is a test. So it is asked, every time, with nothing
// pre-ticked.
//
// What it costs: the TUI takes one database at a time. `pgctl snapshot --from
// <conn>` with no --db still covers every declared database, which is what the
// nightly runs.
func (m *Model) openSnapshot() {
	conn, ok := m.selectedConn()
	if !ok {
		m.err = fmt.Errorf("no connections declared in %s", m.cfg.Source)
		return
	}
	db, ok := m.selectedDatabase()
	if !ok {
		m.err = m.noDatabase(conn)
		return
	}

	explain := "Reads " + db.Name + " and writes a compressed copy. " +
		"Nothing is written to " + conn.Name + "."
	fields := m.destinationFields()
	if len(fields) == 0 {
		explain += " No storage remotes are declared, so it stays on this machine."
	}

	m.action = &actionModel{
		kind:     actionSnapshot,
		conn:     conn.Name,
		database: db.Name,
		title:    "Take a snapshot of " + conn.Name + "/" + db.Name,
		explain:  explain,
		fields:   fields,
	}
}

// destinationFields is one toggle per declared destination, all off.
//
// A toggle each rather than a choice of combinations: with two remotes a single
// choice field has seven options ("here", "here and A", "here and B", "here and
// both", "A only"…), which is a list nobody reads, and it grows exponentially
// with the config. Toggles grow by one row per destination and say the same
// thing.
//
// None is pre-ticked because there is no default — see openSnapshot — and
// nothing at all is offered when there is only one place a snapshot can go:
// asking a question with a single answer is a keystroke charged for nothing.
func (m *Model) destinationFields() []formField {
	if len(m.cfg.Remotes()) == 0 {
		return nil
	}

	// storage.dir as WRITTEN, not resolved. The absolute path is the useful
	// thing in a message about where gigabytes went, and the wrong thing here:
	// it is a temp directory under test, so a golden of this screen changed on
	// every run.
	fields := []formField{newToggle(destinationKey(config.LocalStorage), config.LocalStorage,
		"keep it on this machine, in "+m.cfg.Storage.Dir)}
	for _, r := range m.cfg.Remotes() {
		fields = append(fields, newToggle(destinationKey(r.Name), r.Name,
			"upload it to the "+r.Container+" container"))
	}
	return fields
}

// destinationKey is a destination's field key, namespaced so a remote called
// "widen" cannot collide with a flag.
func destinationKey(name string) string { return "storage:" + name }

// chosenDestinations is the destinations ticked on the form.
//
// A form with no destination fields has exactly one place to put a snapshot,
// and says so: local. Otherwise it is what the operator ticked, and an empty
// answer is refused rather than defaulted — see openSnapshot.
func (m *Model) chosenDestinations() []string {
	if m.action == nil {
		return nil
	}
	if len(m.cfg.Remotes()) == 0 {
		return []string{config.LocalStorage}
	}
	var out []string
	for _, name := range m.cfg.Destinations() {
		if m.action.enabled(destinationKey(name)) {
			out = append(out, name)
		}
	}
	return out
}

// noDatabase is why there is nothing to act on, which is three different
// situations that look identical from the form's side.
func (m *Model) noDatabase(conn config.Connection) error {
	p := m.probes[conn.Name]
	switch {
	case m.probing[conn.Name]:
		return fmt.Errorf("still reaching %s — its databases are what it answers with", conn.Name)
	case p == nil:
		return fmt.Errorf("%s has not been reached yet, so its databases are unknown: "+
			"select it in panel 1, or press r", conn.Name)
	case !p.Reachable:
		// Wrapped, so the probe's error is still the error rather than a copy of
		// its text: it has already had passwords stripped out of it by
		// engine.redact, and re-formatting it as a string would make this the
		// one place that could reintroduce one.
		return fmt.Errorf("%s is unreachable, so there is no database to act on: %w",
			conn.Name, p.Err)
	default:
		return fmt.Errorf("%s reports no databases that %s admits", conn.Name, m.cfg.Source)
	}
}

// openApply builds the apply form for the selected snapshot: where to, how much
// of it, and the flags that decide what happens to a selection that is not
// referentially closed.
func (m *Model) openApply() {
	entry, ok := m.selectedSnapshot()
	if !ok {
		m.err = fmt.Errorf("no snapshot selected — press n to take one, " +
			"or move to the Snapshots panel")
		return
	}
	if !entry.Manifest.Complete() {
		m.err = fmt.Errorf("%s did not finish and cannot be applied", entry.Manifest.ID)
		return
	}

	targets, labels := m.applyTargets()
	if len(targets) == 0 {
		m.err = fmt.Errorf("every declared connection is protected — " +
			"there is nowhere to apply to")
		return
	}

	scopes := []string{"whole database"}
	scopeLabels := []string{"whole database — drop and recreate it"}
	for _, s := range m.cfg.Sets {
		if s.Database != entry.Manifest.Database {
			continue
		}
		scopes = append(scopes, "set:"+s.Name)
		desc := s.Description
		if desc == "" {
			desc = strings.Join(s.Include, ", ")
		}
		scopeLabels = append(scopeLabels, fmt.Sprintf("set %s — %s", s.Name, desc))
	}
	scopes = append(scopes, "tables")
	scopeLabels = append(scopeLabels, "tables — name them yourself")

	m.action = &actionModel{
		kind:    actionApply,
		title:   "Apply " + entry.Manifest.ID,
		explain: "Replaces data on the target. The plan is shown before anything is touched.",
		fields: []formField{
			newChoice("target", "Target", targets, labels),
			newChoice("scope", "Scope", scopes, scopeLabels),
			newText("tables", "Tables",
				"comma separated, schema-qualified — used when scope is `tables`"),
			newToggle("widen", "Widen to closure",
				"include tables the selection references but does not name"),
			// The guarded target's name, as a FIELD rather than a prompt after
			// the plan. comp.Field.Must is what makes that possible: the form
			// knows whether the phrase has been satisfied, so the requirement
			// is visible while the choices are still being made rather than
			// sprung at the end.
			{key: "confirm", label: "Type the target's name", kind: fieldConfirm,
				help: "a guarded target needs its name in full"},
		},
	}
	// Settle which fields apply to the starting choices, so the form explains
	// itself before anything is touched rather than after.
	m.syncAction()
}

// openMove builds the move form.
func (m *Model) openMove() {
	from, ok := m.selectedConn()
	targets, targetLabels := m.applyTargets(from.Name)
	if !ok || len(targets) == 0 {
		m.err = fmt.Errorf("move needs a source and an unprotected target that is not itself")
		return
	}

	db, hasDB := m.selectedDatabase()
	if !hasDB {
		m.err = m.noDatabase(from)
		return
	}

	m.action = &actionModel{
		kind:     actionMove,
		conn:     from.Name,
		database: db.Name,
		title:    "Move " + from.Name + "/" + db.Name + " into another connection",
		explain: "Takes a snapshot into a temporary directory, applies it, and deletes it. " +
			"Nothing is catalogued or uploaded, and nothing is written to " + from.Name + ".",
		fields: []formField{
			newChoice("to", "To", targets, targetLabels),
			newToggle("keep", "Keep the staged snapshot",
				"on catalogues it afterwards instead of deleting it"),
		},
	}
}

func (m *Model) openPrune() {
	r := m.cfg.Storage.Retention
	if r.Daily == 0 && r.Weekly == 0 && r.Monthly == 0 {
		m.err = fmt.Errorf("no storage.retention configured in %s, "+
			"so there is nothing to prune", m.cfg.Source)
		return
	}
	names := append([]string{"all"}, connectionNames(m.cfg.All())...)
	m.action = &actionModel{
		kind:  actionPrune,
		title: "Prune snapshots",
		explain: fmt.Sprintf("Retention: %d daily, %d weekly, %d monthly. "+
			"An incomplete snapshot is never kept, and the newest always is.",
			r.Daily, r.Weekly, r.Monthly),
		fields: []formField{
			newChoice("connection", "Connection", names, names),
			newToggle("apply", "Delete them",
				"off reports what would go, which is the safe way to read a new policy"),
		},
	}
}

func (m *Model) openDelete() {
	entry, ok := m.selectedSnapshot()
	if !ok {
		m.err = fmt.Errorf("no snapshot selected")
		return
	}
	m.action = &actionModel{
		kind:  actionDelete,
		title: "Delete " + entry.Manifest.ID,
		explain: fmt.Sprintf("Removes %s from %s. This cannot be undone.",
			engine.HumanBytes(entry.Manifest.Bytes), entry.Location()),
		fields: []formField{
			newToggle("confirm", "Yes, delete it", "space toggles"),
		},
	}
}

// applyTargets is every connection an apply may write to.
//
// except names a connection to leave out — the move form's source, which cannot
// also be its target. It used to be offered, and the form then refused at
// submit with "prd is both the source and the target": a choice that is always
// wrong is better not offered than caught.
func (m *Model) applyTargets(except ...string) (names, labels []string) {
	for _, conn := range m.cfg.All() {
		if conn.Protected {
			continue
		}
		if slices.Contains(except, conn.Name) {
			continue
		}
		names = append(names, conn.Name)
		label := conn.Name
		if conn.Guarded {
			label += "  (guarded — needs its name typed)"
		}
		labels = append(labels, label)
	}
	return names, labels
}

func connectionNames(conns []config.Connection) []string {
	out := make([]string, 0, len(conns))
	for _, c := range conns {
		out = append(out, c.Name)
	}
	return out
}

// actionKeys handles the keys that OPEN a form.
//
// Opening one is synchronous — the form is built from what is already loaded —
// so there is never a command to run, only a handled-or-not answer for the
// caller's routing.
func (m *Model) actionKeys(key string) bool {
	switch key {
	case "n":
		m.openSnapshot()
		return true
	case "a", "enter":
		if key == "enter" && m.focus != panelSnapshots {
			return false
		}
		m.openApply()
		return true
	case "m":
		m.openMove()
		return true
	case "p":
		m.openPrune()
		return true
	case "x":
		if m.focus != panelSnapshots {
			return false
		}
		m.openDelete()
		return true
	}
	return false
}

// syncAction enables and disables fields that depend on other fields, so the
// form always reflects what the current choices actually allow.
func (m *Model) syncAction() {
	a := m.action
	if a == nil || a.kind != actionApply {
		return
	}
	scope := a.value("scope")
	if f := a.field("tables"); f != nil {
		f.disabled = scope != "tables"
		f.reason = "used only when the scope is `tables`"
	}
	if f := a.field("widen"); f != nil {
		f.disabled = scope == "whole database"
		f.reason = "a whole-database apply replaces everything, so there is nothing to widen"
	}
	// The confirmation field belongs to the TARGET, so it appears and
	// disappears with the choice rather than with the stage. A disabled field
	// still shows, which is the point: an operator scanning the form sees that
	// this target is one of the ones that does not need it.
	if f := a.field("confirm"); f != nil {
		target := a.value("target")
		guarded := false
		for _, conn := range m.cfg.All() {
			if conn.Name == target {
				guarded = conn.Guarded
			}
		}
		f.disabled = !guarded
		f.reason = target + " is not guarded, so it needs no phrase"
		if guarded {
			f.reason = ""
		}
	}
}

func (a *actionModel) nextEnabled(from int) int {
	for i := from + 1; i < len(a.fields); i++ {
		if !a.fields[i].disabled {
			return i
		}
	}
	return from
}

func (a *actionModel) prevEnabled(from int) int {
	for i := from - 1; i >= 0; i-- {
		if !a.fields[i].disabled {
			return i
		}
	}
	return from
}

// actionKey routes a keypress while a form is open.
func (m *Model) actionKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	a := m.action
	key := msg.String()

	switch a.stage {
	case stagePlan:
		return m.planKey(key)
	case stagePlanning:
		// A plan is a read against the target and it can be slow. Leaving is
		// the only thing to offer while it runs — the result lands in a
		// generation nobody is waiting for and is dropped.
		if key == "esc" || key == "ctrl+c" {
			m.action = nil
		}
		return nil, true
	}

	switch key {
	case "esc", "ctrl+c":
		m.action = nil
		return nil, true
	case "up", "shift+tab":
		a.cursor = a.prevEnabled(a.cursor)
		return nil, true
	case "down", "tab":
		a.cursor = a.nextEnabled(a.cursor)
		return nil, true
	case "enter":
		return m.submitAction(), true
	}

	if a.cursor >= len(a.fields) {
		return nil, true
	}
	f := &a.fields[a.cursor]
	if f.disabled {
		return nil, true
	}

	switch f.kind {
	case fieldChoice:
		switch key {
		case "left", "h":
			f.choice = (f.choice + len(f.options) - 1) % len(f.options)
		case "right", "l", " ":
			f.choice = (f.choice + 1) % len(f.options)
		}
		m.syncAction()
	case fieldToggle:
		if key == " " || key == "left" || key == "right" {
			f.on = !f.on
		}
	case fieldText, fieldConfirm:
		// The same line editor the filter uses, so backspace, the arrows, home,
		// end and ctrl+u work in a form field because they work everywhere.
		if text, caret, ok := app.EditAt(f.text, f.caret, msg); ok {
			f.text, f.caret = text, caret
		}
	}
	return nil, true
}

// planKey handles the plan preview: read it, then confirm or go back.
func (m *Model) planKey(key string) (tea.Cmd, bool) {
	a := m.action

	switch key {
	case "esc":
		a.stage = stageForm
		a.err = nil
		m.planView.Goto(0)
		return nil, true
	case "up", "k":
		m.planView.Scroll(-1)
		return nil, true
	case "down", "j":
		m.planView.Scroll(1)
		return nil, true
	case "w":
		// Widening from the plan screen, where the refusal that motivates it is
		// on display, rather than making the operator go back and guess.
		if f := a.field("widen"); f != nil && !f.disabled {
			f.on = true
			return m.planApply(), true
		}
		return nil, true
	case "enter", "y":
		if a.plan == nil {
			return nil, true
		}
		// The phrase is the form's, and the form knows whether it is satisfied.
		// Checked here rather than at the keystroke that typed it, because this
		// is the moment it gates.
		if f := a.field("confirm"); f != nil && !f.disabled &&
			f.text != a.plan.Target.Conn.Name {
			a.err = fmt.Errorf("%s is guarded: go back and type its name in full",
				a.plan.Target.Conn.Name)
			a.stage = stageForm
			a.cursor = len(a.fields) - 1
			return nil, true
		}
		return m.executePlan(a.plan), true
	}
	return nil, true
}
