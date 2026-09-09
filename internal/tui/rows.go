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
// Three of the row's fields do work this file used to do by hand:
//
// Status is the fixed first column — reachability, whether a snapshot finished,
// whether a run failed. It keeps its own colour under the selection, which
// matters more than it sounds: as an ordinary span the glyph came out bold black
// on white under the highlight, so the one row a reader was looking at was the
// one row whose state they could not read.
//
// Right is drawn hard against the row's right edge, and the ROW measures it. The
// version this replaces padded a name to `panelInner - 9` at four call sites, a
// constant derived from the panel width by subtraction — and every one of those
// nines was a column count that had to agree with a border, an inset and a
// marker nobody was looking at.
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

func (m *Model) connectionRows() []comp.Row {
	conns := m.connections()
	out := make([]comp.Row, 0, len(conns))
	for _, conn := range conns {
		// The marker answers "can I reach it" before the name answers "which is
		// it", because an unreachable connection changes what every panel below
		// is showing.
		mark, markStyle := "○", &mutedStyle
		switch {
		case m.probing[conn.Name]:
			mark = spinner(m.now)
		case m.probes[conn.Name] == nil:
		case m.probes[conn.Name].Reachable:
			mark, markStyle = "●", &okStyle
		default:
			mark, markStyle = "✗", &dangerStyle
		}

		// The right column is the SAFETY FLAG, and nothing else.
		//
		// It used to hold the server version too, with a flag displacing it —
		// so a reader saw `17.10` on one row, `guarded` on the next and nothing
		// on a third, which is one column doing three jobs and looks like
		// missing data rather than a distinction. Somebody said so.
		//
		// The flag is what stays, because it is the only thing here that
		// changes what pgctl will DO: protected is refused outright, guarded
		// demands the name typed in full, and both are the engine's rather than
		// this screen's. A version is a fact about the server and belongs where
		// the other facts about it are — the Overview tab, one `[` away, which
		// has said `server PostgreSQL 17.10` all along.
		//
		// A blank right column is therefore correct and not an omission: the
		// rows with text on them are exactly the rows that will argue with you.
		var right []comp.Segment
		switch {
		case conn.Protected:
			right = []comp.Segment{span("protected", &dangerStyle)}
		case conn.Guarded:
			right = []comp.Segment{span("guarded", &warnStyle)}
		}

		out = append(out, comp.Row{
			// Key, so the cursor follows the CONNECTION rather than the line.
			// A filter typed and cleared, or a probe that reordered nothing but
			// redrew everything, lands the reader back on the row they were on.
			Key:         conn.Name,
			Status:      mark,
			StatusStyle: markStyle,
			Text:        conn.Name,
			Right:       right,
		})
	}
	return out
}

func (m *Model) databaseRows() []comp.Row {
	dbs := m.databases()
	out := make([]comp.Row, 0, len(dbs))
	for _, db := range dbs {
		size := span("-", &mutedStyle)
		if db.Bytes > 0 {
			size = span(engine.HumanBytes(db.Bytes), nil)
		}
		out = append(out, comp.Row{
			Key:   db.Name,
			Text:  db.Name,
			Right: []comp.Segment{size},
		})
	}
	return out
}

func (m *Model) snapshotRows() []comp.Row {
	list := m.snapshots()
	out := make([]comp.Row, 0, len(list))
	for _, row := range list {
		// A database heading: a label, not a thing. Skip keeps the cursor off
		// it, so ↑↓ moves between snapshots and the detail pane always has one.
		if row.entry == nil {
			out = append(out, comp.Row{
				Key:   "db/" + row.heading,
				Text:  row.heading,
				Style: &headerStyle,
				Skip:  true,
			})
			continue
		}

		man := row.entry.Manifest
		stamp := man.StartedAt.Local().Format("01-02 15:04")

		// Where it is, in one or two columns, because that is what a panel this
		// narrow has: `l` on disk, `r` in a remote, `l+r` both. A subset apply
		// fetches only the files it needs, so a snapshot that is remote-only is
		// still usable — the marker says what it will COST, not whether it can
		// be used.
		//
		// Which remote is not on this row and cannot be: two remotes are two
		// names, and the names are on the Manifest tab, where there is room for
		// them. This says "somewhere other than here".
		where := span("l", &mutedStyle)
		switch {
		case row.entry.Local() && row.entry.Remote():
			where = span("l+r", &okStyle)
		case row.entry.Remote():
			where = span("r", &accentStyle)
		}

		// An unfinished snapshot cannot be applied — the engine refuses it —
		// so it is marked in the status column, where the answer to "is this
		// one usable" is on every other row too.
		status, statusStyle := "", &mutedStyle
		if !man.Complete() {
			status, statusStyle = "✗", &dangerStyle
		}

		out = append(out, comp.Row{
			Key:         man.ID,
			Status:      status,
			StatusStyle: statusStyle,
			Text:        stamp,
			// Indented under its heading, when there is one. A group of one
			// database has no heading and no indent to sit under.
			Depth: m.snapshotDepth(),
			Right: []comp.Segment{
				span(engine.HumanBytes(man.Bytes)+" ", nil),
				where,
			},
		})
	}
	return out
}

// snapshotDepth indents a snapshot under its database heading, and does not
// when there is no heading to indent under.
//
// Asked of the rows rather than remembered, because whether the panel is
// grouped is a property of what the connection holds: one database's worth of
// snapshots is a flat list, and the second database is what turns it into
// groups.
func (m *Model) snapshotDepth() int {
	for _, r := range m.snapshots() {
		if r.entry == nil {
			return 1
		}
	}
	return 0
}

func (m *Model) setRows() []comp.Row {
	conn, _ := m.selectedConn()
	db, _ := m.selectedDatabase()

	sets := m.sets()
	out := make([]comp.Row, 0, len(sets))
	for _, set := range sets {
		note := span("?", &mutedStyle)
		if s := m.setInfo[setKey(conn.Name, db.Name, set.Name)]; s != nil {
			switch {
			case s.loading:
				note = span(spinner(m.now), &mutedStyle)
			case s.err != nil:
				note = span("✗", &dangerStyle)
			case len(s.added) > 0:
				// The number that matters about a set is not how many tables it
				// names but how many it drags in.
				note = span(fmt.Sprintf("%d+%d", len(s.members), len(s.added)), &warnStyle)
			default:
				note = span(fmt.Sprintf("%d closed", len(s.members)), &okStyle)
			}
		}
		out = append(out, comp.Row{
			Key:   set.Name,
			Text:  set.Name,
			Right: []comp.Segment{note},
		})
	}
	return out
}

func (m *Model) runRows() []comp.Row {
	runs := m.runList()
	out := make([]comp.Row, 0, len(runs))
	for _, r := range runs {
		mark, markStyle := "✓", &okStyle
		switch {
		case r.running:
			mark, markStyle = spinner(m.now), &accentStyle
		case r.err != nil:
			mark, markStyle = "✗", &dangerStyle
		}
		out = append(out, comp.Row{
			Key:         r.id,
			Status:      mark,
			StatusStyle: markStyle,
			Text:        r.kind,
			Right:       []comp.Segment{span(elapsed(r.duration(m.now)), &mutedStyle)},
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
// The frames and that rule are comp.Spinner's — tuikit took them from this file
// and says so in its comment. Every is passed explicitly rather than left to
// default, because it has to agree with tickInterval or the spinner jumps
// several frames between redraws and reads as flicker; the two constants being
// equal by coincidence is exactly the arrangement that breaks quietly.
func spinner(now time.Time) string {
	return comp.Spinner{Every: tickInterval}.Frame(now)
}
