package tui

import (
	"fmt"

	"github.com/richarddavenport/tuikit/comp"
)

// Layout: a left column of panels, the rest to the detail pane.
const (
	// leftWidth is the narrowest the panel column may be dragged, and the
	// width it takes on a screen too small to divide. It is measured from the
	// widest thing that has to stay readable there — a snapshot timestamp with
	// its size and its location marker.
	leftWidth = 32

	// minPaneWide is the detail pane's floor. Below leftWidth+minPaneWide there
	// is no useful division to make, so the pane gets the screen.
	minPaneWide = 40
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

	// A refusal is a sentence, not a word, and the header has room for neither
	// it nor the config path it would push off the row. So it goes in a toast:
	// bounded, wrapped, in the corner, with the key that dismisses it.
	m.drawError(c, bands[1])

	// A modal DOES sit over the frame, because what it is about to do is about
	// what is behind it — the snapshot named in the title is the one selected
	// in the panel underneath.
	switch {
	case m.action != nil:
		m.drawAction(c, r)
	case m.showCommand:
		m.drawCommands(c, r)
	case m.leaving:
		m.drawLeaving(c)
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

// drawHeader is the tool's name, the config it read, and what just happened.
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
	if m.status != "" {
		right = []comp.Segment{{Text: "✓ " + m.status + " ", Style: &okStyle}}
	}

	// MinLeft protects the name and the config path from being squeezed to
	// nothing by a long status. The status is what just happened; the path is
	// what pgctl is pointed at, and an operator about to apply to an
	// environment wants to be sure of that one.
	comp.Bar{Left: left, Right: right, MinLeft: 24}.Draw(c, r, comp.Region(regHeader))
}

// drawError puts whatever went wrong in a corner of the body.
//
// A toast rather than a line in the header, and the reason is what pgctl's
// errors ARE: a refusal names the tables a selection reaches into, or the
// extension a target cannot install. The header had one row and shared it with
// the config path, so every refusal arrived truncated — the one message in the
// tool most worth reading in full.
func (m *Model) drawError(c *comp.Canvas, r comp.Rect) {
	if m.err == nil {
		return
	}
	comp.Toast{
		Title:  "refused",
		Body:   m.err.Error(),
		Hint:   "esc dismiss",
		Margin: 1,
		Accent: &dangerStyle,
		Border: &panelBorder,
		// No BodyStyle: the message is ordinary text, and ordinary text is
		// what the terminal draws without being told.
		HintStyle: &mutedStyle,
	}.Draw(c, r, comp.Region(regToast))
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
		focused := m.panelFocused(panel)

		pane := comp.Pane{
			Title:      m.panelTitle(panel),
			Focused:    focused,
			Border:     &panelBorder,
			Focus:      &panelFocusBorder,
			TitleStyle: &headerStyle,
			FocusTitle: &titleStyle,
		}
		// Narrow, not Inset: a column off each side and none off the top, so
		// the rows sit a space inside the border, and so the first row is not
		// spent on a blank.
		inside := pane.Draw(c, band, comp.Region(panelRegions[panel])).Narrow(1)

		// The empty state is the list's, not a line drawn beside it. Set here
		// because it is not constant: a database panel with nothing in it says
		// "unreachable", "probing…" or "none" depending on what pgctl has been
		// able to find out, and all three are ordinary states rather than
		// errors.
		m.lists[panel].Empty = m.emptyPanel(panel)
		m.lists[panel].Focused = focused
		m.lists[panel].Draw(c, inside, m.panelRows(panel))
	}
}

// panelFocused is whether a panel has the keys — which it does not while a
// modal is open, however it looked a moment ago.
func (m *Model) panelFocused(panel int) bool {
	return m.focus == panel && !m.paneFocus && m.action == nil && !m.showCommand
}

// panelBands divides the column between the panels.
func (m *Model) panelBands(r comp.Rect) []comp.Rect {
	cs := make([]comp.Constraint, panelCount)
	for panel := range cs {
		// The two borders, plus the rows the list spends on itself — which is
		// none now that NoStatus is set, and is asked for rather than assumed.
		// This was `const chrome = 3`, a number that goes silently wrong the
		// moment the answer changes.
		chrome := 2 + m.lists[panel].StatusRows()
		want := max(m.panelLen(panel), 1) + chrome
		// A title and one row is the least a panel can usefully be.
		cs[panel] = comp.Fill(want).Min(1 + chrome).Max(want)
	}
	return comp.Layout{Constraints: cs}.Rows(r)
}

