package tui

import (
	tea "github.com/charmbracelet/bubbletea"

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
	content := m.paneBody(active, body.W)
	if content.detail != nil {
		content.detail.Draw(c, body, comp.Region(regBody))
		return
	}
	lines := content.lines
	m.paneList.Focused = m.paneFocus
	m.paneList.DrawFunc(c, body, len(lines), func(i int) comp.Row {
		return lines[i]
	})
}

// paneRowCount is how many lines the pane's body has, for cursor clamping.
func (m *Model) paneRowCount() int {
	tabs := m.paneTabs()
	active := clamp(m.tabs[m.focus], len(tabs)-1)
	// A detail pane does not scroll, so it has no rows to count: it is laid
	// out to fit and clipped if it does not.
	return len(m.paneBody(active, m.screenWidth()-leftWidth-5).lines)
}

// paneContent is what a tab has to say, in one of the two shapes a tab comes in.
//
// The distinction is real rather than a migration artefact. Overview, Config
// and Manifest are a dozen facts and a note: they fit, and comp.Detail lays
// them out — including the label column, which democtl and azctl both padded to
// an arbitrary number and azctl's overflowed. Tables, Databases and Foreign
// keys are 213 rows of a real manifest: they do not fit, and the thing they
// need is a viewport.
//
// So a tab returns facts OR lines, and drawPane draws whichever it got. A
// component that did both would have to decide, on the tool's behalf, when a
// detail pane becomes a list.
type paneContent struct {
	// detail is drawn into the rect and clipped. Short by construction.
	detail *comp.Detail
	// lines scroll through comp.List. Long by construction.
	lines []comp.Row
}

func facts(d comp.Detail) paneContent { return paneContent{detail: &d} }

// paneBody dispatches to the renderer for the focused panel and tab.
func (m *Model) paneBody(tab, width int) paneContent {
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
	return paneContent{}
}

// table renders aligned columns, truncating the widest flexible column rather
// than wrapping — a wrapped row in a list of 213 is unreadable.
// tableRows lays out aligned columns as rows a comp.List can scroll.
//
// comp.Table does the widths, including the flexible column — which pgctl was
// computing itself with a loop that summed every OTHER column and subtracted,
// and a floor of 8 to stop it going negative. A Column with Fill says the same
// thing declaratively, and at most one column may have it: "a table with two
// greedy columns has no answer and silently picking one hides the mistake."
//
// Rows rather than a string, because comp.Table strings out rather than drawing
// so the result can be scrolled — which is the whole reason the table tabs are
// lines and not a comp.Detail. A 213-table manifest is the normal case here.
//
// Numbers are right-aligned, which pgctl never did: a column of sizes that ends
// ragged is a column you cannot compare down.
func tableRows(width int, headers []string, cols []comp.Column, rows [][]string) []comp.Row {
	t := comp.Table{Columns: cols, Gap: 1}
	lines := t.Rows(width, append([][]string{headers}, rows...))

	out := make([]comp.Row, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, comp.Row{Text: line, Style: &headerStyle})
			continue
		}
		out = append(out, comp.Row{Text: line})
	}
	return out
}

// dataMode renders a table's rule for a listing.
// dataMode is what a rule does to a table's data, as PLAIN text.
//
// Plain because it is a table cell, and comp.Table takes plain strings: it
// measures them ANSI-aware, so a styled cell lays out correctly and then hands
// escape sequences to a canvas that draws clusters into cells — which drops the
// cell entirely. TestColourDoesNotChangeTheShape caught exactly that here, on
// the last column of the Tables tab, for the second time this migration.
//
// The colour is not lost so much as not yet expressible: comp.Table returns
// whole lines, so a per-cell style has nowhere to live. The words carry it —
// "none" and "filtered" say what amber was saying.
func dataMode(rule config.Rule) string {
	switch rule.Data {
	case config.DataNone:
		return "none"
	case config.DataFiltered:
		return "filtered"
	default:
		return "all"
	}
}
