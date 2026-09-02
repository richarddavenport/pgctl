package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/richarddavenport/tuikit/comp"

	"github.com/richarddavenport/pgctl/internal/config"
)

// The detail pane's tabs, per focused panel. The tabs are a property of what is
// selected, not of the pane, so moving between panels changes the questions the
// pane can answer.
func (m *Model) paneTabs() []string {
	switch m.focus {
	case panelConnections:
		return []string{"Overview", "Databases", "Config"}
	case panelDatabases:
		return []string{"Tables", "Rules", "Foreign keys"}
	case panelSnapshots:
		return []string{"Manifest", "Tables", "Warnings", "Drift"}
	case panelSets:
		return []string{"Members", "Closure", "Load order"}
	case panelRuns:
		return []string{"Log"}
	}
	return []string{"Overview"}
}

// paneLoad fetches whatever the current tab needs and does not have.
//
// Called on every selection or tab change, and cheap when the data is already
// cached: the alternative is a pane that shows nothing until the operator
// works out which key fetches it.
func (m *Model) paneLoad() tea.Cmd {
	env, hasEnv := m.selectedConn()
	db, hasDB := m.selectedDatabase()
	if !hasEnv || !hasDB {
		return nil
	}

	switch m.focus {
	case panelDatabases:
		if m.liveTable[liveKey(env.Name, db.Name)] == nil && m.liveErr[liveKey(env.Name, db.Name)] == nil {
			return m.loadLiveTables(env.Name, db.Name)
		}
	case panelSets:
		if set, ok := m.selectedSet(); ok {
			return m.loadSetMembers(env.Name, db.Name, set.Name)
		}
	}
	return nil
}

// drawPane renders the detail panel: a tab strip, then the body of whichever
// tab is selected.
func (m *Model) drawPane(c *comp.Canvas, r comp.Rect) {
	pane := comp.Pane{
		Focused:    m.paneFocus,
		Border:     &panelBorder,
		Focus:      &panelFocusBorder,
		TitleStyle: &headerStyle,
	}
	inside := pane.Draw(c, r, comp.Region(regPane))
	if inside.Empty() {
		return
	}

	tabs := m.paneTabs()
	active := clamp(m.tabs[m.focus], len(tabs)-1)
	entries := make([]comp.Tab, len(tabs))
	for i, t := range tabs {
		entries[i] = comp.Tab{Name: t}
	}
	// The chevrons round the strip say that it CYCLES and that tab is the key,
	// which is a fact about the keymap rather than about the state — and the
	// active tab is already styled, so chevrons around IT would only repeat
	// what the colour says.
	comp.Tabs{
		Tabs:          entries,
		Active:        active,
		Focused:       m.paneFocus,
		Style:         &tabStyle,
		Selected:      &activeTabStyle,
		FocusSelected: &activeTabStyle,
		Chrome:        &mutedStyle,
	}.Draw(c, inside.Narrow(1), regTabs)

	// A blank row between the strip and the body, so the tabs read as chrome
	// rather than as the first line of what they label.
	body := comp.Rect{X: inside.X, Y: inside.Y + 2, W: inside.W, H: inside.H - 2}.Narrow(1)
	if body.H < 1 {
		return
	}

	// The pane scrolls rather than truncating: a manifest of 213 tables is the
	// normal case, not an edge one. comp.List does the scrolling, keeps the
	// cursor in view when it moves, and reports the position — all of which
	// was hand-written arithmetic sharing a window() helper with the panels.
	lines := strings.Split(m.paneBody(active, body.W), "\n")
	m.paneList.Focused = m.paneFocus
	m.paneList.DrawFunc(c, body, len(lines), func(i int) comp.Row {
		// ansi.Strip is SCAFFOLDING, and the only reason the detail pane draws
		// in one colour right now.
		//
		// The canvas draws clusters into cells, so an escape sequence handed to
		// it is text, not styling — the first version of this port passed
		// detail.go's styled strings straight through and the pane rendered
		// blank rows. The real fix is for the tab renderers to emit
		// []comp.Row with spans, the way rows.go now does, which is 528 lines
		// of detail.go and its own commit.
		//
		// Stripping rather than passing through keeps the SHAPE exactly right
		// — Strip does not change any width — so the layout, the scrolling and
		// the goldens are all honest. What is missing is colour, and
		// TestTheDetailPaneHasNoColourYet says so out loud, because the
		// goldens are colour-stripped and could never notice.
		return comp.Row{Text: ansi.Strip(lines[i])}
	})
}

// paneRowCount is how many lines the pane's body has, for cursor clamping.
func (m *Model) paneRowCount() int {
	tabs := m.paneTabs()
	active := clamp(m.tabs[m.focus], len(tabs)-1)
	return len(strings.Split(m.paneBody(active, m.screenWidth()-leftWidth-5), "\n"))
}

// paneBody dispatches to the renderer for the focused panel and tab.
func (m *Model) paneBody(tab, width int) string {
	switch m.focus {
	case panelConnections:
		return m.viewConnectionTab(tab, width)
	case panelDatabases:
		return m.viewDatabaseTab(tab, width)
	case panelSnapshots:
		return m.viewSnapshotTab(tab, width)
	case panelSets:
		return m.viewSetTab(tab, width)
	case panelRuns:
		return m.viewRunTab(width)
	}
	return ""
}

// field renders a label and value pair, aligned so a column of them reads as a
// table rather than as prose.
func field(label, value string) string {
	return fmt.Sprintf("%s %s", mutedStyle.Render(fmt.Sprintf("%-14s", label)), value)
}

// section is a heading inside a pane body.
func section(title string) string {
	return "\n" + headerStyle.Render(strings.ToUpper(title)) + "\n"
}

// table renders aligned columns, truncating the widest flexible column rather
// than wrapping — a wrapped row in a list of 213 is unreadable.
func renderTable(width int, headers []string, widths []int, rows [][]string) string {
	var b strings.Builder

	line := func(cells []string, style lipgloss.Style) string {
		var parts []string
		for i, cell := range cells {
			if i >= len(widths) {
				break
			}
			w := widths[i]
			if w == 0 {
				// The flexible column takes what is left.
				used := 0
				for j, ww := range widths {
					if j != i {
						used += ww + 1
					}
				}
				w = width - used
				if w < 8 {
					w = 8
				}
			}
			parts = append(parts, padTo(truncate(cell, w), w))
		}
		return style.Render(strings.Join(parts, " "))
	}

	b.WriteString(line(headers, headerStyle))
	for _, row := range rows {
		b.WriteString("\n" + line(row, lipgloss.NewStyle()))
	}
	return b.String()
}

// dataMode renders a table's rule for a listing.
func dataMode(rule config.Rule) string {
	switch rule.Data {
	case config.DataNone:
		return warnStyle.Render("none")
	case config.DataFiltered:
		return warnStyle.Render("filtered")
	default:
		return mutedStyle.Render("all")
	}
}
