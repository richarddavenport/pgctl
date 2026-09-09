// Package tui is pgctl's terminal UI.
//
// lazydocker-style layout: numbered list panels stacked on the left, one detail
// pane on the right showing whatever the focused panel has selected. The panels
// are a hierarchy read downwards — the connection chooses the databases, the
// database chooses the snapshots and the sets — so moving down the left column
// narrows what the right pane is about.
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

// panelStatusWidth is the columns a panel reserves for comp.Row.Status: the
// reachability dot, the mark on a snapshot that never finished, whether a run
// failed. Declared per panel rather than measured, so the glyphs line up down
// a panel even on the frame where the only row carrying one has scrolled off.
var panelStatusWidth = [panelCount]int{
	panelConnections: 2,
	panelSnapshots:   2,
	panelRuns:        2,
}

// Model is the whole UI, and it is a POINTER.
//
// tuikit's components own state — where a cursor is, what a viewport shows,
// where a divider sits, how far a log has been scrolled back — and state inside
// a component cannot survive being copied on every message. A value model
// compiles, runs, and silently forgets every scroll.
type Model struct {
	engine *engine.Engine
	cfg    *config.Config

	width, height int

	// focus is the panel with the keys, and paneFocus whether the right pane
	// has taken them instead.
	focus     int
	paneFocus bool

	// The components own the cursors, the viewports and the arithmetic that
	// keeps them honest. None of it is assignable from here, which is the
	// point: the invariant between a cursor and an offset is the whole
	// component, and every version of it pgctl wrote by hand had a bug in it.
	lists     [panelCount]comp.List
	paneList  comp.List
	multiList comp.List
	logPane   comp.LogPane
	split     comp.Split

	// planView is the plan preview, and it is a Viewer rather than a List
	// because a plan is a DOCUMENT: it opens at the top, has nothing to
	// select, and the number it shows means where you are in it. A widened
	// selection's load order is seven layers of table names, which is longer
	// than a modal on a short terminal.
	planView comp.Viewer

	// gen drops the answer to a question nobody is waiting for any more.
	//
	// The bug it prevents is nasty because nothing errors: a plan computed for
	// a form the operator has closed, or for a target they then changed, draws
	// into the form that replaced it — and a plan is the last thing between an
	// operator and a destructive act.
	gen app.Gen

	// tab is the right pane's selected tab, remembered per panel so that
	// returning to a panel returns to the tab you were reading.
	tabs [panelCount]int

	// Data. Each is loaded asynchronously and may be absent.
	probes    map[string]*engine.Probe
	probing   map[string]bool
	entries   []*engine.Entry
	liveTable map[string][]pg.TableInfo // "<conn>/<db>" -> tables
	liveErr   map[string]error
	loading   map[string]bool
	setInfo   map[string]*setSummary

	// runs is this session's operation history, newest last.
	runs   []*runRecord
	active *runRecord

	// filter is the / filter over the focused panel, and comp.Input draws it
	// with a caret you can move — which the hand-rolled version could not, so
	// a typo meant deleting back to it.
	filter    comp.Input
	filtering bool

	// action is the modal form in front of everything, when one is open.
	action *actionModel

	// leaving is the question in front of q while an operation is in flight.
	//
	// q means leave, everywhere, in every tuikit tool — tuikit decision 42, and
	// the one rule the framework imposes. What LEAVING COSTS is still pgctl's
	// business, and here it costs a running restore: the engine's onFailure
	// hooks are what bring an environment back up, so the operation is
	// cancelled rather than abandoned. Answering the question is what does it.
	leaving bool

	// commands is the ctrl+p directory of everything pgctl can do right now,
	// with the key that does it. A directory of the keyboard rather than a
	// replacement for it — every row names its key, and a refused row says why
	// instead of vanishing.
	commands    comp.Palette
	showCommand bool

	// help is the ? overlay. It scrolls, because the full key list is longer
	// than a 24-line terminal and a help screen that says "… more" with no way
	// to reach the rest is the least helpful state a help screen has.
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

	// clock is where now comes from. A field rather than a call to time.Now, so
	// a frame can be pinned to a fixed instant: every duration on screen is
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
			Name: panelRowRegions[panel],
			// The status column, where a panel has one. Two columns on the
			// three panels whose rows carry a state glyph and none on the two
			// that do not — a reserved column that is always blank is two of
			// twenty-eight spent saying nothing, and the panels are narrow.
			StatusWidth: panelStatusWidth[panel],
			// No marker column. pgctl's panels are 28 columns of content and
			// the selection is already a full-width highlight; a "> " would
			// cost two of them on every row to say what the colour says. The
			// pane's list does have one, because there the cursor is a single
			// row inside a body of prose.
			Selected:   &selectedStyle,
			Unfocused:  &currentStyle,
			Status:     &mutedStyle,
			EmptyStyle: &mutedStyle,
			// Five lists stacked in one column, so the status row is charged
			// five times: at 80x24 that was five of about twenty-one body rows,
			// a quarter of the column, on counters reading 3/3 and 1/1 beside
			// panel titles that already say the same number. It earns its row
			// on one big list; it does not earn five. tuikit #46.
			NoStatus: true,
		}
	}
	m.planView = comp.Viewer{
		Name:       regConfirmBox,
		NoCursor:   true,
		Status:     &mutedStyle,
		EmptyStyle: &mutedStyle,
	}
	// The options of a multi-select field in a modal. Its cursor is the FIELD's,
	// so this list is only ever Selected, never Moved — and the ●/○ is a status
	// column so it keeps its own colour under the highlight: whether an option
	// is in is the state, and the cursor is where you are. Two facts, and the
	// selection must not eat one of them.
	m.multiList = comp.List{
		Name:        regModalOptions,
		StatusWidth: 2,
		Marker:      "▸ ",
		Blank:       "  ",
		// The cursor is a MARKER and an accent, not a filled bar, and that is
		// the one place in pgctl where those differ.
		//
		// Two reasons. A full-width reverse-video bar is heavy in a six-row box
		// — the panels earn it because they are dense and twenty-eight columns
		// wide, and a modal's list is neither. And it sidesteps tuikit issue
		// 90: comp.List fills the row and then draws the status glyph with the
		// glyph's OWN style, which carries a foreground and no background, so a
		// filled row comes out with a two-column hole in it exactly where the
		// ●/○ is. A reader asked whether that was intentional. It is not, it is
		// not fixable from here — the same StatusStyle is used on every row and
		// only one of them is filled — and a cursor that does not fill has
		// nothing to leave a hole in.
		Selected:   &currentStyle,
		Status:     &mutedStyle,
		EmptyStyle: &mutedStyle,
		// The field's own row carries the count, so the list's status row would
		// say it twice — in a modal, where the row is an option.
		NoStatus: true,
	}
	m.paneList = comp.List{
		Name:       regBody,
		Marker:     "› ",
		Blank:      "  ",
		Selected:   &currentStyle,
		Unfocused:  &currentStyle,
		Status:     &mutedStyle,
		EmptyStyle: &mutedStyle,
	}
	// Following is a place, not a mode: the log starts pinned to the newest
	// line, and scrolling back is what leaves it. Nothing here has to remember
	// which of those it is in.
	m.logPane = comp.LogPane{
		Follow:     true,
		Empty:      "  nothing logged yet",
		Time:       &mutedStyle,
		Stderr:     &warnStyle,
		Status:     &mutedStyle,
		EmptyStyle: &mutedStyle,
	}
	m.filter = comp.Input{
		Prompt:           "/",
		Placeholder:      "filter",
		TextStyle:        &accentStyle,
		PromptStyle:      &mutedStyle,
		PlaceholderStyle: &mutedStyle,
		CursorFG:         &cursorFG,
		CursorBG:         &cursorBG,
	}
	m.commands = newCommandPalette()
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
// probing the selected connection in the background.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadSnapshots(), m.probeSelected(), tick())
}

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (app.Model, tea.Cmd) {
	m.now = m.clock()

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil

	case tickMsg:
		// The tick is also where a lazy probe starts, and it has to be:
		// comp.List records a Move and resolves it when it DRAWS, so a command
		// launched in the same Update as the keystroke reads the cursor from
		// before the key. A mouse click is immediate and every arrow key is
		// not, so "on change" covered one and missed the other.
		//
		// probeSelected is a map lookup when there is nothing to do, so asking
		// every frame costs nothing and states the rule more clearly than "on
		// change" did: whatever is selected gets reached, however it came to be
		// selected — a key, a click, a filter, or a list clamping itself when
		// the rows moved underneath.
		return m, tea.Batch(tick(), m.probeSelected())

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
		m.planArrived(msg)
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
		return m, m.key(msg)
	}
	return m, nil
}

