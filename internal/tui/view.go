package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/richarddavenport/tuikit/comp"
)

// Layout: a fixed-width left column of panels, the rest to the detail pane.
// The column is wide enough for a snapshot timestamp and its location marker,
// which is the widest thing that has to stay readable.
const (
	leftWidth   = 32
	minPaneWide = 40

	// panelBlock is what lipgloss is told the panel is: leftWidth less the two
	// columns its rounded border takes.
	panelBlock = leftWidth - 2

	// panelInner is what a row actually gets. lipgloss's Width includes
	// padding, so the row loses the two columns of it as well. Getting this
	// wrong by two wraps every row, which is how the first version rendered a
	// database list.
	panelInner = panelBlock - 2
)

// Draw renders the whole screen.
//
// Cells, not strings. What that buys pgctl is a mouse it never had — every row
// records which connection, database or snapshot drew it, so a click resolves
// to the thing rather than to a line number — and clipping that is structural:
// a component drawing past its rect is cut by the canvas rather than running
// off the side of the terminal, which is three of the four layout bugs the
// goldens found.
func (m *Model) Draw(c *comp.Canvas, r comp.Rect) {
	m.frame = r
	defer func() { m.canvas = c }()

	// The help overlay replaces the frame rather than sitting over it: it is a
	// reference you read, not a modal you act through, and the panels behind it
	// are not the question.
	if m.showHelp {
		m.drawHelp(c, r)
		return
	}

	bands := m.bands(r)
	m.drawHeader(c, bands[0])
	m.drawBody(c, bands[1])
	m.drawFooter(c, bands[2])

	// A modal DOES sit over the frame, because what it is about to do is about
	// what is behind it — the snapshot named in the title is the one selected
	// in the panel underneath.
	if m.action != nil {
		m.drawAction(c, r)
	}
}

// bands is pgctl's window: a header, the body, one row of key hints.
//
// One declaration rather than a rect and a height computed separately. The
// version this replaces subtracted the header and footer heights from the
// terminal in View and then recomputed the same thing in three other places;
// nothing related them, and no test could catch it going wrong because both
// numbers were equally plausible.
func (m *Model) bands(r comp.Rect) []comp.Rect {
	// A rect of zero means no WindowSizeMsg has arrived. A real terminal sends
	// one immediately, but a pty with no size attached never does, and a UI
	// that waits forever for it renders nothing at all under `script`.
	if r.W <= 0 {
		r.W = 80
	}
	if r.H <= 0 {
		r.H = 24
	}
	return comp.Layout{Constraints: []comp.Constraint{
		comp.Length(1),      // pgctl · the config it read
		comp.Fill(1).Min(3), // the panel column and the detail pane
		comp.Length(1),      // the key hints
	}}.Rows(r)
}

// body is the space the panes are drawn into.
func (m *Model) body() comp.Rect { return m.bands(m.bounds())[1] }

// bounds is the rect the last frame was drawn into, which is the canvas the
// runner made: one row shorter than the terminal, because that is what
// app.Runner leaves for the terminal itself.
//
// Before the first draw there is no frame, so it falls back to the size the
// model was told about. The bands have to agree with the canvas they are drawn
// into, or the key hints land on a row that does not exist and are silently
// clipped.
func (m *Model) bounds() comp.Rect {
	if !m.frame.Empty() {
		return m.frame
	}
	return comp.Rect{W: m.screenWidth(), H: max(1, m.screenHeight()-1)}
}

// screenWidth is the width to lay out against, defaulting when no size has
// arrived. See bands.
func (m *Model) screenWidth() int {
	if !m.frame.Empty() {
		return m.frame.W
	}
	if m.width <= 0 {
		return 80
	}
	return m.width
}

func (m *Model) screenHeight() int {
	if m.height <= 0 {
		return 24
	}
	return m.height
}

