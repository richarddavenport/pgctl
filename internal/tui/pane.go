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
		// No Databases tab. Panel 2 IS the list of this connection's databases,
		// with the same names and the same sizes, and the Overview tab already
		// counts them — so the tab was the same fact a third time, with a
		// cursor of its own. Two database lists on screen with two cursors, one
		// of which meant nothing, is what made "what am I snapshotting?"
		// unanswerable.
		return []string{"Overview", "Config"}
	case panelDatabases:
		return []string{"Tables", "Rules", "Foreign keys"}
	case panelSnapshots:
		return []string{"Manifest", "Tables", "Warnings", "Drift"}
	case panelSets:
		return []string{"Members", "Closure", "Load order"}
	case panelRuns:
		return []string{"Steps", "Log"}
	}
	return []string{"Overview"}
}

// paneLoad fetches whatever the current tab needs and does not have.
//
// Called on every selection or tab change, and cheap when the data is already
// cached: the alternative is a pane that shows nothing until the operator works
// out which key fetches it.
func (m *Model) paneLoad() tea.Cmd {
	conn, hasConn := m.selectedConn()
	db, hasDB := m.selectedDatabase()
	if !hasConn || !hasDB {
		return nil
	}

	switch m.focus {
	case panelDatabases:
		key := liveKey(conn.Name, db.Name)
		if m.liveTable[key] == nil && m.liveErr[key] == nil {
			return m.loadLiveTables(conn.Name, db.Name)
		}
	case panelSets:
		if set, ok := m.selectedSet(); ok {
			return m.loadSetMembers(conn.Name, db.Name, set.Name)
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

	// A run is the one thing in here that is still happening while you look at
	// it, so it draws itself: steps, a meter and a log, none of which is a fact
	// or a row.
	if m.focus == panelRuns {
		m.drawRun(c, body, tabs[active])
		return
	}

	// A comp.Detail lays itself out into the rect and wraps to it, so it is
	// handed the body and nothing here measures anything.
	content := m.paneBody(active, m.textWidth(body))
	if content.detail != nil {
		content.detail.Draw(c, body, comp.Region(regBody))
		return
	}

	// The pane scrolls rather than truncating: a manifest of 213 tables is the
	// normal case, not an edge one. comp.List does the scrolling, keeps the
	// cursor in view when it moves, and reports the position — all of which was
	// hand-written arithmetic sharing a window() helper with the panels.
	//
	// The list's status row says HOW MUCH is hidden; the scrollbar says WHERE
	// in it you are. Two different questions, and the list drew neither: it
	// exposed Offset and Max for exactly this and nothing read them.
	lines := content.lines
	list, bar := m.scrollable(body, len(lines))

	// Laid out AGAIN when a scrollbar appeared, because it took a column the
	// first layout had already spent. Two passes, and the order is forced: the
	// width depends on whether there is a bar, and whether there is a bar
	// depends on how many rows the width produced.
	//
	// This is the bug that shipped: the rows were laid out for the whole body
	// and drawn into a rect three columns narrower — one for the scrollbar, two
	// for the cursor marker — so a 224-table listing lost the end of its last
	// column and the header read SNAPSH.
	if w := m.textWidth(list); w != m.textWidth(body) {
		lines = m.paneBody(active, w).lines
	}
	m.paneList.Focused = m.paneFocus
	m.paneList.DrawFunc(c, list, len(lines), func(i int) comp.Row {
		return lines[i]
	})
	if !bar.Empty() {
		comp.Scrollbar{
			Total:  len(lines),
			Shown:  m.paneList.Shown(),
			Offset: m.paneList.Offset(),
			Track:  &mutedStyle,
			Thumb:  &accentStyle,
		}.Draw(c, bar, comp.Region(regScroll))
	}
}

// textWidth is the columns a ROW actually gets, which is not the rect's width.
//
// comp.List draws the cursor marker and the status column before the row's own
// text, so a caller laying out a table has to subtract them or the last column
// falls off the right-hand edge — quietly, because the canvas clips rather than
// erroring. LeadWidth is the component's own answer to "how many columns do you
// spend before mine", and asking it beats the literal 2 that would rot the first
// time the marker changed.
func (m *Model) textWidth(r comp.Rect) int {
	return max(1, r.W-m.paneList.LeadWidth())
}

// scrollable divides the body between the rows and a scrollbar, and gives the
// bar no columns at all when everything fits.
//
// A gutter that is always there costs a column of every row to say nothing; one
// that appears only when it means something is a column of content the rest of
// the time. The list is asked whether it overflowed rather than told: it is the
// component that knows how many rows its own chrome spends.
func (m *Model) scrollable(body comp.Rect, rows int) (list, bar comp.Rect) {
	if rows <= body.H-m.paneList.StatusRows() {
		return body, comp.Rect{}
	}
	bands := comp.Layout{Constraints: []comp.Constraint{
		comp.Fill(1), comp.Length(1),
	}}.Cols(body)
	return bands[0], bands[1]
}

// paneRowCount is how many lines the pane's body has, for cursor clamping.
func (m *Model) paneRowCount() int {
	tabs := m.paneTabs()
	active := clamp(m.tabs[m.focus], len(tabs)-1)
	// A detail pane does not scroll, so it has no rows to count: it is laid out
	// to fit and clipped if it does not.
	//
	// The width is an estimate, and it can be, because every tab that returns
	// lines returns one line per thing whatever the width — nothing here wraps.
	// It is the same estimate the cursor is clamped against either way.
	return len(m.paneBody(active, m.screenWidth()-leftWidth-5).lines)
}

// paneContent is what a tab has to say, in one of the two shapes a tab comes in.
//
// The distinction is real rather than a migration artefact. Overview, Config
// and Manifest are a dozen facts and a note: they fit, and comp.Detail lays
// them out — including the label column, which two other tools both padded to
// an arbitrary number and one of them overflowed. Tables, Databases and Foreign
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
		return m.viewConnectionTab(tab)
	case panelDatabases:
		return m.viewDatabaseTab(tab, width)
	case panelSnapshots:
		return m.viewSnapshotTab(tab, width)
	case panelSets:
		return m.viewSetTab(tab, width)
	}
	return paneContent{}
}

