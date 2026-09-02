// Package tui is pgctl's terminal UI.
//
// lazydocker-style layout, the same shape swarmctl uses: numbered list panels
// stacked on the left, one detail panel on the right showing whatever the
// focused panel has selected. The panels are a hierarchy read downwards — the
// environment chooses the databases, the database chooses the snapshots and
// sets — so moving down the left column narrows what the right pane is about.
//
// The UI holds no opinion about what is safe. Refusals, confirmations and load
// orders are the engine's, rendered here.
package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/pg"
)

// The left column's panels, in order.
const (
	panelConnections = iota
	panelDatabases
	panelSnapshots
	panelSets
	panelRuns
	panelCount
)

var panelTitles = [panelCount]string{"Connections", "Databases", "Snapshots", "Sets", "Runs"}

// Model is the whole UI.
type Model struct {
	engine *engine.Engine
	cfg    *config.Config

	width, height int

	// focus is the panel with the keys, and paneFocus whether the right pane
	// has taken them instead.
	focus     int
	cursors   [panelCount]int
	offsets   [panelCount]int
	paneFocus bool

	// tab is the right pane's selected tab, remembered per panel so that
	// returning to a panel returns to the tab you were reading.
	tabs       [panelCount]int
	paneCursor int
	paneOffset int

	// Data. Each is loaded asynchronously and may be absent.
	probes    map[string]*engine.Probe
	probing   map[string]bool
	entries   []*engine.Entry
	liveTable map[string][]pg.TableInfo // "<env>/<db>" -> tables
	liveErr   map[string]error
	loading   map[string]bool
	setInfo   map[string]*setSummary

	// runs is this session's operation history, newest last.
	runs   []*runRecord
	active *runRecord

	// filter is the / filter over the focused panel.
	filter    string
	filtering bool

	// action is the modal form in front of everything, when one is open.
	action *actionModel

	// help is the ? overlay.
	showHelp bool

	err    error
	status string

	// now is read once per frame so every duration on screen agrees.
	now time.Time

	// clock is where now comes from. A field rather than a call to time.Now,
	// so a frame can be pinned to a fixed instant: every duration on screen is
	// relative to it, and a golden written today still reads the same tomorrow.
	// Update overwrites now on every message, so pinning the field alone would
	// last exactly until the next keystroke.
	clock func() time.Time
}

// setSummary is a set resolved against a live database.
type setSummary struct {
	members []string
	added   []string
	err     error
	loading bool
}

// New builds the model.
func New(e *engine.Engine) *Model {
	m := &Model{
		engine:    e,
		cfg:       e.Config(),
		probes:    map[string]*engine.Probe{},
		probing:   map[string]bool{},
		liveTable: map[string][]pg.TableInfo{},
		liveErr:   map[string]error{},
		loading:   map[string]bool{},
		setInfo:   map[string]*setSummary{},
		clock:     time.Now,
	}
	m.now = m.clock()
	return m
}

// SetSize tells the model how big the terminal is.
//
// For a harness rendering a frame rather than a program running one: there is
// no WindowSizeMsg when nothing is attached to a terminal. Satisfies
// harness.Sizer.
func (m *Model) SetSize(w, h int) {
	if w > 0 {
		m.width = w
	}
	if h > 0 {
		m.height = h
	}
}

// Now pins the clock to an instant. Satisfies harness.Clock.
func (m *Model) Now(t time.Time) {
	m.clock = func() time.Time { return t }
	m.now = t
}

// Init loads what can be loaded without a network round trip, and starts
// probing environments in the background.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadSnapshots(), m.probeAll(), tick())
}

