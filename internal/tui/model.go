package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// stage is which question the UI is asking. A restore is a sequence of
// decisions — what to restore, where to, how much of it, and are you sure —
// and each stage is one of them, so that going back is going back one decision.
type stage int

const (
	stageSnapshots stage = iota // browse what exists
	stageSourceEnv              // take a snapshot: from where
	stageTargetEnv              // apply: to where
	stageScope                  // apply: whole database, or which set
	stagePlan                   // apply: the plan, and the confirmation
	stageRunning                // an operation in flight
)

// Model is the UI's state.
type Model struct {
	engine *engine.Engine

	stage stage
	width int
	// height is tracked so that a long list pages rather than overflowing;
	// bubbletea reports 0 until the first resize, which is treated as unknown.
	height int

	snapshots []*snapshot.Manifest
	envs      []config.Environment
	sets      []config.Set

	cursor int
	// cursors remembers each stage's position, so returning to a list returns
	// to where you were in it.
	cursors map[stage]int

	// The apply being assembled.
	chosen *snapshot.Manifest
	target string
	set    string
	plan   *engine.Plan

	// The run in flight.
	run       *run
	events    []engine.Event
	startedAt time.Time

	// progress is the latest progress event, redrawn in place rather than
	// appended, and progressStep the step it belongs to.
	progress     engine.Event
	progressStep string

	// confirmation is the typed name for a guarded environment.
	confirmation string
	confirming   bool

	err    error
	status string
}

// run is an operation executing in the background, with the channel its events
// arrive on.
type run struct {
	kind string
	// explain is one plain sentence saying what the operation does and where
	// its output goes. "Snapshot" means nothing to someone who has not read
	// the design notes; "reading qat into .pgctl/snapshots" means something to
	// anyone.
	explain string
	events  chan engine.Event
	done    chan error
	cancel  context.CancelFunc
}

// New builds the model.
func New(e *engine.Engine) *Model {
	m := &Model{engine: e, cursors: map[stage]int{}}
	m.envs = e.Config().Environments
	m.sets = e.Config().Sets
	m.reload()
	return m
}

func (m *Model) reload() {
	all, err := m.engine.Snapshots()
	if err != nil {
		m.err = err
		return
	}
	// Newest first: the snapshot anyone wants is almost always the last one.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	m.snapshots = all
}

// Init satisfies tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update handles a message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// A zero size — a pty with none, or a terminal mid-resize — is not a
		// size to lay out against.
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		return m, nil

	case tickMsg:
		// A running operation redraws once a second whether or not anything
		// has happened. Without this the screen freezes at whatever the last
		// event said, and an operation that is working looks identical to one
		// that has hung — which is exactly how it looked.
		if m.stage != stageRunning {
			return m, nil
		}
		return m, tick()

	case eventMsg:
		ev := engine.Event(msg)
		if ev.Kind == engine.EventProgress && ev.Step == m.progressStep {
			// Progress replaces the previous progress line for the same step
			// rather than scrolling it, so a byte counter counts in place.
			m.progress = ev
			return m, m.waitForEvent()
		}
		if ev.Kind == engine.EventProgress {
			m.progressStep = ev.Step
			m.progress = ev
			return m, m.waitForEvent()
		}
		m.events = append(m.events, ev)
		return m, m.waitForEvent()

	case doneMsg:
		m.stage = stageSnapshots
		m.cursor = m.cursors[stageSnapshots]
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.status = msg.summary
		}
		m.run = nil
		m.reload()
		return m, nil

	case planMsg:
		if msg.err != nil {
			m.err = msg.err
			// A refusal belongs on the screen that caused it, so that the
			// operator can widen the selection or choose another set without
			// starting over.
			m.stage = stageScope
			return m, nil
		}
		m.plan = msg.plan
		m.stage = stagePlan
		m.cursor = 0
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// A typed confirmation captures the keyboard: every printable key is part
	// of the environment's name, so no shortcut can fire by accident while
	// someone is authorising a destructive act.
	if m.confirming {
		switch key {
		case "esc":
			m.confirming = false
			m.confirmation = ""
		case "enter":
			if m.confirmation == m.plan.Target.Env.Name {
				m.confirming = false
				return m, m.startApply()
			}
			m.err = fmt.Errorf("that is not %q", m.plan.Target.Env.Name)
			m.confirmation = ""
		case "backspace":
			if m.confirmation != "" {
				m.confirmation = m.confirmation[:len(m.confirmation)-1]
			}
		default:
			if len(key) == 1 {
				m.confirmation += key
			}
		}
		return m, nil
	}

	switch key {
	case "ctrl+c", "q":
		if m.run != nil {
			// Cancelling lets the engine's failure hooks run, which is the
			// difference between a stopped apply and an environment left down.
			m.run.cancel()
			m.status = "cancelling…"
			return m, nil
		}
		return m, tea.Quit

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		if m.cursor < m.itemCount()-1 {
			m.cursor++
		}
		return m, nil

	case "esc":
		m.err = nil
		m.back()
		return m, nil
	}

	return m.action(key)
}

