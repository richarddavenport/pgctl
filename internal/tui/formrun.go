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
	plan *engine.RunPlan
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
		connection := a.conn
		databases := m.chosenDatabases()
		if len(databases) == 0 {
			a.err = fmt.Errorf("choose at least one database — space chooses, " +
				"and a takes all of them")
			return nil
		}
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
			strings.Join(databases, ", "), connection, strings.Join(destinations, " and "))
		// No total: how many tables a dump will find is not known until pg_dump
		// has read the catalog. See runRecord.total.
		return m.start(snapshotKind(connection, databases), explain, 0,
			func(ctx context.Context, report engine.Reporter) (string, error) {
				// ONE timestamp for every database in the run, so a snapshot of
				// six databases is one set rather than six unrelated ones —
				// which is what `<env>/latest` has to be able to mean.
				at := nowFunc()
				for _, db := range databases {
					if _, err := e.Dump(ctx, engine.DumpRequest{
						Connection: connection, Database: db,
						At: at, Destinations: destinations,
					}, report); err != nil {
						return "", err
					}
				}
				return fmt.Sprintf("snapshotted %s from %s to %s",
					strings.Join(databases, ", "), connection,
					strings.Join(destinations, " and ")), nil
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
		run := a.run
		if run == nil {
			m.action = nil
			return nil
		}
		id := run.ID
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
	if a.run == nil {
		m.action = nil
		return nil
	}

	databases := m.chosenDatabases()
	if len(databases) == 0 {
		a.err = fmt.Errorf("choose at least one database of the snapshot — " +
			"space chooses, a takes all")
		return nil
	}

	req := engine.RunApplyRequest{
		Run:       a.run.ID,
		Target:    a.value("target"),
		Databases: databases,
		Widen:     a.enabled("widen"),
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
		plan, err := e.PlanRun(ctx, req, nil)
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

func (m *Model) executePlan(plan *engine.RunPlan) tea.Cmd {
	m.action = nil
	e := m.engine

	databases := make([]string, 0, len(plan.Plans))
	for _, one := range plan.Plans {
		databases = append(databases, one.Snapshot.Database)
	}
	explain := fmt.Sprintf("Replacing %s on %s with the contents of %s: %s.",
		plural(plan.Tables(), "table"), plan.Target, plan.Run.ID,
		strings.Join(databases, ", "))

	// The one operation that knows its own denominator: every plan resolved its
	// selection against the target's catalog, so the meter has something real
	// to divide by — the tables of every database it will restore.
	return m.start("apply → "+plan.Target, explain, plan.Tables(),
		func(ctx context.Context, report engine.Reporter) (string, error) {
			if err := e.ExecuteRun(ctx, plan, report); err != nil {
				return "", err
			}
			return fmt.Sprintf("applied %s to %s", plan.Run.ID, plan.Target), nil
		})
}

// snapshotKind names the run for the Runs panel, which is 28 columns wide.
//
// One database is named; several are counted. "snapshot prd/claims,
// product-development, quote" truncates to "snapshot prd/claims, produc" and
// reads as a snapshot of something called that.
func snapshotKind(connection string, databases []string) string {
	if len(databases) == 1 {
		return "snapshot " + connection + "/" + databases[0]
	}
	return fmt.Sprintf("snapshot %s (%d dbs)", connection, len(databases))
}

// nowFunc is a seam for tests.
var nowFunc = time.Now
