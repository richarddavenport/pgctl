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

// drawSteps is the phases of the run.
func (m *Model) drawSteps(c *comp.Canvas, r comp.Rect, rec *runRecord) {
	steps := rec.steps(m.now)
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

	list := comp.StepList{
		Steps:       steps,
		Look:        stepLook(m.now),
		Status:      status,
		StatusStyle: statusStyle,
		Muted:       &mutedStyle,
	}

	// A set-level apply logs eleven phases and a whole-database one seven, so
	// they fit — but a run whose steps outgrow the pane scrolls, because
	// StepList draws every step and does not scroll and a run that has
	// overflowed is exactly the one you want the end of. Rows() is what the
	// component offers for that.
	rows := list.Rows(r.W)
	if len(rows) <= r.H {
		list.Draw(c, r, regSteps)
		return
	}
	m.paneList.Focused = m.paneFocus
	m.paneList.DrawFunc(c, r, len(rows), func(i int) comp.Row { return rows[i] })
}

// drawLog is every event in order.
func (m *Model) drawLog(c *comp.Canvas, r comp.Rect, rec *runRecord) {
	events := rec.log()
	lines := make([]comp.LogLine, 0, len(events))
	for _, ev := range events {
		lines = append(lines, comp.LogLine{
			At:   ev.At.Local().Format("15:04:05"),
			Text: logText(ev),
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
func logText(ev engine.Event) string {
	switch {
	case ev.Table != "" && ev.Message != "":
		return ev.Table + "  " + ev.Message
	case ev.Table != "":
		return ev.Table
	case ev.Step != "" && ev.Kind == engine.EventStep:
		return ev.Message
	default:
		return ev.Message
	}
}

// steps is the run's phases, derived from the events rather than declared.
//
// The engine names a Step on everything it reports, so the phases are the
// engine's own account of what it is doing: nothing here has to be kept in step
// with apply.go, and a phase added there appears here without an edit. What
// this file decides is only how a phase LOOKS — which one is running, what each
// one cost, and which one a failure belongs to.
func (r *runRecord) steps(now time.Time) []comp.Step {
	events := r.log()

	var steps []comp.Step
	var startedAt []time.Time
	at := func(i int) *comp.Step { return &steps[i] }

	for _, ev := range events {
		last := len(steps) - 1

		// A named phase that is not the one we are in opens a new step and
		// closes the one before it.
		if ev.Step != "" && (last < 0 || steps[last].Label != ev.Step) {
			if last >= 0 && steps[last].State == comp.StepRunning {
				at(last).State = comp.StepDone
				at(last).Took = elapsed(ev.At.Sub(startedAt[last]))
			}
			steps = append(steps, comp.Step{Label: ev.Step, State: comp.StepRunning})
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
	return steps
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