// Update handles a message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.now = m.clock()

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		return m, nil

	case tickMsg:
		return m, tick()

	case probeMsg:
		delete(m.probing, msg.probe.Connection)
		m.probes[msg.probe.Connection] = msg.probe
		return m, nil

	case snapshotsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.entries = msg.entries
		m.clampCursors()
		return m, nil

	case liveTablesMsg:
		delete(m.loading, msg.key)
		if msg.err != nil {
			m.liveErr[msg.key] = msg.err
		} else {
			delete(m.liveErr, msg.key)
			m.liveTable[msg.key] = msg.tables
		}
		return m, nil

	case setMembersMsg:
		s := m.setInfo[msg.key]
		if s == nil {
			s = &setSummary{}
			m.setInfo[msg.key] = s
		}
		s.loading = false
		s.members, s.added, s.err = msg.members, msg.added, msg.err
		return m, nil

	case planReadyMsg:
		if m.action == nil {
			return m, nil
		}
		if msg.err != nil {
			m.action.stage = stageForm
			m.action.err = msg.err
			return m, nil
		}
		m.action.stage = stagePlan
		m.action.plan = &planPreview{plan: msg.plan, needsName: msg.plan.Target.Conn.Guarded}
		return m, nil

	case runEventMsg:
		if m.active != nil {
			m.active.add(engine.Event(msg))
		}
		return m, m.waitForEvent()

	case runDoneMsg:
		return m, m.finishRun(msg)

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// key routes a keypress. Order matters: a modal takes everything, then the
// filter's text entry, then the global keys, then the focused surface.
func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.showHelp {
		m.showHelp = false
		return m, nil
	}
	if m.action != nil {
		return m.actionKey(msg)
	}
	if m.filtering {
		return m.filterKey(key)
	}

	switch key {
	case "ctrl+c":
		return m, m.quit()
	case "q":
		if m.active != nil && m.active.running {
			// A running operation is cancelled rather than abandoned, so the
			// engine's failure hooks get to bring an environment back up.
			m.active.cancel()
			m.status = "cancelling…"
			return m, nil
		}
		return m, m.quit()
	case "?":
		m.showHelp = true
		return m, nil
	case "/":
		m.filtering = true
		m.filter = ""
		return m, nil
	case "esc":
		switch {
		case m.filter != "":
			m.filter = ""
		case m.paneFocus:
			m.paneFocus = false
		}
		m.err = nil
		return m, nil
	case "tab":
		// Tab cycles the right pane's tabs when the pane has focus, and moves
		// focus into it when it does not.
		if !m.paneFocus {
			m.paneFocus = true
			return m, nil
		}
		m.tabs[m.focus] = (m.tabs[m.focus] + 1) % len(m.paneTabs())
		m.paneCursor, m.paneOffset = 0, 0
		return m, m.paneLoad()
	case "shift+tab":
		if m.paneFocus {
			tabs := len(m.paneTabs())
			m.tabs[m.focus] = (m.tabs[m.focus] + tabs - 1) % tabs
			m.paneCursor, m.paneOffset = 0, 0
			return m, m.paneLoad()
		}
		return m, nil
	case "1", "2", "3", "4", "5":
		m.focus = int(key[0] - '1')
		m.paneFocus = false
		m.filter = ""
		return m, m.onSelectionChanged()
	}

	if m.actionKeys(key) {
		return m, nil
	}
	return m.navigate(key)
}

// navigate moves the cursor in whichever surface has focus.
func (m *Model) navigate(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.paneFocus {
			if m.paneCursor > 0 {
				m.paneCursor--
			}
			return m, nil
		}
		if m.cursors[m.focus] > 0 {
			m.cursors[m.focus]--
		}
		return m, m.onSelectionChanged()
	case "down", "j":
		if m.paneFocus {
			if m.paneCursor < m.paneRowCount()-1 {
				m.paneCursor++
			}
			return m, nil
		}
		if m.cursors[m.focus] < m.panelLen(m.focus)-1 {
			m.cursors[m.focus]++
		}
		return m, m.onSelectionChanged()
	case "left", "h":
		if m.paneFocus {
			m.paneFocus = false
			return m, nil
		}
		if m.focus > 0 {
			m.focus--
			m.filter = ""
		}
		return m, m.onSelectionChanged()
	case "right", "l":
		if !m.paneFocus {
			m.paneFocus = true
		}
		return m, nil
	case "g", "home":
		m.setCursor(0)
		return m, m.onSelectionChanged()
	case "G", "end":
		m.setCursor(m.panelLen(m.focus) - 1)
		return m, m.onSelectionChanged()
	case "J":
		if m.focus < panelCount-1 {
			m.focus++
			m.filter = ""
		}
		return m, m.onSelectionChanged()
	case "K":
		if m.focus > 0 {
			m.focus--
			m.filter = ""
		}
		return m, m.onSelectionChanged()
	case "r":
		m.status = "reloading"
		return m, tea.Batch(m.loadSnapshots(), m.probeAll())
	}
	return m, nil
}

func (m *Model) setCursor(i int) {
	if m.paneFocus {
		m.paneCursor = clamp(i, m.paneRowCount()-1)
		return
	}
	m.cursors[m.focus] = clamp(i, m.panelLen(m.focus)-1)
}

// onSelectionChanged loads whatever the new selection needs. Selection is
// hierarchical, so moving the environment cursor changes what every panel
// below it is about.
func (m *Model) onSelectionChanged() tea.Cmd {
	m.clampCursors()
	return m.paneLoad()
}

func (m *Model) clampCursors() {
	for i := range m.cursors {
		m.cursors[i] = clamp(m.cursors[i], m.panelLen(i)-1)
	}
	m.paneCursor = clamp(m.paneCursor, m.paneRowCount()-1)
}

func (m *Model) quit() tea.Cmd {
	if m.active != nil && m.active.running {
		m.active.cancel()
	}
	return tea.Quit
}

// clamp keeps a cursor inside a list that may have shrunk under it.
func clamp(v, hi int) int {
	switch {
	case hi < 0, v < 0:
		return 0
	case v > hi:
		return hi
	default:
		return v
	}
}

// Run opens the UI against a config.
func Run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	model := New(engine.New(cfg))
	for _, w := range cfg.Warnings {
		model.status = w
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("terminal ui: %w", err)
	}
	return nil
}
