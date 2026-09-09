package tui

import (
	"fmt"
	"time"

	"github.com/richarddavenport/tuikit/comp"

	"github.com/richarddavenport/pgctl/internal/engine"
)

// The run screen is what an operation looks like while it is happening, and
// afterwards. It has two tabs because there are two questions:
//
// Steps is "where has it got to". The engine names a phase on every event it
// reports — dump, filtered copy, drop constraints, load, reindex, validate —
// so the phases are data rather than something this file invents, and a
// comp.StepList is exactly that list with a glyph per state and what each one
// cost. A whole-database apply has seven; a set-level apply of the claims set
// has eleven.
//
// Log is "what exactly did it say". Every event in order, tailing, scrollable
// back. comp.LogPane distinguishes a line from stderr and nothing else, which
// is why the previous version of this screen would not use it: the engine
// reports six kinds and five of them are things a reader needs to tell apart.
// Splitting the screen answers that — the kinds carry the steps view, and the
// log is the stream it came from.

// drawRun renders the selected run.
func (m *Model) drawRun(c *comp.Canvas, r comp.Rect, tab string) {
	rec, ok := m.selectedRun()
	if !ok {
		m.detail(comp.Block{
			Text: "Nothing has run yet. n takes a snapshot, a applies one, " +
				"m moves between environments.",
		}).Draw(c, r, comp.Region(regBody))
		return
	}

	// The title, and the meter under it while there is a fraction to show.
	bands := comp.Layout{Constraints: []comp.Constraint{
		comp.Length(1), comp.Length(m.meterRows(rec)), comp.Fill(1),
	}}.Rows(r)

	state, stateStyle := "ok", &okStyle
	switch {
	case rec.running:
		state, stateStyle = "running", &accentStyle
	case rec.err != nil:
		state, stateStyle = "failed", &dangerStyle
	}
	left := []comp.Segment{
		{Text: rec.kind, Style: &titleStyle},
		{Text: "  " + rec.explain, Style: &mutedStyle},
	}
	comp.Bar{
		Left: left,
		Right: []comp.Segment{
			{Text: elapsed(rec.duration(m.now)) + "  ", Style: &mutedStyle},
			{Text: state, Style: stateStyle},
		},
		MinLeft: 16,
	}.Draw(c, bands[0], comp.Region(regBody))

	m.drawMeter(c, bands[1], rec)

	if tab == "Log" {
		m.drawLog(c, bands[2], rec)
		return
	}
	m.drawSteps(c, bands[2], rec)
}

// meterRows is one row for the meter, or none.
//
// None unless there is a real denominator: comp.Meter takes a value from 0 to 1,
// and a bar built from a cumulative byte count with nothing to divide it by is
// a bar that invents its own progress. A plan knows how many tables it will
// load, so that is the fraction — and a snapshot, which does not, gets the byte
// counter in the log and no bar at all.
func (m *Model) meterRows(rec *runRecord) int {
	if rec.total > 0 {
		return 1
	}
	return 0
}

func (m *Model) drawMeter(c *comp.Canvas, r comp.Rect, rec *runRecord) {
	if rec.total <= 0 || r.H < 1 {
		return
	}
	done := rec.tablesSeen()
	comp.Meter{
		Value:      float64(done) / float64(rec.total),
		Label:      fmt.Sprintf("%d of %d tables", done, rec.total),
		Track:      regMeter,
		Filled:     &okStyle,
		Empty:      &mutedStyle,
		LabelStyle: &mutedStyle,
		// A smooth bar where the terminal can draw pictures, characters where
		// it cannot. The component asks; it never says what colour, because the
		// gradient comes from the canvas, which read it from the terminal's own
		// palette.
		Pixels: true,
	}.Draw(c, r, comp.Region(regMeter))
}

