package tui

import (
	"fmt"
	"strings"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
)

// An action is a modal form: the questions an operation needs answered, on one
// screen, with every option the command line exposes.
//
// A wizard that asks one question per screen was the previous design and the
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
type fieldKind int

const (
	fieldChoice  fieldKind = iota // one of a list
	fieldMulti                    // any number of a list
	fieldToggle                   // on or off
	fieldText                     // typed
	fieldConfirm                  // the environment's name, typed exactly
)

// formField is one row.
type formField struct {
	key   string
	label string
	kind  fieldKind
	help  string

	options  []string
	labels   []string
	selected map[int]bool
	choice   int
	on       bool
	text     string

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

	fields []formField
	cursor int

	// stage separates filling the form from reading the plan it produced.
	stage actionStage
	plan  *planPreview

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

// chosenDatabases is the databases ticked in a multi-select. Only one field is
// ever a multi-select, so this names it rather than pretending to be general.
func (a *actionModel) chosenDatabases() []string {
	f := a.field("databases")
	if f == nil {
		return nil
	}
	var out []string
	for i, opt := range f.options {
		if f.selected[i] {
			out = append(out, opt)
		}
	}
	return out
}

// newChoice builds a single-choice field.
// newChoice builds a single-choice field. Every one of them is driven the same
// way, so the hint is the same and belongs here rather than at each call.
func newChoice(key, label string, options, labels []string) formField {
	return formField{key: key, label: label, kind: fieldChoice,
		options: options, labels: labels, help: "← → to change"}
}

// newMulti builds a multi-select, everything on by default: the common case for
// "which databases" is all of them, and unchecking is less work than checking.
func newMulti(key, label string, options []string, help string) formField {
	selected := map[int]bool{}
	for i := range options {
		selected[i] = true
	}
	return formField{key: key, label: label, kind: fieldMulti,
		options: options, selected: selected, help: help}
}

func newToggle(key, label string, on bool, help string) formField {
	return formField{key: key, label: label, kind: fieldToggle, on: on, help: help}
}

func newText(key, label, help string) formField {
	return formField{key: key, label: label, kind: fieldText, help: help}
}

// openSnapshot builds the snapshot form: which environment, which databases,
// and whether to upload.
func (m *Model) openSnapshot() {
	envs := make([]string, 0, len(m.cfg.Environments))
	for _, e := range m.cfg.Environments {
		envs = append(envs, e.Name)
	}
	if len(envs) == 0 {
		m.err = fmt.Errorf("no environments declared in %s", m.cfg.Source)
		return
	}
	current := 0
	if env, ok := m.selectedEnv(); ok {
		for i, name := range envs {
			if name == env.Name {
				current = i
			}
		}
	}

	databases := m.declaredDatabaseNames()
	dbField := newMulti("databases", "Databases", databases,
		"space toggles · a all · n none")
	// Default to the database in focus rather than all of them: the panel
	// selection is a statement of intent, and six databases is rarely what
	// someone means when they were looking at one.
	if db, ok := m.selectedDatabase(); ok && db.Declared {
		for i := range dbField.selected {
			dbField.selected[i] = false
		}
		for i, name := range databases {
			if name == db.Name {
				dbField.selected[i] = true
			}
		}
	}

	envField := newChoice("env", "Environment", envs, m.environmentLabels(envs))
	envField.choice = current

	push := newToggle("push", "Upload to storage", m.cfg.Storage.Kind != config.StorageLocal,
		"off keeps the snapshot on this machine only")
	if m.cfg.Storage.Kind == config.StorageLocal {
		push.disabled = true
		push.reason = "storage.kind is local — there is nowhere to upload to"
	}

	m.action = &actionModel{
		kind:    actionSnapshot,
		title:   "Take a snapshot",
		explain: "Reads the chosen databases and writes a compressed copy. Nothing is written to the source.",
		fields:  []formField{envField, dbField, push},
	}
}

// openApply builds the apply form for the selected snapshot: where to, how much
// of it, and the flags that decide what happens to a selection that is not
// referentially closed.
func (m *Model) openApply() {
	entry, ok := m.selectedSnapshot()
	if !ok {
		m.err = fmt.Errorf("no snapshot selected — press n to take one, or move to the Snapshots panel")
		return
	}
	if !entry.Manifest.Complete() {
		m.err = fmt.Errorf("%s did not finish and cannot be applied", entry.Manifest.ID)
		return
	}

	targets, labels := m.applyTargets()
	if len(targets) == 0 {
		m.err = fmt.Errorf("every declared environment is protected — there is nowhere to apply to")
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
			newText("tables", "Tables", "comma separated, schema-qualified — used when scope is `tables`"),
			newToggle("widen", "Widen to closure", false,
				"include tables the selection references but does not name"),
		},
	}
	// Settle which fields apply to the starting scope, so the form explains
	// itself before anything is touched rather than after.
	m.syncAction()
}

