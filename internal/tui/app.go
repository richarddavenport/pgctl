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

	"github.com/richarddavenport/tuikit/app"
	"github.com/richarddavenport/tuikit/comp"

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
	paneFocus bool

	// lists own the cursors and the scroll offsets that used to be two arrays
	// of ints here, plus the window arithmetic that kept them in step — a
	// comp.List will not let a caller assign either, because the invariant
	// between them is the whole component.
	lists    [panelCount]comp.List
	paneList comp.List

	// split divides the panel column from the detail pane, and is draggable.
	split comp.Split

	// tab is the right pane's selected tab, remembered per panel so that
	// returning to a panel returns to the tab you were reading.
	tabs [panelCount]int

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

	// help is the ? overlay. It scrolls, because the full key list is 35 lines
	// and a 24-line terminal is a normal one.
	showHelp   bool
	helpOffset int

	err    error
	status string

	// now is read once per frame so every duration on screen agrees.
	now time.Time

	// canvas is the last frame, kept so a click can ask what it landed on. The
	// frame IS the region list, so there is nothing else to keep in step.
	canvas *comp.Canvas
	mouse  app.Mouse

	// frame is the rect the last Draw was given.
	//
	// The runner makes the canvas and is the authority on how big it is — it
	// keeps its own default until a WindowSizeMsg arrives, which under a pty
	// with no size attached is never. So the model asking ITSELF how wide the
	// screen is gets a different answer from the canvas it is drawing into,
	// and everything computed off the wrong one lands somewhere the frame is
	// not. Recording the rect makes the two the same fact, and it is also what
	// a drag needs: the divider moves relative to the body it was last drawn
	// in, not the body it would be drawn in next.
	frame comp.Rect

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

	for panel := range m.lists {
		m.lists[panel] = comp.List{
			Name:  panelRowRegions[panel],
			Empty: "",
			// No marker column. pgctl's panels are 28 columns of content and
			// the selection is already a full-width highlight; a "> " would
			// cost two of them on every row to say what the colour says. The
			// pane's list does have one, because there the cursor is a single
			// row inside a body of prose.
			Selected:   &selectedStyle,
			Unfocused:  &currentStyle,
			Status:     &mutedStyle,
			EmptyStyle: &mutedStyle,
		}
	}
	m.paneList = comp.List{
		Name:       regBody,
		Selected:   &currentStyle,
		Unfocused:  &currentStyle,
		Status:     &mutedStyle,
		EmptyStyle: &mutedStyle,
	}
	// A quarter of the width to the panels, and never narrower than the widest
	// thing that has to stay readable there — a snapshot timestamp with its
	// location marker, which is what leftWidth was measured from. A third, the
	// obvious default, gave the column 44 columns at 132 and padded every row
	// with a dozen of dead space.
	m.split = comp.Split{Name: regSplit, Ratio: [2]int{1, 4}, Min: leftWidth}
	return m
}

// Canvas is the last frame, so a mouse event or a capture script can address a
// region by name.
func (m *Model) Canvas() *comp.Canvas { return m.canvas }

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
func (m *Model) Update(msg tea.Msg) (app.Model, tea.Cmd) {
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

	case tea.MouseMsg:
		return m, m.onMouse(msg)

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// key routes a keypress. Order matters: a modal takes everything, then the
// filter's text entry, then the global keys, then the focused surface.
func (m *Model) key(msg tea.KeyMsg) (app.Model, tea.Cmd) {
	key := msg.String()

	if m.showHelp {
		// The key list is longer than a short terminal, so the overlay scrolls
		// rather than showing two thirds of itself and no way to reach the
		// rest. Anything that is not a scroll closes it, which keeps the
		// "press any key" feel for the common case where it all fits.
		switch key {
		case "up", "k":
			m.helpOffset = max(m.helpOffset-1, 0)
		case "down", "j":
			m.helpOffset++
		case "g", "home":
			m.helpOffset = 0
		default:
			m.showHelp = false
			m.helpOffset = 0
		}
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
		m.paneList.Reset()
		return m, m.paneLoad()
	case "shift+tab":
		if m.paneFocus {
			tabs := len(m.paneTabs())
			m.tabs[m.focus] = (m.tabs[m.focus] + tabs - 1) % tabs
			m.paneList.Reset()
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
func (m *Model) navigate(key string) (app.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.paneFocus {
			if m.paneList.Cursor() > 0 {
				m.paneList.Move(-1)
			}
			return m, nil
		}
		if m.cursor(m.focus) > 0 {
			m.lists[m.focus].Move(-1)
		}
		return m, m.onSelectionChanged()
	case "down", "j":
		if m.paneFocus {
			if m.paneList.Cursor() < m.paneRowCount()-1 {
				m.paneList.Move(1)
			}
			return m, nil
		}
		if m.cursor(m.focus) < m.panelLen(m.focus)-1 {
			m.lists[m.focus].Move(1)
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
	// No clamping here. comp.List settles the cursor against the rows that
	// actually exist when it draws, because that is the only moment the rows
	// are known — the list does not hold them.
	if m.paneFocus {
		m.paneList.Select(i)
		return
	}
	m.lists[m.focus].Select(i)
}

// cursor is the selected row of a panel. A method rather than a field read,
// because the cursor lives in the list now and a caller that could assign it
// could reintroduce the offset bug the component exists to prevent.
func (m *Model) cursor(panel int) int { return m.lists[panel].Cursor() }

// onSelectionChanged loads whatever the new selection needs. Selection is
// hierarchical, so moving the environment cursor changes what every panel
// below it is about.
//
// It used to clamp every cursor first, from when they were plain ints that
// could point past a list that had shrunk under them. comp.List owns that now
// and does it at DRAW time, which is the only moment it knows what rows exist.
// Clamping here was worse than redundant: it went through Select, and Select
// cancels a pending Move — so every arrow key was applied and then immediately
// undone, and the cursor never left the first row.
func (m *Model) onSelectionChanged() tea.Cmd { return m.paneLoad() }

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

	// The runner owns the canvas, its size and its chrome — it is the only
	// place comp.NewCanvas is called, because those are decisions with one
	// right answer per program and a tool that made them itself would make
	// them differently in each of its screens.
	//
	// Mouse cell motion, because the split between the panel column and the
	// detail pane is draggable and a drag needs the moves between press and
	// release, not just the two ends.
	p := tea.NewProgram(
		app.New(model, app.WithChrome(Chrome)),
		tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("terminal ui: %w", err)
	}
	return nil
}