// drawHeader is the tool's name, the config it read, and whatever just happened.
func (m *Model) drawHeader(c *comp.Canvas, r comp.Rect) {
	source := m.cfg.Source
	if source == "" {
		source = "no config"
	}

	left := []comp.Segment{
		{Text: "pgctl", Style: &titleStyle},
		{Text: "  " + source, Style: &mutedStyle},
	}

	var right []comp.Segment
	switch {
	case m.err != nil:
		right = []comp.Segment{{Text: "✗ " + m.err.Error() + " ", Style: &dangerStyle}}
	case m.status != "":
		right = []comp.Segment{{Text: "✓ " + m.status + " ", Style: &okStyle}}
	}

	// MinLeft protects the name and the config path from being squeezed to
	// nothing by a long error message. The message is what just happened; the
	// path is what pgctl is pointed at, and an operator about to apply to an
	// environment wants to be sure of that one.
	comp.Bar{Left: left, Right: right, MinLeft: 24}.Draw(c, r, comp.Region(regHeader))
}

// drawBody splits the panel column from the detail pane.
func (m *Model) drawBody(c *comp.Canvas, r comp.Rect) {
	// A narrow terminal gets the pane alone: two half-width columns are worse
	// than one usable one. Checked against the whole width rather than the
	// split's answer, because below this there is no useful division to make.
	if r.W < leftWidth+minPaneWide {
		m.drawPane(c, r)
		return
	}
	left, right := m.split.Draw(c, r)
	m.drawPanels(c, left)
	m.drawPane(c, right)
}

// drawPanels stacks the five panels, each with a share of the height weighted
// by how much it has to show.
func (m *Model) drawPanels(c *comp.Canvas, r comp.Rect) {
	for panel, band := range m.panelBands(r) {
		if band.H < 3 {
			continue
		}
		focused := m.focus == panel && !m.paneFocus && m.action == nil

		pane := comp.Pane{
			Title:      m.panelTitle(panel),
			Focused:    focused,
			Border:     &panelBorder,
			Focus:      &panelFocusBorder,
			TitleStyle: &headerStyle,
			FocusTitle: &titleStyle,
		}
		// Narrow, not Inset: a column off each side and none off the top, so
		// the rows sit a space inside the border the way they did when the
		// panel was a lipgloss box with Padding(0, 1) — and so the first row
		// is not spent on a blank.
		inside := pane.Draw(c, band, comp.Region(panelRegions[panel])).Narrow(1)

		rows := m.panelRows(panel)
		if len(rows) == 0 {
			c.Text(inside.X, inside.Y, m.emptyPanel(panel), &mutedStyle,
				comp.Region(panelRegions[panel]))
			continue
		}

		m.lists[panel].Focused = focused
		m.lists[panel].Draw(c, inside, rows)
	}
}

// panelBands divides the column between the panels.
func (m *Model) panelBands(r comp.Rect) []comp.Rect {
	// Three rows of chrome per panel that a row cannot use: the two borders,
	// and the row comp.List keeps for its position counter.
	const chrome = 3
	// A title and one row is the least a panel can usefully be.
	const minBand = 1 + chrome

	cs := make([]comp.Constraint, panelCount)
	for panel := range cs {
		want := max(m.panelLen(panel), 1) + chrome
		cs[panel] = comp.Fill(want).Min(minBand).Max(want)
	}
	return comp.Layout{Constraints: cs}.Rows(r)
}

// panelTitle is the panel's number, its name, its count and its filter.
//
// The number is the key that jumps to it, which is the only reason it is on
// screen: a panel labelled "1 Connections" tells you how to get there without
// a legend.
func (m *Model) panelTitle(panel int) string {
	focused := m.focus == panel && !m.paneFocus && m.action == nil
	if focused && m.filtering {
		return fmt.Sprintf("%d /%s▏", panel+1, m.filter)
	}
	title := fmt.Sprintf("%d %s", panel+1, panelTitles[panel])
	if n := m.panelLen(panel); n > 0 {
		title += fmt.Sprintf(" (%d)", n)
	}
	if focused && m.filter != "" {
		title += " /" + m.filter
	}
	return title
}

// drawFooter is one row of key hints.
func (m *Model) drawFooter(c *comp.Canvas, r comp.Rect) {
	id := comp.Region(regFooter)
	if m.filtering {
		comp.Bar{Left: []comp.Segment{{
			Text:  "filter: " + m.filter + "▏" + comp.Hints(comp.Hint{Key: "enter", Label: "accept"}, comp.Hint{Key: "esc", Label: "clear"}),
			Style: &footerStyle,
		}}}.Draw(c, r, id)
		return
	}
	if m.action != nil {
		comp.Bar{Left: []comp.Segment{{Text: m.actionFooter(), Style: &footerStyle}}}.Draw(c, r, id)
		return
	}
	comp.Bar{Left: []comp.Segment{
		{Text: fitHints(m.hints(), r.W), Style: &footerStyle},
	}}.Draw(c, r, id)
}