// tableRows lays out aligned columns as rows a comp.List can scroll.
//
// comp.Table does the widths, including the flexible column — which pgctl was
// computing itself with a loop that summed every OTHER column and subtracted,
// and a floor of 8 to stop it going negative. A Column with Fill says the same
// thing declaratively, and at most one column may have it: "a table with two
// greedy columns has no answer and silently picking one hides the mistake."
//
// Rows rather than a string, because comp.Table lays out rather than drawing,
// so the result can be scrolled — which is the whole reason the table tabs are
// lines and not a comp.Detail. A 213-table manifest is the normal case here.
//
// Numbers are right-aligned, which pgctl never did: a column of sizes that ends
// ragged is a column you cannot compare down.
//
// Spans, not strings. The previous version of this comment said a per-cell
// colour had nowhere to live, because the table returned whole lines — so the
// data-mode column said "none" and "filtered" in plain text and let the words
// carry what amber was saying. comp.Table.Spans lays out styled cells and
// returns styled rows, so the colour is back and the words stay.
func tableRows(width int, headers []string, cols []comp.Column, rows [][]comp.Segment) []comp.Row {
	head := make([]comp.Segment, len(headers))
	for i, h := range headers {
		head[i] = comp.Segment{Text: h, Style: &headerStyle}
	}

	laid := comp.Table{Columns: cols, Gap: 1}.Spans(width, append([][]comp.Segment{head}, rows...))
	out := make([]comp.Row, 0, len(laid))
	for i, spans := range laid {
		row := comp.Row{Spans: spans}
		for _, s := range spans {
			row.Text += s.Text
		}
		// The header is not a row the cursor can land on. Without Skip, ↑↓
		// appears to do nothing on the first press and enter acts on a thing
		// that is not one.
		row.Skip = i == 0
		out = append(out, row)
	}
	return out
}

// row builds a comp.Row from spans, filling in the plain text a selected row is
// drawn with — comp.List needs both, and deriving one from the other here means
// no caller can supply a Text that disagrees with its Spans.
func row(spans ...comp.Segment) comp.Row {
	var text string
	for _, s := range spans {
		text += s.Text
	}
	return comp.Row{Text: text, Spans: spans}
}

// head is a row above a table that the cursor passes over: a count, a heading,
// the blank between them.
//
// Row.Skip is the whole of it, and without it three things break at once — ↑↓
// appears to do nothing on the first press, the marker sits on a line that is
// not a thing, and enter would act on it. The summary line above a 224-table
// listing is exactly that: it says how many there are, and it is not one of
// them.
func headRow(spans ...comp.Segment) comp.Row {
	r := row(spans...)
	r.Skip = true
	return r
}

// blank is a spacer, which is also not a row the cursor may land on.
func blank() comp.Row { return comp.Row{Skip: true} }

// cells is one table row, styled per cell.
func cells(in ...comp.Segment) []comp.Segment { return in }

// text is an unstyled cell, which most of them are.
func text(s string) comp.Segment { return comp.Segment{Text: s} }

// dataMode is what a rule does to a table's data, and the style that says it.
//
// rule.Mode(), not rule.Data: the default — "filtered" when there is a Where,
// "all" otherwise — is config's, and reading the raw field reported a filtered
// rule as carrying everything, which is the most misleading answer available.
func dataMode(rule config.Rule) comp.Segment {
	switch rule.Mode() {
	case config.DataNone:
		return comp.Segment{Text: "none", Style: &warnStyle}
	case config.DataFiltered:
		return comp.Segment{Text: "filtered", Style: &warnStyle}
	default:
		return comp.Segment{Text: "all", Style: &mutedStyle}
	}
}

// clamp keeps an index inside a list that may have shrunk under it.
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
