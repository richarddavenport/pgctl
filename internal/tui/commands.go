package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/pgctl/internal/engine"
)

// eventMsg carries one engine event into the update loop.
type eventMsg engine.Event

// tickMsg drives the redraw of a running operation.
type tickMsg time.Time

// tickInterval has to match the spinner's frame rate, not the rate at which
// anything interesting happens. Redrawing once a second while the spinner
// advances every 100ms means it jumps ten frames between redraws, which reads
// as flicker rather than rotation.
const tickInterval = 100 * time.Millisecond

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// doneMsg ends a run.
type doneMsg struct {
	err     error
	summary string
}

// startSnapshot begins a dump of every declared database on an environment.
func (m *Model) startSnapshot(env string) tea.Cmd {
	databases := make([]string, 0, len(m.engine.Config().Databases))
	for _, d := range m.engine.Config().Databases {
		databases = append(databases, d.Name)
	}

	explain := fmt.Sprintf("Copying %s from %s into %s. Nothing is written to %s.",
		strings.Join(databases, ", "), env, m.engine.StorageDir(), env)
	return m.start("snapshot "+env, explain, func(ctx context.Context, report engine.Reporter) (string, error) {
		for _, db := range databases {
			if _, err := m.engine.Dump(ctx, engine.DumpRequest{
				Environment: env,
				Database:    db,
			}, report); err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("snapshotted %d database(s) from %s", len(databases), env), nil
	})
}

// startApply executes the plan already on screen — the one that was confirmed,
// not a fresh one that might differ from it.
func (m *Model) startApply() tea.Cmd {
	plan := m.plan
	what := fmt.Sprintf("%d tables", len(plan.Selection))
	if plan.WholeDatabase {
		what = "the whole " + plan.Snapshot.Database + " database"
	}
	explain := fmt.Sprintf("Replacing %s on %s with the contents of %s.",
		what, plan.Target.Env.Name, plan.Snapshot.ID)
	return m.start("apply "+plan.Snapshot.ID, explain, func(ctx context.Context, report engine.Reporter) (string, error) {
		if err := m.engine.Execute(ctx, plan, report); err != nil {
			return "", err
		}
		return fmt.Sprintf("applied %s to %s", plan.Snapshot.ID, plan.Target.Env.Name), nil
	})
}

// buildPlan computes a plan without running it. Planning connects to the
// target, so it is a command rather than done inline — a slow or unreachable
// environment must not freeze the UI.
func (m *Model) buildPlan(widen bool) tea.Cmd {
	req := engine.ApplyRequest{
		Snapshot: m.chosen.ID,
		Target:   m.target,
		Set:      m.set,
		Widen:    widen,
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), planTimeout)
		defer cancel()
		plan, err := m.engine.Plan(ctx, req, nil)
		return planMsg{plan: plan, err: err}
	}
}

// planMsg delivers a computed plan.
type planMsg struct {
	plan *engine.Plan
	err  error
}

// start runs an operation in the background, forwarding its events into the
// update loop.
func (m *Model) start(kind, explain string, op func(context.Context, engine.Reporter) (string, error)) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	r := &run{
		kind:    kind,
		explain: explain,
		// Buffered: the engine must not block on a UI that is mid-render, and
		// an operation that outruns the buffer is one whose intermediate
		// progress nobody could have read anyway.
		events: make(chan engine.Event, 256),
		done:   make(chan error, 1),
		cancel: cancel,
	}
	m.run = r
	m.events = nil
	m.stage = stageRunning
	m.startedAt = nowFunc()
	m.err = nil
	m.status = ""
	m.progress = engine.Event{}
	m.progressStep = ""

	summary := make(chan string, 1)
	go func() {
		defer close(r.events)
		s, err := op(ctx, func(ev engine.Event) {
			select {
			case r.events <- ev:
			default:
			}
		})
		summary <- s
		r.done <- err
	}()

	return tea.Batch(tick(), m.waitForEvent(), func() tea.Msg {
		err := <-r.done
		return doneMsg{err: err, summary: <-summary}
	})
}

// waitForEvent blocks in a command until the next event arrives, which is how a
// bubbletea program consumes a channel.
func (m *Model) waitForEvent() tea.Cmd {
	r := m.run
	if r == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-r.events
		if !ok {
			// The channel closing is not the end of the run: doneMsg is,
			// and it carries the error.
			return nil
		}
		return eventMsg(ev)
	}
}
