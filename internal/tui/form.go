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
// Four of the five are comp.Field's: text, choice, toggle, and a text field with
// a Must phrase. The fifth — multi, ANY NUMBER of a list — is drawn by hand,
// because comp.Field picks exactly one. That is tuikit issue 49, and this is the
// second screen in this tool to need it.
//
// It is worth recording that it was deleted and came back. The databases
// multi-select went because the panels already said which database, and the
// conclusion drawn at the time was that pgctl no longer needed a multi-select at
// all. Destinations arrived the same day: nobody's panel says where a snapshot
// should go, "both" is a real answer, and rendering that as two independent
// yes/no toggles reads as two settings rather than one choice — which is the
// complaint that produced this comment.
type fieldKind int

const (
	fieldChoice  fieldKind = iota // one of a list
	fieldMulti                    // any number of a list
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

	// selected is which options of a fieldMulti are in. A map rather than a
	// slice of bools so that a field built from a config's order does not have
	// to be rebuilt when the order changes.
	selected map[int]bool

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

// openSnapshot builds the snapshot form: WHICH databases, and WHERE they go.
//
// Both are asked, and the panel selection is the DEFAULT rather than the answer.
// That is a reversal, recorded as decision 24, and the reasoning it reverses was
// good: the panels are a hierarchy, a form that asks what a panel already said
// is a second way to say one thing, and two ways can disagree.
//
// What broke it in use is that "which database" was NOT unambiguously said. Two
// lists of databases were on screen — panel 2, and the Connections pane's own
// Databases tab — each with its own cursor, and only one of them meant anything:
// `n` acted on panel 2's `claims` while the pane's cursor sat on
// `product-development` and the modal said `prd/claims`. The duplicate tab is
// gone, and the form asks anyway, because a snapshot covering SEVERAL databases
// is a thing people want and no single cursor can express it.
//
// The connection is still not asked. That half of the principle holds: panel 1
// is the only statement of which server this is, there is nothing to combine,
// and the failure it prevents is real — the connection field once offered one
// server's name beside another server's database names.
func (m *Model) openSnapshot() {
	conn, ok := m.selectedConn()
	if !ok {
		m.err = fmt.Errorf("no connections declared in %s", m.cfg.Source)
		return
	}
	databases := m.databaseNames()
	if len(databases) == 0 {
		m.err = m.noDatabase(conn)
		return
	}

	fields := []formField{m.databaseField(databases)}
	explain := "Reads the chosen databases and writes a compressed copy. " +
		"Nothing is written to " + conn.Name + "."
	if dests := m.destinationFields(); len(dests) > 0 {
		fields = append(fields, dests...)
		explain += " Choose where they go: keeping them here, sending them away, or both."
	} else {
		explain += " No storage remotes are declared, so they stay on this machine."
	}

	m.action = &actionModel{
		kind:    actionSnapshot,
		conn:    conn.Name,
		title:   "Take a snapshot of " + conn.Name,
		explain: explain,
		fields:  fields,
	}
}

// databaseField is the multi-select of what to cover.
//
// Defaulted to the database under panel 2's cursor, which is the whole of what
// the old "the panel decides" design got right: looking at one database is a
// statement of intent, and six is rarely what somebody means when they were
// looking at one. `a` takes all of them, which is what the nightly does and what
// `pgctl snapshot --from prd` with no --db has always done.
func (m *Model) databaseField(databases []string) formField {
	f := formField{
		key:      "databases",
		label:    "Databases",
		kind:     fieldMulti,
		options:  databases,
		selected: map[int]bool{},
	}
	if db, ok := m.selectedDatabase(); ok {
		for i, name := range databases {
			if name == db.Name {
				f.selected[i], f.choice = true, i
			}
		}
	}
	return f
}

// chosenDatabases is the databases chosen on the form.
func (m *Model) chosenDatabases() []string {
	if m.action == nil {
		return nil
	}
	f := m.action.field("databases")
	if f == nil {
		return nil
	}
	var out []string
	for i, name := range f.options {
		if f.selected[i] {
			out = append(out, name)
		}
	}
	return out
}

// destinationFields is the one field a snapshot form has: where it goes.
//
// ONE field holding every destination, not a toggle per destination. The
// toggles were the first attempt and they were wrong for a reason worth
// keeping: "local no / snapshots yes" is two settings a reader has to combine
// themselves, and this is one question with two answers available. A reader
// looking at it said so — *"this ticking didn't make sense to me"* — and they
// were right that the shape, not the wording, was the problem.
//
// So the rows carry ● and ○, which is the same in/out pair the Connections
// panel marks reachability with, and the cursor moves down them. What that
// costs is a hand-drawn field: comp.Field picks exactly one of a list, which is
// tuikit issue 49.
//
// Nothing is chosen because there is no default — see openSnapshot — and
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
	options := []string{config.LocalStorage}
	hints := []string{"keep it on this machine, in " + m.cfg.Storage.Dir}
	for _, r := range m.cfg.Remotes() {
		options = append(options, r.Name)
		hints = append(hints, "upload it to the "+r.Container+" container")
	}

	return []formField{{
		key:      "destinations",
		label:    "Where it goes",
		kind:     fieldMulti,
		options:  options,
		labels:   hints,
		selected: map[int]bool{},
		// No help text: for a multi-select the help row says what the current
		// selection MEANS, which the keys and the rows cannot. See
		// destinationConsequence.

	}}
}

// chosenDestinations is the destinations chosen on the form.
//
// A form with no destination field has exactly one place to put a snapshot, and
// says so: local. Otherwise it is what the operator chose, and an empty answer
// is refused rather than defaulted — see openSnapshot.
func (m *Model) chosenDestinations() []string {
	if m.action == nil {
		return nil
	}
	f := m.action.field("destinations")
	if f == nil {
		return []string{config.LocalStorage}
	}
	var out []string
	for i, name := range f.options {
		if f.selected[i] {
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

	// One database, from panel 2, and NOT a multi-select like the snapshot
	// form's. A move drops and reloads a database on the target: doing six at
	// once is an hour of somebody's environment being unusable, and the panel
	// cursor is a fine way to say which one. Ask if this turns out to be wrong
	// in use, the way the snapshot form's did.
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
	case "shift+tab":
		a.cursor = a.prevEnabled(a.cursor)
		return nil, true
	case "tab":
		a.cursor = a.nextEnabled(a.cursor)
		return nil, true
	case "enter":
		return m.submitAction(), true
	case "up", "down":
		// The arrows move between FIELDS, except inside a multi-select, where
		// they move between its options — see the fieldMulti case below. A list
		// whose rows do not answer to ↑↓ is a list you have to be told how to
		// operate.
		if a.cursor < len(a.fields) && a.fields[a.cursor].kind == fieldMulti {
			break
		}
		if key == "up" {
			a.cursor = a.prevEnabled(a.cursor)
		} else {
			a.cursor = a.nextEnabled(a.cursor)
		}
		return nil, true
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
	case fieldMulti:
		// The arrows move within the list, because it IS a list and a vertical
		// list answers to vertical keys. tab still moves between fields, which
		// is what keeps this unambiguous on a form that has others.
		switch key {
		case " ":
			f.choice = clamp(f.choice, len(f.options)-1)
			f.selected[f.choice] = !f.selected[f.choice]
		case "up", "k":
			f.choice = clamp(f.choice-1, len(f.options)-1)
		case "down", "j":
			f.choice = clamp(f.choice+1, len(f.options)-1)
		case "a":
			for i := range f.options {
				f.selected[i] = true
			}
		}
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
