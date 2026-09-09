package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/pgctl/internal/engine"
)

// planReadyMsg carries a computed plan back to the form that asked for it.
type planReadyMsg struct {
	gen  int
	plan *engine.Plan
	err  error
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
		connection, database := a.conn, a.database
		destinations := m.chosenDestinations()
		if len(destinations) == 0 {
			// No default, so no answer is an unanswered question rather than
			// "local". A snapshot is gigabytes and where it goes is not
			// something to infer from silence.
			a.err = fmt.Errorf("choose at least one destination — space chooses, " +
				"and more than one is fine")
			return nil
		}
		m.action = nil
		e := m.engine
		explain := fmt.Sprintf("Copying %s from %s to %s.",
			database, connection, strings.Join(destinations, " and "))
		// No total: how many tables a dump will find is not known until
		// pg_dump has read the catalog. See runRecord.total.
		return m.start("snapshot "+connection+"/"+database, explain, 0,
			func(ctx context.Context, report engine.Reporter) (string, error) {
				if _, err := e.Dump(ctx, engine.DumpRequest{
					Connection: connection, Database: database,
					At: nowFunc(), Destinations: destinations,
				}, report); err != nil {
					return "", err
				}
				return fmt.Sprintf("snapshotted %s from %s to %s",
					database, connection, strings.Join(destinations, " and ")), nil
			})

	case actionApply:
		return m.planApply()

	case actionMove:
		// The source and the database are where the panels are, and the target
		// list excludes the source, so "both the source and the target" is not
		// reachable.
		from, to, database := a.conn, a.value("to"), a.database
		keep := a.enabled("keep")
		m.action = nil
		e := m.engine
		explain := fmt.Sprintf("Refreshing %s on %s from %s, "+
			"staging a snapshot that is deleted afterwards.", database, to, from)
		return m.start(fmt.Sprintf("move %s→%s", from, to), explain, 0,
			func(ctx context.Context, report engine.Reporter) (string, error) {
				if err := e.Move(ctx, engine.MoveRequest{
					From: from, To: to, Database: database, Keep: keep,
				}, report); err != nil {
					return "", err
				}
				return fmt.Sprintf("moved %s from %s to %s", database, from, to), nil
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
		return m.start("prune", explain, 0,
			func(ctx context.Context, report engine.Reporter) (string, error) {
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
		return m.start("delete "+id, "Removing "+id+".", 0,
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
	a.plannedAt = m.now
	m.planView.Goto(0)
	e := m.engine
	// A new generation, so a plan the operator walked away from lands in the
	// void rather than drawing into the form that replaced it.
	gen := m.gen.Next()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		plan, err := e.Plan(ctx, req, nil)
		return planReadyMsg{gen: gen, plan: plan, err: err}
	}
}

// planArrived is a computed plan, or the refusal that came instead.
//
// A refusal is not a failure: it is the engine having decided, and the form goes
// back to the fields with the reason on it so the choice that caused it can be
// changed. That is why it does not go to m.err — the message belongs to the
// question being asked, not to the screen behind it.
func (m *Model) planArrived(msg planReadyMsg) {
	if m.action == nil || m.gen.Stale(msg.gen) {
		return
	}
	if msg.err != nil {
		m.action.stage = stageForm
		m.action.err = msg.err
		return
	}
	m.action.stage = stagePlan
	m.action.plan = msg.plan
	m.planView.Goto(0)
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

	// The one operation that knows its own denominator: the plan resolved the
	// selection against the target's catalog, so the meter has something real
	// to divide by.
	return m.start("apply → "+plan.Target.Conn.Name, explain, len(plan.Selection),
		func(ctx context.Context, report engine.Reporter) (string, error) {
			if err := e.Execute(ctx, plan, report); err != nil {
				return "", err
			}
			return fmt.Sprintf("applied %s to %s",
				plan.Snapshot.ID, plan.Target.Conn.Name), nil
		})
}

// nowFunc is a seam for tests.
var nowFunc = time.Now
