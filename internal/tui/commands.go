package tui

import (
	"context"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/pg"
)

// Everything that touches a database, a blob container or a subprocess happens
// in a command. Nothing in Update may block: a slow environment must make one
// panel say "probing" rather than freeze the whole UI.

// tickInterval has to match the spinner's frame rate, not the rate at which
// anything interesting happens. Redrawing once a second while the spinner
// advances every 100ms means it jumps ten frames between redraws, which reads
// as flicker rather than rotation.
const tickInterval = 100 * time.Millisecond

// probeTimeout bounds a reachability check. An environment behind a firewall
// that drops packets rather than refusing them would otherwise leave a panel
// saying "probing" for the rest of the session.
const probeTimeout = 20 * time.Second

// loadTimeout bounds a catalog read.
const loadTimeout = 2 * time.Minute

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type (
	probeMsg     struct{ probe *engine.Probe }
	snapshotsMsg struct {
		entries []*engine.Entry
		err     error
	}
	liveTablesMsg struct {
		key    string
		tables []pg.TableInfo
		err    error
	}
	setMembersMsg struct {
		key            string
		members, added []string
		err            error
	}
)

// probeAll starts a probe of every environment at once. They are independent,
// and one unreachable environment must not delay the rest.
func (m *Model) probeAll() tea.Cmd {
	var cmds []tea.Cmd
	for _, env := range m.cfg.Environments {
		if cmd := m.probe(env.Name); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) probe(name string) tea.Cmd {
	if m.probing[name] {
		return nil
	}
	m.probing[name] = true
	e := m.engine
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		defer cancel()
		p := e.ProbeEnvironment(ctx, name)
		return probeMsg{probe: &p}
	}
}

func (m *Model) loadSnapshots() tea.Cmd {
	e := m.engine
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		entries, err := e.Index(ctx, nil)
		return snapshotsMsg{entries: entries, err: err}
	}
}

func (m *Model) loadLiveTables(env, database string) tea.Cmd {
	key := liveKey(env, database)
	if m.loading[key] {
		return nil
	}
	m.loading[key] = true
	e := m.engine
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		tables, err := e.LiveTables(ctx, env, database)
		return liveTablesMsg{key: key, tables: tables, err: err}
	}
}

func (m *Model) loadSetMembers(env, database, set string) tea.Cmd {
	key := setKey(env, database, set)
	if s := m.setInfo[key]; s != nil && (s.loading || s.members != nil || s.err != nil) {
		return nil
	}
	m.setInfo[key] = &setSummary{loading: true}
	e := m.engine
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		members, added, err := e.SetMembers(ctx, env, database, set)
		return setMembersMsg{key: key, members: members, added: added, err: err}
	}
}

// runRecord is one operation, kept for the Runs panel so a failure can be read
// after the screen that reported it has gone.
type runRecord struct {
	kind      string
	explain   string
	startedAt time.Time
	endedAt   time.Time

	mu     sync.Mutex
	events []engine.Event

	// progress is the newest progress event, redrawn in place rather than
	// appended, so a byte counter counts instead of scrolling.
	progress engine.Event

	running bool
	err     error
	summary string

	cancel func()
	ch     chan engine.Event
	done   chan runResult
}

type runResult struct {
	summary string
	err     error
}

type (
	runEventMsg engine.Event
	runDoneMsg  runResult
)

func (r *runRecord) add(ev engine.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ev.Kind == engine.EventProgress {
		r.progress = ev
		return
	}
	r.events = append(r.events, ev)
}

// log returns a copy of the record's events, safe to render while the operation
// is still writing to it.
func (r *runRecord) log() []engine.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]engine.Event{}, r.events...)
}

func (r *runRecord) latestProgress() engine.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.progress
}

func (r *runRecord) duration(now time.Time) time.Duration {
	if r.running {
		return now.Sub(r.startedAt)
	}
	return r.endedAt.Sub(r.startedAt)
}

// start runs an operation in the background, recording it.
func (m *Model) start(kind, explain string, op func(context.Context, engine.Reporter) (string, error)) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	r := &runRecord{
		kind:      kind,
		explain:   explain,
		startedAt: time.Now(),
		running:   true,
		cancel:    cancel,
		// Buffered: the engine must not block on a UI that is mid-render, and
		// an operation outrunning the buffer is one whose intermediate progress
		// nobody could have read anyway.
		ch:   make(chan engine.Event, 256),
		done: make(chan runResult, 1),
	}
	m.runs = append(m.runs, r)
	m.active = r
	m.focus = panelRuns
	m.paneFocus = false
	m.cursors[panelRuns] = 0
	m.err = nil
	m.status = ""

	go func() {
		defer close(r.ch)
		summary, err := op(ctx, func(ev engine.Event) {
			select {
			case r.ch <- ev:
			default:
			}
		})
		r.done <- runResult{summary: summary, err: err}
	}()

	return tea.Batch(m.waitForEvent(), func() tea.Msg { return runDoneMsg(<-r.done) })
}

// waitForEvent blocks in a command until the next event arrives, which is how a
// bubbletea program consumes a channel.
func (m *Model) waitForEvent() tea.Cmd {
	r := m.active
	if r == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-r.ch
		if !ok {
			return nil
		}
		return runEventMsg(ev)
	}
}

// finishRun closes out the active operation and reloads whatever it changed.
func (m *Model) finishRun(res runDoneMsg) tea.Cmd {
	r := m.active
	if r == nil {
		return nil
	}
	r.running = false
	r.endedAt = time.Now()
	r.err = res.err
	r.summary = res.summary
	m.active = nil

	if res.err != nil {
		m.err = res.err
	} else if res.summary != "" {
		m.status = res.summary
	}

	// An operation changes what is on disk and what is in the target, so both
	// the snapshot list and the probes are stale the moment it ends.
	return tea.Batch(m.loadSnapshots(), m.probeAll())
}