// openMove builds the move form.
func (m *Model) openMove() {
	envs := make([]string, 0, len(m.cfg.Environments))
	for _, e := range m.cfg.Environments {
		envs = append(envs, e.Name)
	}
	targets, targetLabels := m.applyTargets()
	if len(envs) == 0 || len(targets) == 0 {
		m.err = fmt.Errorf("move needs a source and an unprotected target")
		return
	}

	source := newChoice("from", "From", envs, m.environmentLabels(envs))
	if env, ok := m.selectedEnv(); ok {
		for i, name := range envs {
			if name == env.Name {
				source.choice = i
			}
		}
	}

	databases := m.declaredDatabaseNames()
	dbField := newMulti("databases", "Databases", databases, "space toggles · a all · n none")
	if db, ok := m.selectedDatabase(); ok && db.Declared {
		for i := range dbField.selected {
			dbField.selected[i] = false
		}
		for i, name := range databases {
			if name == db.Name {
				dbField.selected[i] = true
			}
		}
	}

	m.action = &actionModel{
		kind:  actionMove,
		title: "Move between environments",
		explain: "Takes a snapshot into a temporary directory, applies it, and deletes it. " +
			"Nothing is catalogued or uploaded.",
		fields: []formField{
			source,
			newChoice("to", "To", targets, targetLabels),
			dbField,
			newToggle("keep", "Keep the staged snapshot", false,
				"on catalogues it afterwards instead of deleting it"),
		},
	}
}

func (m *Model) openPrune() {
	r := m.cfg.Storage.Retention
	if r.Daily == 0 && r.Weekly == 0 && r.Monthly == 0 {
		m.err = fmt.Errorf("no storage.retention configured in %s, so there is nothing to prune", m.cfg.Source)
		return
	}
	envs := append([]string{"all"}, environmentNames(m.cfg.Environments)...)
	m.action = &actionModel{
		kind:  actionPrune,
		title: "Prune snapshots",
		explain: fmt.Sprintf("Retention: %d daily, %d weekly, %d monthly. "+
			"An incomplete snapshot is never kept, and the newest always is.",
			r.Daily, r.Weekly, r.Monthly),
		fields: []formField{
			newChoice("env", "Environment", envs, envs),
			newToggle("apply", "Delete them", false,
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
			newToggle("confirm", "Yes, delete it", false, "space toggles"),
		},
	}
}

// applyTargets is every environment that may be written to, with protected ones
// left out rather than shown and refused.
func (m *Model) applyTargets() (names, labels []string) {
	for _, env := range m.cfg.Environments {
		if env.Protected {
			continue
		}
		names = append(names, env.Name)
		label := env.Name
		if env.Guarded {
			label += "  (guarded — needs its name typed)"
		}
		labels = append(labels, label)
	}
	return names, labels
}

func (m *Model) environmentLabels(names []string) []string {
	labels := make([]string, 0, len(names))
	for _, name := range names {
		label := name
		if p := m.probes[name]; p != nil && p.Reachable {
			label += fmt.Sprintf("  PostgreSQL %s, %d databases",
				formatServerVersion(p.ServerVersion), len(p.Databases))
		} else if p != nil {
			label += "  unreachable"
		}
		labels = append(labels, label)
	}
	return labels
}

func environmentNames(envs []config.Environment) []string {
	out := make([]string, 0, len(envs))
	for _, e := range envs {
		out = append(out, e.Name)
	}
	return out
}

// actionKeys handles the keys that open a form. Opening one is synchronous —
// the form is built from what is already loaded — so there is never a command
// to run, only a handled/not-handled answer for the caller's routing.
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