// drawSteps is the phases of the run, under the database each belongs to.
func (m *Model) drawSteps(c *comp.Canvas, r comp.Rect, rec *runRecord) {
	steps, databases := rec.stepsByDatabase(m.now)
	if len(steps) == 0 {
		m.detail(comp.Block{Text: spinner(m.now) + " starting…"}).
			Draw(c, r, comp.Region(regSteps))
		return
	}

	status, statusStyle := "done", &okStyle
	switch {
	case rec.running:
		status, statusStyle = "running…", &accentStyle
	case rec.err != nil:
		status, statusStyle = "failed — "+rec.err.Error(), &dangerStyle
	case rec.summary != "":
		status = rec.summary
	}

	// Nothing in a step list is selectable — a step is something that happened,
	// not a thing to act on — so the pane's cursor marker is turned off for
	// this view. Left on, comp.List drew a › against row zero, which is a
	// database heading, and the cursor could not move off it because every row
	// is Skip.
	m.paneList.Marker, m.paneList.Blank = "", ""
	defer func() { m.paneList.Marker, m.paneList.Blank = "› ", "  " }()

	look := stepLook(m.now)
	list := comp.StepList{
		Steps:       steps,
		Look:        look,
		Status:      status,
		StatusStyle: statusStyle,
		Muted:       &mutedStyle,
	}

	// One database, or an operation that does not name one: the steps ARE the
	// run and there is nothing to group under.
	rows := list.Rows(r.W)
	if names := distinct(databases); len(names) > 1 {
		// The width a grouped row actually gets: the rect, less the indent the
		// heading puts its steps under. Getting this wrong clips the
		// right-aligned duration column and nothing says so — the same
		// mistake, in the same shape, as the detail pane's tables.
		rows = m.groupedStepRows(steps, databases, look, r.W-c.Chrome().Indent)
		rows = append(rows, blank(), comp.Row{Text: "  " + status, Style: statusStyle, Skip: true})
	}

	// A set-level apply logs eleven phases and a whole-database one seven, so
	// they fit — but a snapshot of six databases is eighteen plus six headings,
	// and StepList draws every step and does not scroll. A run that has
	// overflowed is exactly the one you want the end of, so it goes through the
	// list, which does.
	if len(rows) <= r.H && len(distinct(databases)) <= 1 {
		list.Draw(c, r, regSteps)
		return
	}
	m.paneList.Focused = m.paneFocus
	m.paneList.DrawFunc(c, r, len(rows), func(i int) comp.Row { return rows[i] })
}

// groupedStepRows is the steps with a heading per database.
//
// One StepList per group rather than one for all of them, because the component
// is what knows how a step is drawn — the glyph, the label column, the
// right-aligned duration — and reimplementing that here to insert headings
// would be the fourth tool to draw a step list by hand. It lays out each group;
// this only decides what comes between them.
func (m *Model) groupedStepRows(steps []comp.Step, databases []string,
	look [5]comp.StepLook, width int) []comp.Row {

	var out []comp.Row
	for i := 0; i < len(steps); {
		db := databases[i]
		j := i
		for j < len(steps) && databases[j] == db {
			j++
		}

		if db == "" {
			db = "the run"
		}
		if len(out) > 0 {
			out = append(out, blank())
		}
		out = append(out, comp.Row{Text: db, Style: &headerStyle, Skip: true})

		group := comp.StepList{Steps: steps[i:j], Look: look, Muted: &mutedStyle}
		for _, row := range group.Rows(width - 2) {
			row.Depth, row.Skip = 1, true
			out = append(out, row)
		}
		i = j
	}
	return out
}