// key routes one keystroke, and the ORDER is the contract.
//
// Whatever is capturing gets the key first, then the screen, then the globals.
// A global handled before the filter box is a filter box you cannot type q
// into — press q to search for "qat" and the program exits. app.Keys makes that
// order the shape of a struct rather than an early return somebody can delete.
func (m *Model) key(msg tea.KeyMsg) tea.Cmd {
	return app.Keys{
		Capture: m.capture(),
		Screen:  m.screenKey,
		Global:  m.globalKey,
	}.Route(msg)
}

// capture is whatever is eating keystrokes, or nil.
//
// A nil Capture is a tool with nothing capturing, rather than a tool that
// forgot to say. The order inside is the depth of the stack: the question in
// front of quitting is on top of the form, which is on top of the directory.
func (m *Model) capture() app.Handled {
	switch {
	case m.leaving:
		return m.leavingKey
	case m.action != nil:
		return m.actionKey
	case m.showCommand:
		return m.commandKey
	case m.showHelp:
		return m.helpKey
	case m.filtering:
		return m.filterKey
	}
	return nil
}

// screenKey is the keys that act on what is focused right now.
func (m *Model) screenKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch key := msg.String(); key {
	case "1", "2", "3", "4", "5":
		m.focus = int(key[0] - '1')
		m.paneFocus = false
		m.clearFilter()
		return m.onSelectionChanged(), true

	case "tab":
		// Tab cycles the right pane's tabs when the pane has focus, and moves
		// focus into it when it does not.
		if !m.paneFocus {
			m.paneFocus = true
			return nil, true
		}
		m.tabs[m.focus] = (m.tabs[m.focus] + 1) % len(m.paneTabs())
		m.paneList.Reset()
		return m.paneLoad(), true

	case "shift+tab":
		if m.paneFocus {
			tabs := len(m.paneTabs())
			m.tabs[m.focus] = (m.tabs[m.focus] + tabs - 1) % tabs
			m.paneList.Reset()
			return m.paneLoad(), true
		}
		return nil, true

	case "/":
		m.filtering = true
		m.filter.Text, m.filter.Cursor = "", 0
		return nil, true

	case "ctrl+p":
		m.showCommand = true
		m.commands.Groups = m.commandGroups()
		m.commands.Select(0)
		return nil, true

	case "r":
		m.status = "reloading"
		return tea.Batch(m.loadSnapshots(), m.probeAll()), true
	}

	if m.actionKeys(msg.String()) {
		return nil, true
	}
	return m.navigate(msg.String())
}

