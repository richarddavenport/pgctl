package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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

	var need [panelCount]int
	total := 0
	for panel := 0; panel < panelCount; panel++ {
		need[panel] = max(m.panelLen(panel)+1, minRows)
		total += need[panel]
	}

	if total <= rows {
		// Everything fits. Any slack is left at the bottom of the column
		// rather than inflating a panel that has nothing to put in it.
		return need
	}

	var out [panelCount]int
	spare := rows
	for panel := 0; panel < panelCount; panel++ {
		out[panel] = minRows
		spare -= minRows
	}
	if spare <= 0 {
		return out
	}

	// Proportional share of what each panel still wants.
	wanted := 0
	for panel := 0; panel < panelCount; panel++ {
		wanted += need[panel] - minRows
	}
	used := 0
	for panel := 0; panel < panelCount && wanted > 0; panel++ {
		extra := (need[panel] - minRows) * spare / wanted
		out[panel] += extra
		used += extra
	}
	if left := spare - used; left > 0 {
		out[m.focus] += left
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

	keys := []string{"n snapshot", "a apply", "m move", "p prune"}
	if m.focus == panelSnapshots {
		keys = append(keys, "x delete")
	}
	if m.active != nil {
		keys = []string{"q cancel the run"}
	}
	keys = append(keys, "/ filter", "tab pane", "? keys")
	return footerStyle.Render(strings.Join(keys, "  ·  "))
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

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	// Cut by runes, leaving room for the ellipsis.
	runes := []rune(s)
	if width == 1 {
		return "…"
	}
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// clip returns the first width columns of a rendered line, ANSI intact enough
// for an overlay's purposes.
func clip(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return truncateHard(s, width)
}

func truncateHard(s string, width int) string {
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

func padTo(s string, width int) string {
	if w := lipgloss.Width(s); w < width {
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