// distinct is the unique values, in order of appearance.
func distinct(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// drawLog is every event in order.
func (m *Model) drawLog(c *comp.Canvas, r comp.Rect, rec *runRecord) {
	events := rec.log()

	// The database column is measured, not guessed. comp.Pad truncates as well
	// as pads, so a literal twelve turned `product-development` into
	// `product-dev…` on every line of its own run.
	width := 0
	for _, ev := range events {
		width = max(width, comp.Width(ev.Database))
	}

	lines := make([]comp.LogLine, 0, len(events))
	for _, ev := range events {
		lines = append(lines, comp.LogLine{
			At:   ev.At.Local().Format("15:04:05"),
			Text: logText(ev, width),
			// A warning and a failure are the two the reader has to be able to
			// find in a run that logged four hundred lines. Everything else the
			// engine reports is ordinary progress, and colouring that as an
			// error would make every successful run look broken.
			Stderr: ev.Kind == engine.EventWarning || ev.Kind == engine.EventFailed,
		})
	}
	// The newest progress as the last line, while there is one. It is not in
	// the event list — add() replaces it in place rather than appending — and
	// this end of the seam is why: rebuilt every frame, the counter counts in
	// place instead of writing a line per redraw and burying the log it is
	// meant to annotate.
	if p := rec.progressNow(); rec.running && p.Message != "" {
		lines = append(lines, comp.LogLine{
			At:   p.At.Local().Format("15:04:05"),
			Text: p.Message,
		})
	}
	m.logPane.Draw(c, r, lines, regLog)
}

// logText is one event as a line.
//
// The database leads, where there is one, for the same reason the steps are
// grouped by it: a snapshot of six databases logs the same phases six times and
// the only other clue is a path inside a message.
func logText(ev engine.Event, width int) string {
	var what string
	switch {
	case ev.Table != "" && ev.Message != "":
		what = ev.Table + "  " + ev.Message
	case ev.Table != "":
		what = ev.Table
	default:
		what = ev.Message
	}
	if ev.Database != "" {
		return comp.Pad(ev.Database, width) + "  " + what
	}
	return what
}

// steps is the run's phases, derived from the events rather than declared.
//
// The engine names a Step on everything it reports, so the phases are the
// engine's own account of what it is doing: nothing here has to be kept in step
// with apply.go, and a phase added there appears here without an edit. What
// this file decides is only how a phase LOOKS — which one is running, what each
// one cost, and which one a failure belongs to.
func (r *runRecord) steps(now time.Time) []comp.Step {
	steps, _ := r.stepsByDatabase(now)
	return steps
}

// stepsByDatabase is the phases, and which database each one belongs to.
//
// Two returns rather than a struct, because comp.Step is the component's type
// and pgctl does not get to add a field to it. The slices are the same length
// and the same order, which is the whole contract.
func (r *runRecord) stepsByDatabase(now time.Time) ([]comp.Step, []string) {
	events := r.log()

	var steps []comp.Step
	var databases []string
	var startedAt []time.Time
	at := func(i int) *comp.Step { return &steps[i] }

	for _, ev := range events {
		last := len(steps) - 1

		// A named phase that is not the one we are in opens a new step and
		// closes the one before it — and so does the SAME phase arriving about
		// a different database, which is what makes six dumps six steps rather
		// than one step that keeps restarting.
		if ev.Step != "" && (last < 0 || steps[last].Label != ev.Step ||
			databases[last] != ev.Database) {
			if last >= 0 && steps[last].State == comp.StepRunning {
				at(last).State = comp.StepDone
				at(last).Took = elapsed(ev.At.Sub(startedAt[last]))
			}
			steps = append(steps, comp.Step{Label: ev.Step, State: comp.StepRunning})
			databases = append(databases, ev.Database)
			startedAt = append(startedAt, ev.At)
			last = len(steps) - 1
		}
		if last < 0 {
			continue
		}

		switch ev.Kind {
		case engine.EventStep:
			at(last).Detail = ev.Message
		case engine.EventTable:
			// The table, not the message: a phase loading 42 tables says which
			// one it is on, and the detail column is one line.
			at(last).Detail = ev.Table
		case engine.EventWarning:
			// A warning does not stop a step, so the step keeps its state and
			// gains the line a warning owes.
			at(last).Note = "! " + ev.Message
		case engine.EventFailed:
			at(last).State = comp.StepFailed
			at(last).Note = ev.Message
			at(last).Took = elapsed(ev.At.Sub(startedAt[last]))
		case engine.EventDone:
			at(last).State = comp.StepDone
			at(last).Took = elapsed(ev.At.Sub(startedAt[last]))
		}
	}

	// The newest progress belongs to whatever phase is in flight, and it does
	// not come through the event list: add() replaces it in place so a byte
	// counter counts instead of scrolling.
	if last := len(steps) - 1; last >= 0 && steps[last].State == comp.StepRunning {
		if p := r.progressNow(); p.Message != "" {
			steps[last].Detail = p.Message
		}
	}

	// The last phase is still running only if the operation is. A cancelled run
	// leaves its phase where it stopped rather than claiming it finished.
	if last := len(steps) - 1; last >= 0 && steps[last].State == comp.StepRunning {
		switch {
		case r.running:
			steps[last].Took = elapsed(now.Sub(startedAt[last]))
		case r.err != nil:
			steps[last].State = comp.StepFailed
			steps[last].Took = elapsed(r.endedAt.Sub(startedAt[last]))
		default:
			steps[last].State = comp.StepDone
			steps[last].Took = elapsed(r.endedAt.Sub(startedAt[last]))
		}
	}
	return steps, databases
}

// stepLook is the glyph and colour per state.
//
// The spinner is the running step's glyph, so the one phase in flight is the one
// thing moving on the screen — and it comes from the clock, so it turns at the
// same rate as every other spinner in the interface.
func stepLook(now time.Time) [5]comp.StepLook {
	return [5]comp.StepLook{
		comp.StepWaiting: comp.Look("○", &mutedStyle),
		comp.StepRunning: {Glyph: spinner(now), Style: &accentStyle, LabelStyle: &accentStyle},
		comp.StepSkipped: comp.Look("·", &mutedStyle),
		comp.StepDone:    comp.Look("✓", &okStyle),
		comp.StepFailed:  {Glyph: "✗", Style: &dangerStyle, LabelStyle: &dangerStyle},
	}
}