// globalKey is what works everywhere, and it runs last.
func (m *Model) globalKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		// ctrl+c is not a question. It is the terminal's own "stop", and a tool
		// that puts a dialog in front of it has taken away the one key a reader
		// is certain of — so the operation is still cancelled, because the
		// engine's onFailure hooks are what bring an environment back up, and
		// then pgctl leaves without asking.
		return m.quit(), true
	case "q":
		if m.active != nil && m.active.running {
			m.leaving = true
			return nil, true
		}
		return tea.Quit, true
	case "?":
		m.showHelp = true
		m.helpOffset = 0
		return nil, true
	case "esc":
		switch {
		case m.filter.Text != "":
			m.clearFilter()
		case m.paneFocus:
			m.paneFocus = false
		}
		m.err = nil
		m.status = ""
		return m.onSelectionChanged(), true
	}
	return nil, false
}

// navigate moves the cursor in whichever surface has focus.
func (m *Model) navigate(key string) (tea.Cmd, bool) {
	switch key {
	case "up", "k":
		if m.paneFocus {
			if m.paneList.Cursor() > 0 {
				m.paneList.Move(-1)
			}
			return nil, true
		}
		if m.cursor(m.focus) > 0 {
			m.lists[m.focus].Move(-1)
		}
		return m.onSelectionChanged(), true

	case "down", "j":
		if m.paneFocus {
			if m.paneList.Cursor() < m.paneRowCount()-1 {
				m.paneList.Move(1)
			}
			return nil, true
		}
		if m.cursor(m.focus) < m.panelLen(m.focus)-1 {
			m.lists[m.focus].Move(1)
		}
		return m.onSelectionChanged(), true

	case "left", "h":
		if m.paneFocus {
			m.paneFocus = false
			return nil, true
		}
		if m.focus > 0 {
			m.focus--
			m.clearFilter()
		}
		return m.onSelectionChanged(), true

	case "right", "l":
		m.paneFocus = true
		return nil, true

	case "g", "home":
		m.setCursor(0)
		return m.onSelectionChanged(), true

	case "G", "end":
		m.setCursor(m.panelLen(m.focus) - 1)
		return m.onSelectionChanged(), true

	case "J":
		if m.focus < panelCount-1 {
			m.focus++
			m.clearFilter()
		}
		return m.onSelectionChanged(), true

	case "K":
		if m.focus > 0 {
			m.focus--
			m.clearFilter()
		}
		return m.onSelectionChanged(), true
	}
	return nil, false
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

// clearFilter drops the filter and puts the cursor back at the top of what is
// left, which is one thing rather than the two places that used to forget the
// second half.
func (m *Model) clearFilter() {
	m.filtering = false
	m.filter.Text, m.filter.Cursor = "", 0
	m.lists[m.focus].Reset()
}

// onSelectionChanged loads whatever the new selection needs. Selection is
// hierarchical, so moving the connection cursor changes what every panel below
// it is about.
//
// It used to clamp every cursor first, from when they were plain ints that
// could point past a list that had shrunk under them. comp.List owns that now
// and does it at DRAW time, which is the only moment it knows what rows exist.
// Clamping here was worse than redundant: it went through Select, and Select
// cancels a pending Move — so every arrow key was applied and then immediately
// undone, and the cursor never left the first row.
func (m *Model) onSelectionChanged() tea.Cmd {
	// Moving to a connection is what asks pgctl to reach it. Probing is lazy —
	// see probeSelected — so this is where all but the first one happen.
	return tea.Batch(m.probeSelected(), m.paneLoad())
}

// filterKey handles typing in the / filter.
//
// The filter narrows only the focused panel. Filtering all of them from one box
// would empty the panels above and below the one being searched, which reads as
// data loss rather than as a filter.
func (m *Model) filterKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		m.clearFilter()
		return m.onSelectionChanged(), true
	case "enter":
		// The filter stays applied and stops taking keys, so the next j moves
		// the cursor through what it matched.
		m.filtering = false
		return m.onSelectionChanged(), true
	}
	// app.EditAt is the line editor every one of these tools wrote by hand:
	// backspace, delete, the arrows, home and end, ctrl+u. Keeping the caret
	// here rather than in a component's private state is what lets comp.Input
	// draw the same position the edits are applied at.
	if text, cursor, ok := app.EditAt(m.filter.Text, m.filter.Cursor, msg); ok {
		m.filter.Text, m.filter.Cursor = text, cursor
		// The cursor goes to the top of what is left: keeping its index would
		// leave it pointing at a different row than it was on.
		m.lists[m.focus].Reset()
		return m.onSelectionChanged(), true
	}
	return nil, true
}