// panelTitle is the panel's key, its name, its count and its filter.
//
// The number is BRACKETED because it is a key rather than a quantity. Bare, it
// sat beside a count in parentheses — "1 Connections (4)" — and read as two
// numbers about the panel, one of which is not about the panel at all. `[1]`
// says press this.
//
// The filter appears here as text and nowhere as a caret. Where the typing
// lands is shown once, by the comp.Input in the footer — the version that drew
// its own ▏ in the title had the cursor in two places and could only ever
// append, because a caret you cannot move is a typo you correct by deleting
// back to it.
func (m *Model) panelTitle(panel int) string {
	title := fmt.Sprintf("[%d] %s", panel+1, panelTitles[panel])
	if n := m.panelItems(panel); n > 0 {
		title += fmt.Sprintf(" (%d)", n)
	}
	if m.panelFocused(panel) && m.filter.Text != "" {
		title += " /" + m.filter.Text
	}
	return title
}

// drawFooter is one row: the filter being typed, or the keys that act on what
// is focused.
func (m *Model) drawFooter(c *comp.Canvas, r comp.Rect) {
	id := comp.Region(regFooter)

	// A modal carries its own keys, on its own bottom row. Repeating them down
	// here would put the same answer in two places and make the reader choose
	// which one to trust.
	if m.action != nil || m.showCommand {
		return
	}

	if m.filtering {
		// The input takes the left of the row and the two keys that end it
		// take the right, so the caret is never pushed off by the hints.
		bands := comp.Layout{Constraints: []comp.Constraint{
			comp.Fill(1), comp.Length(comp.Width(filterHints) + 1),
		}}.Cols(r)
		m.filter.Focused = true
		m.filter.Draw(c, bands[0], comp.Region(regFilter))
		comp.Bar{Right: []comp.Segment{{Text: filterHints, Style: &footerStyle}}}.
			Draw(c, bands[1], id)
		return
	}

	comp.Bar{Left: []comp.Segment{
		{Text: fitHints(m.hints(), r.W), Style: &footerStyle},
	}}.Draw(c, r, id)
}

// filterHints is what ends a filter, and it is a constant because comp.Hints
// joins with the chrome's separator: building it per frame would measure the
// same string every draw to lay out the row it sits on.
var filterHints = comp.Hints(
	comp.Hint{Key: "enter", Label: "keep"},
	comp.Hint{Key: "esc", Label: "clear"},
)

func (m *Model) emptyPanel(panel int) string {
	switch panel {
	case panelConnections:
		return "none declared"
	case panelDatabases:
		if conn, ok := m.selectedConn(); ok {
			if p := m.probes[conn.Name]; p != nil && !p.Reachable {
				return "unreachable"
			}
			if m.probing[conn.Name] {
				return "probing…"
			}
		}
		return "none"
	case panelSnapshots:
		// Named, because "none" on a panel whose contents depend on the
		// selection above it is ambiguous between "this connection has none"
		// and "pgctl has not looked".
		if conn, ok := m.selectedConn(); ok {
			return "none on " + conn.Name + " — press n"
		}
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
// Not every action pgctl has: a footer listing every action on every panel
// grows a letter per feature and reads as a menu of things mostly not
// applicable. ctrl+p is where the whole list lives.
func (m *Model) hints() []comp.Hint {
	if m.active != nil {
		return []comp.Hint{
			{Key: "q", Label: "stop and leave"},
			{Key: "]", Label: "log"},
			{Key: "?", Label: "keys"},
		}
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
		comp.Hint{Key: "[ ]", Label: "tabs"},
		comp.Hint{Key: "ctrl+p", Label: "commands"},
		comp.Hint{Key: "?", Label: "keys"},
	)
}

// fitHints joins key hints into one line no wider than the terminal.
//
// comp.Hint rather than pre-formatted strings, and comp.Hints to join them, so
// the separator lives in the chrome once instead of in every footer string —
// and so this list is the same type the command directory is built from.
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
