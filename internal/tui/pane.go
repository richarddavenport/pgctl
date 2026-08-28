package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/richarddavenport/pgctl/internal/config"
)

// The detail pane's tabs, per focused panel. The tabs are a property of what is
// selected, not of the pane, so moving between panels changes the questions the
// pane can answer.
func (m *Model) paneTabs() []string {
	switch m.focus {
	case panelEnvironments:
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
	env, hasEnv := m.selectedEnv()
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

// pane renders the detail panel.
func (m *Model) pane(width, height int) string {
	tabs := m.paneTabs()
	active := clamp(m.tabs[m.focus], len(tabs)-1)

	var bar strings.Builder
	for i, t := range tabs {
		if i == active {
			bar.WriteString(activeTabStyle.Render(t))
		} else {
			bar.WriteString(tabStyle.Render(t))
		}
	}

	// As in panel(): the border takes two columns and the padding two more.
	inner := width - 4
	body := m.paneBody(active, inner)

	// The pane scrolls rather than truncating: a manifest of 213 tables is the
	// normal case, not an edge one.
	rows := strings.Split(body, "\n")
	visible := height - 4
	if visible < 1 {
		visible = 1
	}
	shown, offset := window(len(rows)+1, visible+1, m.paneCursor, m.paneOffset)
	m.paneOffset = offset

	var b strings.Builder
	for i := 0; i < shown && offset+i < len(rows); i++ {
		line := rows[offset+i]
		if m.paneFocus && offset+i == m.paneCursor {
			line = currentStyle.Render(truncateHard(line, inner))
		}
		b.WriteString(line)
		if i < shown-1 {
			b.WriteString("\n")
		}
	}
	if len(rows) > shown {
		b.WriteString("\n" + mutedStyle.Render(fmt.Sprintf("  %d more — tab to focus, j/k to scroll",
			len(rows)-shown-offset)))
	}

	style := panelStyle
	if m.paneFocus {
		style = focusedPanelStyle
	}
	return style.Width(width - 2).Height(height).Render(bar.String() + "\n\n" + b.String())
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
	case panelEnvironments:
		return m.viewEnvironmentTab(tab, width)
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
