package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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

// View renders the whole screen.
func (m *Model) View() string {
	// A size of zero means no WindowSizeMsg has arrived. A real terminal sends
	// one immediately, but a pty with no size attached never does, and a UI
	// that waits forever for it is a UI that renders nothing at all under
	// `script`, in CI, or over a connection that lost its window size.
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	if m.showHelp {
		return m.viewHelp()
	}

	header := m.header()
	footer := m.footer()
	bodyHeight := height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	left := m.leftColumn(bodyHeight)
	paneWidth := width - leftWidth - 1
	if paneWidth < minPaneWide {
		// A narrow terminal gets the pane alone: two half-width columns are
		// worse than one usable one.
		body := m.pane(width, bodyHeight)
		return strings.Join([]string{header, body, footer}, "\n")
	}
	right := m.pane(paneWidth, bodyHeight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)

	screen := strings.Join([]string{header, body, footer}, "\n")
	if m.action != nil {
		return m.overlay(screen, m.viewAction())
	}
	return screen
}

// screenWidth is the width to lay out against, defaulting when no size has
// arrived. See View.
func (m *Model) screenWidth() int {
	if m.width <= 0 {
		return 80
	}
	return m.width
}

func (m *Model) header() string {
	width := m.screenWidth()

	// The name, then whatever room is left for the config path and a message.
	// An absolute path to a config in a deep directory is longer than most
	// terminals are wide, and an untruncated header pushed the whole frame
	// sideways.
	const name = "pgctl"
	line := titleStyle.Render(name)
	remaining := width - len(name)

	source := m.cfg.Source
	if source == "" {
		source = "no config"
	}
	var message, style = "", mutedStyle
	switch {
	case m.err != nil:
		message, style = "✗ "+m.err.Error(), dangerStyle
	case m.status != "":
		message, style = "✓ "+m.status, okStyle
	}

	// A message earns its space first: it is the thing that just happened.
	if message != "" {
		shown := truncate(message, remaining-4)
		remaining -= lipgloss.Width(shown) + 3
		if remaining > 6 {
			line += mutedStyle.Render("  " + truncate(source, remaining-2))
		}
		return line + "   " + style.Render(shown)
	}
	return line + mutedStyle.Render("  "+truncate(source, remaining-2))
}

// leftColumn stacks the panels, giving each a share of the height weighted by
// how much it has to show.
func (m *Model) leftColumn(height int) string {
	// Two lines of chrome per panel (border top and bottom), so the rows
	// available are what is left after that.
	rows := height - 2*panelCount
	if rows < panelCount {
		rows = panelCount
	}

	heights := m.panelHeights(rows)
	blocks := make([]string, 0, panelCount)
	for panel := 0; panel < panelCount; panel++ {
		blocks = append(blocks, m.panel(panel, heights[panel]))
	}
	return lipgloss.JoinVertical(lipgloss.Left, blocks...)
}

// panelHeights divides the available rows between the panels.
//
// A panel asks for exactly what it has to show and is never stretched beyond
// it: a list of three environments given twenty-six rows wastes the space the
// snapshot list needed, and the empty rows read as a panel that failed to load.
// When the panels want more than there is, everyone keeps a minimum and the
// rest is shared out in proportion to what they asked for, with the focused
// panel — the one being read — taking any rounding.
func (m *Model) panelHeights(rows int) [panelCount]int {
	const minRows = 2 // a title and one row

	// comp.Layout, which is this arithmetic as a constraint solve. The three
	// rules pgctl wanted map onto it exactly, and reading them off the
	// constraints is the point — the version this replaces spelled them out as
	// twenty lines of proportional division, and the rule each line implemented
	// was only in the comment above it:
	//
	//	Fill(want)  a squeezed panel's share is proportional to what it asked for
	//	Min(2)      everyone keeps a title and one row
	//	Max(want)   a panel is never stretched past what it has to show, so
	//	            slack is left at the bottom of the column rather than
	//	            inflating a panel that has nothing to put in it
	cs := make([]comp.Constraint, panelCount)
	for panel := 0; panel < panelCount; panel++ {
		want := max(m.panelLen(panel)+1, minRows)
		cs[panel] = comp.Fill(want).Min(minRows).Max(want)
	}

	var out [panelCount]int
	for i, band := range (comp.Layout{Constraints: cs}).Rows(comp.Rect{W: 1, H: rows}) {
		out[i] = band.H
	}
	return out
}