func (m *Model) emptyPanel(panel int) string {
	switch panel {
	case panelConnections:
		return "none declared"
	case panelDatabases:
		if env, ok := m.selectedConn(); ok {
			if p := m.probes[env.Name]; p != nil && !p.Reachable {
				return "unreachable"
			}
			if m.probing[env.Name] {
				return "probing…"
			}
		}
		return "none"
	case panelSnapshots:
		return "none — press n"
	case panelSets:
		return "none declared"
	case panelRuns:
		return "nothing run yet"
	}
	return ""
}

// hints is what acts on what is focused, right now.
//
// Not every action pgctl has: swarmctl learned that the expensive way, where a
// footer listing every action on every panel grew a letter per feature and read
// as a menu of things mostly not applicable.
func (m *Model) hints() []comp.Hint {
	if m.active != nil {
		return []comp.Hint{{Key: "q", Label: "cancel the run"}}
	}
	hints := []comp.Hint{
		{Key: "n", Label: "snapshot"},
		{Key: "a", Label: "apply"},
		{Key: "m", Label: "move"},
		{Key: "p", Label: "prune"},
	}
	if m.focus == panelSnapshots {
		hints = append(hints, comp.Hint{Key: "x", Label: "delete"})
	}
	return append(hints,
		comp.Hint{Key: "/", Label: "filter"},
		comp.Hint{Key: "tab", Label: "pane"},
		comp.Hint{Key: "?", Label: "keys"},
	)
}

// fitHints joins key hints into one line no wider than the terminal.
//
// comp.Hint rather than pre-formatted strings, and comp.Hints to join them, so
// the separator lives in the glyph set once instead of in every footer string —
// and so this list is the same type a context menu is built from. tuikit's
// mouse notes make that the rule: the keyboard path and the pointer path to an
// action have to be ONE list, not a list and a keymap maintained beside it.
//
// The fitting is pgctl's own, because comp.Hints does not measure. The footer
// used to render whatever it had: eight hints at 95 columns, on the LAST line
// of the frame, which is the one place an overflow makes the terminal scroll
// and tear the whole screen rather than clip a row.
//
// Hints are dropped from the RIGHT, except the last, which is kept whatever
// else goes — it is "? keys", the hint that leads to all the others.
func fitHints(hints []comp.Hint, width int) string {
	if width <= 0 || len(hints) == 0 {
		return ""
	}
	last := hints[len(hints)-1]
	head := hints[:len(hints)-1]
	for n := len(head); n >= 0; n-- {
		line := comp.Hints(append(append([]comp.Hint{}, head[:n]...), last)...)
		if comp.Width(line) <= width {
			return line
		}
	}
	return comp.Truncate(comp.Hints(last), width)
}

// The text helpers are comp's now. They stay as functions here because they
// have thirty-odd call sites between them and because two of them differ from
// comp's in a way this package relies on.
//
// All three of the versions these replace cut by RUNES. That is wrong twice
// over: a rune is not a column, so a CJK name measured this way is half its
// real width; and a rendered line contains escape sequences, so cutting between
// runes can end a line in the middle of one and leave the rest of the frame
// wearing whatever colour it was setting. comp counts columns and is ANSI-aware.

// truncate shortens to width columns with an ellipsis when it had to cut.
func truncate(s string, width int) string { return comp.Truncate(s, width) }

// clip returns the first width columns, with no ellipsis.
//
// Not comp.Truncate: the overlay uses this to cut the screen behind a modal,
// and an ellipsis there would draw a "…" against the modal's left edge on every
// row, which reads as content rather than as a seam.
func clip(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "")
}

// padTo pads to width columns, and unlike comp.Pad leaves a longer string
// alone. Callers here pad columns into alignment and clip separately; a pad
// that silently truncated would hide the overflow rather than show it.
func padTo(s string, width int) string {
	if w := comp.Width(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
