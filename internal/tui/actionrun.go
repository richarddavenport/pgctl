package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/pgctl/internal/engine"
)

// planPreview is a computed plan waiting to be confirmed.
type planPreview struct {
	plan *engine.Plan
	// needsName is a guarded target, which must have its name typed in full
	// before the apply runs. Held here rather than in the form because it is a
	// property of the plan's target, decided after the form was filled in.
	needsName bool
	typed     string
}

type planReadyMsg struct {
	plan *engine.Plan
	err  error
}

// actionKey routes a keypress while a form is open.
func (m *Model) actionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := m.action
	key := msg.String()

	if a.stage == stagePlan {
		return m.planKey(key, msg)
	}
	if a.stage == stagePlanning {
		if key == "esc" || key == "ctrl+c" {
			m.action = nil
		}
		return m, nil
	}

	switch key {
	case "esc", "ctrl+c":
		m.action = nil
		return m, nil
	case "up", "shift+tab":
		a.cursor = a.prevEnabled(a.cursor)
		return m, nil
	case "down", "tab":
		a.cursor = a.nextEnabled(a.cursor)
		return m, nil
	case "enter":
		return m, m.submitAction()
	}

	if a.cursor >= len(a.fields) {
		return m, nil
	}
	f := &a.fields[a.cursor]
	if f.disabled {
		return m, nil
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
		switch key {
		case " ":
			// The multi-select's cursor is its own, so that moving between
			// options does not move between fields.
			f.choice = clamp(f.choice, len(f.options)-1)
			f.selected[f.choice] = !f.selected[f.choice]
		case "left", "h", "up":
			f.choice = clamp(f.choice-1, len(f.options)-1)
		case "right", "l", "down":
			f.choice = clamp(f.choice+1, len(f.options)-1)
		case "a":
			for i := range f.options {
				f.selected[i] = true
			}
		case "n":
			for i := range f.options {
				f.selected[i] = false
			}
		}
	case fieldToggle:
		if key == " " || key == "left" || key == "right" {
			f.on = !f.on
		}
	case fieldText, fieldConfirm:
		switch key {
		case "backspace":
			if f.text != "" {
				f.text = f.text[:len(f.text)-1]
			}
		default:
			if len(key) == 1 {
				f.text += key
			}
		}
	}
	return m, nil
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

// submitAction turns a filled form into work.
//
// An apply plans first and shows the plan; everything else starts immediately,
// because there is nothing about them an operator could not already see on the
// form.
func (m *Model) submitAction() tea.Cmd {
	a := m.action
	switch a.kind {
	case actionSnapshot:
		databases := a.chosenDatabases()
		if len(databases) == 0 {
			a.err = fmt.Errorf("choose at least one database")
			return nil
		}
		connection := a.value("connection")
		noPush := !a.enabled("push")
		m.action = nil
		e := m.engine
		explain := fmt.Sprintf("Copying %s from %s into %s. Nothing is written to %s.",
			strings.Join(databases, ", "), connection, e.StorageDir(), connection)
		return m.start("snapshot "+connection, explain, func(ctx context.Context, report engine.Reporter) (string, error) {
			at := nowFunc()
			for _, db := range databases {
				if _, err := e.Dump(ctx, engine.DumpRequest{
					Connection: connection, Database: db, At: at, NoPush: noPush,
				}, report); err != nil {
					return "", err
				}
			}
			return fmt.Sprintf("snapshotted %s from %s", strings.Join(databases, ", "), connection), nil
		})

	case actionApply:
		return m.planApply()

	case actionMove:
		databases := a.chosenDatabases()
		if len(databases) == 0 {
			a.err = fmt.Errorf("choose at least one database")
			return nil
		}
		from, to := a.value("from"), a.value("to")
		if from == to {
			a.err = fmt.Errorf("%s is both the source and the target", from)
			return nil
		}
		keep := a.enabled("keep")
		m.action = nil
		e := m.engine
		explain := fmt.Sprintf("Refreshing %s on %s from %s, staging a snapshot that is deleted afterwards.",
			strings.Join(databases, ", "), to, from)
		return m.start(fmt.Sprintf("move %s→%s", from, to), explain,
			func(ctx context.Context, report engine.Reporter) (string, error) {
				for _, db := range databases {
					if err := e.Move(ctx, engine.MoveRequest{
						From: from, To: to, Database: db, Keep: keep,
					}, report); err != nil {
						return "", err
					}
				}
				return fmt.Sprintf("moved %s from %s to %s", strings.Join(databases, ", "), from, to), nil
			})

	case actionPrune:
		connection := a.value("connection")
		if connection == "all" {
			connection = ""
		}
		doIt := a.enabled("apply")
		m.action = nil
		e := m.engine
		explain := "Applying the retention policy."
		if !doIt {
			explain = "Reporting what the retention policy would remove. Nothing is deleted."
		}
		return m.start("prune", explain, func(ctx context.Context, report engine.Reporter) (string, error) {
			return e.Prune(ctx, connection, doIt, report)
		})

	case actionDelete:
		if !a.enabled("confirm") {
			a.err = fmt.Errorf("not confirmed")
			return nil
		}
		entry, ok := m.selectedSnapshot()
		if !ok {
			m.action = nil
			return nil
		}
		id := entry.Manifest.ID
		m.action = nil
		e := m.engine
		return m.start("delete "+id, "Removing "+id+".",
			func(ctx context.Context, report engine.Reporter) (string, error) {
				if err := e.DeleteSnapshotEverywhere(ctx, id); err != nil {
					return "", err
				}
				return "deleted " + id, nil
			})
	}
	return nil
}