// panel renders one left-column panel.
func (m *Model) panel(panel, height int) string {
	focused := m.focus == panel && !m.paneFocus && m.action == nil

	title := fmt.Sprintf("%d %s", panel+1, panelTitles[panel])
	if n := m.panelLen(panel); n > 0 {
		title += mutedStyle.Render(fmt.Sprintf(" (%d)", n))
	}
	if focused && m.filtering {
		title = fmt.Sprintf("%d /%s", panel+1, m.filter)
	} else if focused && m.filter != "" {
		title += mutedStyle.Render(" /" + m.filter)
	}

	rows := m.panelRows(panel)
	inner := panelInner
	visible, offset := window(len(rows), height, m.cursors[panel], m.offsets[panel])
	m.offsets[panel] = offset

	var b strings.Builder
	for i := 0; i < visible; i++ {
		idx := offset + i
		if idx >= len(rows) {
			break
		}
		line := truncate(rows[idx], inner)
		if idx == m.cursors[panel] && focused {
			line = selectedStyle.Width(inner).Render(line)
		} else if idx == m.cursors[panel] {
			line = currentStyle.Render(line)
		}
		b.WriteString(line)
		if i < visible-1 {
			b.WriteString("\n")
		}
	}
	if len(rows) == 0 {
		b.WriteString(mutedStyle.Render(m.emptyPanel(panel)))
	}

	style := panelStyle
	if focused {
		style = focusedPanelStyle
	}
	return style.Width(panelBlock).Height(height).Render(
		headerStyle.Render(title) + "\n" + b.String())
}

// window works out which slice of a list is visible and keeps the cursor in it.
func window(count, height, cursor, offset int) (visible, newOffset int) {
	// One row of the panel is its title.
	visible = height - 1
	if visible < 1 {
		visible = 1
	}
	if count <= visible {
		return count, 0
	}
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+visible {
		offset = cursor - visible + 1
	}
	if offset > count-visible {
		offset = count - visible
	}
	if offset < 0 {
		offset = 0
	}
	return visible, offset
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

func (m *Model) footer() string {
	if m.filtering {
		return footerStyle.Render("filter: " + m.filter + "▏   enter accept · esc clear")
	}
	if m.action != nil {
		return footerStyle.Render(m.actionFooter())
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
	if m.active != nil {
		hints = []comp.Hint{{Key: "q", Label: "cancel the run"}}
	}
	hints = append(hints,
		comp.Hint{Key: "/", Label: "filter"},
		comp.Hint{Key: "tab", Label: "pane"},
		comp.Hint{Key: "?", Label: "keys"},
	)
	return footerStyle.Render(fitHints(hints, m.screenWidth()))
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

// overlay centres a box over the screen, which is how a modal appears without
// the panels behind it being torn down and rebuilt.
func (m *Model) overlay(screen, box string) string {
	lines := strings.Split(screen, "\n")
	boxLines := strings.Split(box, "\n")

	top := (len(lines) - len(boxLines)) / 2
	if top < 0 {
		top = 0
	}
	boxWidth := 0
	for _, l := range boxLines {
		boxWidth = max(boxWidth, lipgloss.Width(l))
	}
	left := (m.screenWidth() - boxWidth) / 2
	if left < 0 {
		left = 0
	}

	for i, bl := range boxLines {
		row := top + i
		if row >= len(lines) {
			break
		}
		lines[row] = padTo(clip(lines[row], left), left) + bl
	}
	return strings.Join(lines, "\n")
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