// action handles the keys that mean something different in each stage.
func (m *Model) action(key string) (tea.Model, tea.Cmd) {
	switch m.stage {
	case stageSnapshots:
		switch key {
		case "n":
			m.goTo(stageSourceEnv)
		case "r":
			m.reload()
			m.status = "reloaded"
		case "enter", "a":
			if len(m.snapshots) == 0 {
				m.err = fmt.Errorf("no snapshots yet — press n to take one")
				return m, nil
			}
			m.chosen = m.snapshots[m.cursor]
			if !m.chosen.Complete() {
				m.err = fmt.Errorf("%s did not finish and cannot be applied", m.chosen.ID)
				return m, nil
			}
			m.goTo(stageTargetEnv)
		}

	case stageSourceEnv:
		if key == "enter" {
			return m, m.startSnapshot(m.envs[m.cursor].Name)
		}

	case stageTargetEnv:
		if key == "enter" {
			env := m.envs[m.cursor]
			if env.Protected {
				m.err = fmt.Errorf("%s is protected and can never be an apply target", env.Name)
				return m, nil
			}
			m.target = env.Name
			m.goTo(stageScope)
		}

	case stageScope:
		if key == "enter" {
			m.set = ""
			if m.cursor > 0 {
				m.set = m.scopeSets()[m.cursor-1].Name
			}
			return m, m.buildPlan(false)
		}

	case stagePlan:
		switch key {
		case "w":
			// Widening is offered rather than applied: the operator sees which
			// tables joined the selection and why.
			return m, m.buildPlan(true)
		case "enter", "y":
			if m.plan == nil {
				return m, nil
			}
			if m.plan.Target.Env.Guarded {
				m.confirming = true
				m.confirmation = ""
				return m, nil
			}
			return m, m.startApply()
		}
	}
	return m, nil
}

func (m *Model) goTo(s stage) {
	m.cursors[m.stage] = m.cursor
	m.stage = s
	m.cursor = m.cursors[s]
	if m.cursor >= m.itemCount() {
		m.cursor = 0
	}
	m.err = nil
	m.status = ""
}

func (m *Model) back() {
	switch m.stage {
	case stageSourceEnv, stageTargetEnv:
		m.goTo(stageSnapshots)
	case stageScope:
		m.goTo(stageTargetEnv)
	case stagePlan:
		m.plan = nil
		m.goTo(stageScope)
	}
}

// scopeSets are the sets applicable to the chosen snapshot's database.
func (m *Model) scopeSets() []config.Set {
	var out []config.Set
	for _, s := range m.sets {
		if m.chosen == nil || s.Database == m.chosen.Database {
			out = append(out, s)
		}
	}
	return out
}

func (m *Model) itemCount() int {
	switch m.stage {
	case stageSnapshots:
		return len(m.snapshots)
	case stageSourceEnv, stageTargetEnv:
		return len(m.envs)
	case stageScope:
		return len(m.scopeSets()) + 1
	}
	return 0
}

// summariseEvents is what the run view shows: the last few lines, since an
// operation can emit hundreds.
func (m *Model) summariseEvents(limit int) []engine.Event {
	if len(m.events) <= limit {
		return m.events
	}
	return m.events[len(m.events)-limit:]
}

// planTimeout bounds planning. It connects to the target and reads its
// catalog; slower than that means something is wrong with the environment, not
// with the plan.
const planTimeout = 2 * time.Minute

// nowFunc is a seam for tests.
var nowFunc = time.Now

// elapsed formats a running duration as m:ss, or h:mm:ss past an hour.
//
// Not time.Duration.String(): "1m0s" and "1h0m0s" are hard to read at a glance
// and change width as they tick, which makes a status line jitter.
func elapsed(since time.Time) string {
	d := time.Since(since)
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second).Seconds())
	h, m, sec := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}