// planApply computes the plan the operator will confirm.
func (m *Model) planApply() tea.Cmd {
	a := m.action
	entry, ok := m.selectedSnapshot()
	if !ok {
		m.action = nil
		return nil
	}

	req := engine.ApplyRequest{
		Snapshot: entry.Manifest.ID,
		Target:   a.value("target"),
		Widen:    a.enabled("widen"),
	}
	scope := a.value("scope")
	switch {
	case strings.HasPrefix(scope, "set:"):
		req.Set = strings.TrimPrefix(scope, "set:")
	case scope == "tables":
		for _, t := range strings.Split(a.value("tables"), ",") {
			if t = strings.TrimSpace(t); t != "" {
				req.Tables = append(req.Tables, t)
			}
		}
		if len(req.Tables) == 0 {
			a.err = fmt.Errorf("name at least one table, schema-qualified")
			return nil
		}
	}

	a.stage = stagePlanning
	a.err = nil
	e := m.engine
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		plan, err := e.Plan(ctx, req, nil)
		return planReadyMsg{plan: plan, err: err}
	}
}

// planKey handles the plan preview: read it, then confirm or go back.
func (m *Model) planKey(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := m.action
	p := a.plan

	if p != nil && p.needsName {
		// A guarded target captures the keyboard while its name is typed, so
		// no shortcut can fire by accident mid-authorisation.
		switch key {
		case "esc":
			a.stage = stageForm
			p.typed = ""
			return m, nil
		case "enter":
			if p.typed == p.plan.Target.Conn.Name {
				return m, m.executePlan(p.plan)
			}
			a.err = fmt.Errorf("that is not %q", p.plan.Target.Conn.Name)
			p.typed = ""
			return m, nil
		case "backspace":
			if p.typed != "" {
				p.typed = p.typed[:len(p.typed)-1]
			}
			return m, nil
		default:
			if len(key) == 1 {
				p.typed += key
			}
			return m, nil
		}
	}

	switch key {
	case "esc":
		a.stage = stageForm
		a.err = nil
		return m, nil
	case "enter", "y":
		if p == nil {
			return m, nil
		}
		return m, m.executePlan(p.plan)
	case "w":
		// Widening from the plan screen, where the refusal that motivates it
		// is on display, rather than making the operator go back and guess.
		if f := a.field("widen"); f != nil && !f.disabled {
			f.on = true
			return m, m.planApply()
		}
	}
	_ = msg
	return m, nil
}

func (m *Model) executePlan(plan *engine.Plan) tea.Cmd {
	m.action = nil
	e := m.engine

	what := fmt.Sprintf("%d tables", len(plan.Selection))
	if plan.WholeDatabase {
		what = "the whole " + plan.Snapshot.Database + " database"
	}
	explain := fmt.Sprintf("Replacing %s on %s with the contents of %s.",
		what, plan.Target.Conn.Name, plan.Snapshot.ID)

	return m.start("apply → "+plan.Target.Conn.Name, explain,
		func(ctx context.Context, report engine.Reporter) (string, error) {
			if err := e.Execute(ctx, plan, report); err != nil {
				return "", err
			}
			return fmt.Sprintf("applied %s to %s", plan.Snapshot.ID, plan.Target.Conn.Name), nil
		})
}

// nowFunc is a seam for tests.
var nowFunc = time.Now