// helpKey scrolls the key list, and anything else closes it.
//
// Which keeps the "press any key" feel for the common case where it all fits,
// and still reaches the last section on a 24-line terminal.
func (m *Model) helpKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "up", "k":
		m.helpOffset = max(m.helpOffset-1, 0)
	case "down", "j":
		m.helpOffset++
	case "g", "home":
		m.helpOffset = 0
	default:
		m.showHelp, m.helpOffset = false, 0
	}
	return nil, true
}

// leavingKey answers the question in front of q.
func (m *Model) leavingKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "y", "enter":
		m.leaving = false
		return m.quit(), true
	default:
		m.leaving = false
		return nil, true
	}
}

// quit cancels a running operation on the way out, so the engine's onFailure
// and postApply hooks run: they are what bring an environment back up, and they
// are the reason leaving is not simply exiting.
func (m *Model) quit() tea.Cmd {
	if m.active != nil && m.active.running {
		m.active.cancel()
	}
	return tea.Quit
}

// Open builds the model against a config, without showing it.
//
// Separate from Run because the caller may want to CAPTURE it instead — see the
// browse command's --snapshot. The model is the same either way, which is the
// point: a captured frame is the interface, not a rendering of it.
func Open(configPath string) (*Model, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	m := New(engine.New(cfg))
	for _, w := range cfg.Warnings {
		m.status = w
	}
	return m, nil
}

// LoadNow reads the world synchronously, for a capture.
//
// A capture never runs a tea.Cmd — that is what makes it deterministic — so a
// model that loads in Init captures the screen from before its data arrived,
// and nothing says so: `pgctl browse --snapshot` produced a frame with a header
// and four empty panels, which is neither documentation nor something an agent
// can read.
//
// It runs exactly what Init asks for, through the same messages, so there is no
// second copy of the loading to keep in step — and exactly what Init asks for
// means the SELECTED connection and no other. A capture that probed all of them
// would open a session to production because somebody asked for a screenshot,
// which is the bug lazy probing exists to prevent.
//
// Blocking, and bounded by the same timeouts the commands carry.
func (m *Model) LoadNow() {
	for _, cmd := range []tea.Cmd{m.loadSnapshots(), m.probeSelected()} {
		if cmd == nil {
			continue
		}
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
	// The pane's own load, which depends on what the probe just found: a
	// database is only selectable once the connection has answered.
	if cmd := m.paneLoad(); cmd != nil {
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
}

// Run shows the interface.
func Run(model *Model) error {
	// The runner owns the canvas, its size and its chrome — it is the only
	// place comp.NewCanvas is called, because those are decisions with one
	// right answer per program and a tool that made them itself would make them
	// differently in each of its screens.
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
