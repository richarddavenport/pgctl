package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/richarddavenport/tuikit/comp"

	"github.com/richarddavenport/pgctl/internal/engine"
)

// panelRows renders one panel's list.
//
// A row is PLAIN TEXT plus styled spans, never a pre-styled string. The canvas
// draws clusters into cells, so an escape sequence handed to it is text: the
// first version of this after the canvas port passed the old styled strings
// straight through, and with colour on the Connections panel drew a blank row.
// With colour off it worked, so 38 goldens agreed it was fine —
// TestColourDoesNotChangeTheShape is what caught it, on the first run.
//
// Spans also mean the selected row can be ONE colour. comp.List drops a row's
// own spans under the cursor deliberately: the selection is the reader's mark
// on the list, and a row that kept its own colours under it makes the cursor
// hard to find in exactly the list where finding it matters.
func (m *Model) panelRows(panel int) []comp.Row {
	switch panel {
	case panelConnections:
		return m.connectionRows()
	case panelDatabases:
		return m.databaseRows()
	case panelSnapshots:
		return m.snapshotRows()
	case panelSets:
		return m.setRows()
	case panelRuns:
		return m.runRows()
	}
	return nil
}

// span is one styled run of a row.
func span(text string, style *lipgloss.Style) comp.Segment {
	return comp.Segment{Text: text, Style: style}
}

// row builds a comp.Row from spans, filling in the plain text a selected row
// is drawn with — comp.List needs both, and deriving one from the other here
// means no caller can supply a Text that disagrees with its Spans.
func row(spans ...comp.Segment) comp.Row {
	var text string
	for _, s := range spans {
		text += s.Text
	}
	return comp.Row{Text: text, Spans: spans}
}

func (m *Model) connectionRows() []comp.Row {
	conns := m.connections()
	out := make([]comp.Row, 0, len(conns))
	for _, conn := range conns {
		// The marker answers "can I reach it" before the name answers "which
		// is it", because an unreachable environment changes what every panel
		// below is showing.
		// The reachability glyph is the row's LEAD, not its first span, so it
		// keeps its own colour when the row is selected. As a span it came out
		// bold black on white under the highlight — the one row a reader is
		// looking at was the one row whose status they could not read, which is
		// what tuikit #44 was about and Row.LeadStyle is the answer to.
		mark, markStyle := "○", &mutedStyle
		note := comp.Segment{}
		switch {
		case m.probing[conn.Name]:
			mark = spinner(m.now)
		case m.probes[conn.Name] == nil:
		case m.probes[conn.Name].Reachable:
			mark, markStyle = "●", &okStyle
			note = span(" "+formatServerVersion(m.probes[conn.Name].ServerVersion), &mutedStyle)
		default:
			mark, markStyle = "✗", &dangerStyle
		}

		switch {
		case conn.Protected:
			note = span(" protected", &dangerStyle)
		case conn.Guarded:
			note = span(" guarded", &warnStyle)
		}

		r := row(span(fmt.Sprintf("%-9s", comp.Truncate(conn.Name, 9)), nil), note)
		r.Lead, r.LeadStyle = mark+" ", markStyle
		out = append(out, r)
	}
	return out
}

func (m *Model) databaseRows() []comp.Row {
	dbs := m.databases()
	out := make([]comp.Row, 0, len(dbs))
	for _, db := range dbs {
		size := span("       -", &mutedStyle)
		if db.Bytes > 0 {
			size = span(fmt.Sprintf("%8s", engine.HumanBytes(db.Bytes)), nil)
		}
		// The name takes whatever the size column leaves, so a long database
		// name is only shortened when it genuinely does not fit.
		width := panelInner - 9
		out = append(out, row(
			span(comp.Pad(db.Name, width)+" ", nil),
			size,
		))
	}
	return out
}

func (m *Model) snapshotRows() []comp.Row {
	snaps := m.snapshots()
	out := make([]comp.Row, 0, len(snaps))
	for _, entry := range snaps {
		man := entry.Manifest
		stamp := man.StartedAt.Local().Format("01-02 15:04")

		where := span("l", &mutedStyle)
		switch {
		case entry.Local && entry.Remote:
			where = span("l+r", &okStyle)
		case entry.Remote:
			where = span("r", &accentStyle)
		}
		spans := []comp.Segment{
			span(fmt.Sprintf("%s %7s ", stamp, engine.HumanBytes(man.Bytes)), nil),
			where,
		}
		if !man.Complete() {
			spans = append(spans, span(" ✗", &dangerStyle))
		}
		out = append(out, row(spans...))
	}
	return out
}

func (m *Model) setRows() []comp.Row {
	conn, _ := m.selectedConn()
	db, _ := m.selectedDatabase()

	sets := m.sets()
	out := make([]comp.Row, 0, len(sets))
	for _, set := range sets {
		note := span(" ?", &mutedStyle)
		if s := m.setInfo[setKey(conn.Name, db.Name, set.Name)]; s != nil {
			switch {
			case s.loading:
				note = span(" "+spinner(m.now), &mutedStyle)
			case s.err != nil:
				note = span(" ✗", &dangerStyle)
			case len(s.added) > 0:
				// The number that matters about a set is not how many tables it
				// names but how many it drags in.
				note = span(fmt.Sprintf(" %d+%d", len(s.members), len(s.added)), &warnStyle)
			default:
				note = span(fmt.Sprintf(" %d closed", len(s.members)), &okStyle)
			}
		}
		out = append(out, row(span(comp.Truncate(set.Name, panelInner-8), nil), note))
	}
	return out
}

func (m *Model) runRows() []comp.Row {
	runs := m.runList()
	out := make([]comp.Row, 0, len(runs))
	for _, r := range runs {
		// The lead again: whether a run failed is state, and a run you have
		// selected is exactly the one you want that about.
		mark, markStyle := "✓", &okStyle
		switch {
		case r.running:
			mark, markStyle = spinner(m.now), &accentStyle
		case r.err != nil:
			mark, markStyle = "✗", &dangerStyle
		}
		out = append(out, comp.Row{
			Lead:      mark + " ",
			LeadStyle: markStyle,
			Text:      comp.Pad(r.kind, panelInner-9) + " " + elapsed(r.duration(m.now)),
			Spans: []comp.Segment{
				span(comp.Pad(r.kind, panelInner-9)+" ", nil),
				span(elapsed(r.duration(m.now)), &mutedStyle),
			},
		})
	}
	return out
}

// formatServerVersion renders 170004 as "17.4".
func formatServerVersion(v int) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprintf("%d.%d", v/10000, v%10000)
}

// elapsed formats a duration as m:ss, or h:mm:ss past an hour.
//
// Not time.Duration.String(): "1m0s" and "1h0m0s" are hard to read at a glance
// and change width as they tick, which makes a status line jitter.
func elapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second).Seconds())
	h, mins, sec := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, mins, sec)
	}
	return fmt.Sprintf("%d:%02d", mins, sec)
}

// age renders how long ago something happened, in the coarsest unit that still
// says something useful.
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// spinner picks its frame from the clock rather than from a counter, so every
// spinner on screen turns together and at a steady rate however often the view
// happens to be rebuilt.
//
// The frames and that rule are now comp.Spinner's — tuikit took them from this
// file and says so in its comment. Every is passed explicitly rather than left
// to default, because it has to agree with tickInterval or the spinner jumps
// several frames between redraws and reads as flicker; the two constants being
// equal by coincidence is exactly the arrangement that breaks quietly.
func spinner(now time.Time) string {
	return comp.Spinner{Every: tickInterval}.Frame(now)
}
