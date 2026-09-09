package tui

import (
	"strings"
	"testing"

	"github.com/richarddavenport/tuikit/app"
	"github.com/richarddavenport/tuikit/comp"
	"github.com/richarddavenport/tuikit/harness"
)

// state is one thing pgctl can be showing.
type state struct {
	name  string
	build func(w, h int) *app.Runner
}

// states is every distinct thing pgctl can be showing.
//
// ONE list, walked by the goldens, the narrow-terminal run, the colour check
// and the capture. A screen added without a frame is a screen added without any
// of them, which is the only arrangement in which they cannot drift apart.
//
// The build funcs reach the state by pressing keys rather than by setting
// fields, wherever a person could: a frame reachable only by reaching into the
// model is a frame that may not be reachable at all.
func states(t *testing.T) []state {
	// The size is set BEFORE the keys are pressed, and that is not a detail: a
	// state reached at 132x38 and then resized to 80x24 is not the state a
	// person on an eighty-column terminal reaches. The help overlay is the case
	// that proved it — it fits at 38 lines with nothing to scroll, so a
	// "scrolled" frame built at the wide size and rendered narrow showed the
	// top of the list and looked like a broken scroll.
	//
	// A Runner, not a Model. Keys go through it so that every press REDRAWS,
	// which a running program does and which the components depend on: a
	// comp.List learns how many rows fit from the frame it last drew, so a
	// cursor moved without a redraw is a cursor moved against a viewport that
	// does not exist yet.
	loaded := func(w, h int) *app.Runner {
		m := fixtureModel(t)
		fixtureSnapshot(t, m)
		return run(m, w, h)
	}
	keys := func(r *app.Runner, k ...string) *app.Runner {
		harness.Press(r, k...)
		return r
	}

	states := []state{
		{"connections", loaded},
		{"databases", func(w, h int) *app.Runner { return keys(loaded(w, h), "2") }},
		{"snapshots", func(w, h int) *app.Runner { return keys(loaded(w, h), "3") }},
		{"sets", func(w, h int) *app.Runner { return keys(loaded(w, h), "4") }},

		// The Snapshots panel grouped by database, which is what it looks like
		// on any connection anybody has taken more than one kind of snapshot
		// from. The selected database's group leads.
		{"snapshots-grouped", func(w, h int) *app.Runner {
			m := fixtureModel(t)
			fixtureSnapshot(t, m)
			fixtureTwoDatabases(m)
			return keys(run(m, w, h), "3")
		}},

		// A connection pgctl could not reach. The detail pane has to explain
		// itself rather than render an empty form.
		{"unreachable", func(w, h int) *app.Runner { return keys(loaded(w, h), "down", "down") }},

		// The modal forms, which are where an operator does damage.
		{"form-snapshot", func(w, h int) *app.Runner { return keys(loaded(w, h), "n") }},
		{"form-apply", func(w, h int) *app.Runner { return keys(loaded(w, h), "3", "a") }},
		{"form-move", func(w, h int) *app.Runner { return keys(loaded(w, h), "m") }},
		{"form-prune", func(w, h int) *app.Runner { return keys(loaded(w, h), "p") }},
		{"form-delete", func(w, h int) *app.Runner { return keys(loaded(w, h), "3", "x") }},

		// The snapshot form on a connection with nothing to act on: a refusal
		// naming which of the three reasons it is, rather than an empty form.
		{"form-snapshot-refused", func(w, h int) *app.Runner {
			return keys(loaded(w, h), "1", "G", "n")
		}},

		// The apply form with a scope chosen and the guarded target's phrase
		// half typed: the two fields that gate everything.
		{"form-apply-guarded", func(w, h int) *app.Runner {
			return keys(loaded(w, h), "3", "a", "down", "right", "down", "down", "q", "a")
		}},

		// The plan, which is the last screen between an operator and a
		// destructive act.
		{"plan", func(w, h int) *app.Runner {
			m := fixtureModel(t)
			fixtureSnapshot(t, m)
			r := run(m, w, h)
			fixturePlan(m)
			return r
		}},
		{"plan-scrolled", func(w, h int) *app.Runner {
			m := fixtureModel(t)
			fixtureSnapshot(t, m)
			r := run(m, w, h)
			fixturePlan(m)
			return keys(r, "j", "j", "j", "j", "j", "j")
		}},

		// A run: the steps it is on, and the log they came from.
		{"run-steps", func(w, h int) *app.Runner {
			m := fixtureModel(t)
			fixtureSnapshot(t, m)
			fixtureRun(m)
			r := run(m, w, h)
			return keys(r, "5")
		}},
		{"run-log", func(w, h int) *app.Runner {
			m := fixtureModel(t)
			fixtureSnapshot(t, m)
			fixtureRun(m)
			r := run(m, w, h)
			return keys(r, "5", "tab", "tab")
		}},
		{"run-finished", func(w, h int) *app.Runner {
			m := fixtureModel(t)
			fixtureSnapshot(t, m)
			fixtureRun(m)
			r := run(m, w, h)
			return keys(r, "5", "j")
		}},
		{"leaving", func(w, h int) *app.Runner {
			m := fixtureModel(t)
			fixtureSnapshot(t, m)
			fixtureRun(m)
			r := run(m, w, h)
			return keys(r, "q")
		}},

		{"commands", func(w, h int) *app.Runner { return keys(loaded(w, h), "ctrl+p") }},
		// The directory with a query typed, which flattens the groups into one
		// column and is where a refused row has to keep its reason.
		{"commands-query", func(w, h int) *app.Runner {
			return keys(loaded(w, h), "ctrl+p", "p")
		}},
		// The directory on a connection with nothing selected under it, so the
		// refusals are the point of the frame.
		{"commands-refused", func(w, h int) *app.Runner {
			return keys(run(fixtureModel(t), w, h), "ctrl+p")
		}},

		// A listing longer than the pane, which is the ordinary case for a real
		// database and the state a scrollbar exists for. It is also the one that
		// changes the width every row gets, so it is the frame that holds the
		// table's last column on screen.
		{"tab-databases-tables-scrolling", func(w, h int) *app.Runner {
			m := fixtureModel(t)
			fixtureSnapshot(t, m)
			fixtureManyTables(m)
			r := run(m, w, h)
			return keys(r, "2", "tab")
		}},

		{"help", func(w, h int) *app.Runner { return keys(loaded(w, h), "?") }},
		// The help scrolled to the bottom. The list is longer than a short
		// terminal, so the state that matters is the one where the last section
		// is reachable at all.
		{"help-scrolled", func(w, h int) *app.Runner {
			r := keys(loaded(w, h), "?")
			for i := 0; i < 30; i++ {
				harness.Press(r, "j")
			}
			return r
		}},

		{"filter", func(w, h int) *app.Runner { return keys(loaded(w, h), "/", "q") }},
		{"filter-matches-nothing", func(w, h int) *app.Runner {
			return keys(loaded(w, h), "/", "z", "z")
		}},
		// A refusal, in the toast that carries it. Reached by pressing a on a
		// panel with no snapshot under it, which is how a reader reaches it.
		{"refused", func(w, h int) *app.Runner {
			return keys(run(fixtureModel(t), w, h), "3", "a")
		}},

		// Nothing loaded: no probe has answered and there are no snapshots.
		// Every panel's empty state at once, which is the first thing a new
		// user sees and the last thing anybody looks at.
		{"empty", func(w, h int) *app.Runner { return run(fixtureModel(t), w, h) }},
	}

	// Every tab body, generated from the panels rather than listed.
	//
	// Listed, four of the fourteen had a frame and the other ten did not — and
	// the list's own comment claimed otherwise. A tab added to paneTabs now
	// gets a frame without anybody adding one, which is the only arrangement in
	// which "a screen added without a frame is a screen added without any of
	// them" is true rather than aspirational.
	for panel := 0; panel < panelCount; panel++ {
		if panel == panelRuns {
			// The run screen's tabs have frames of their own above, built
			// against a run: generated here they would all be "nothing has run
			// yet", which is one state listed twice.
			continue
		}
		probe := fixtureModel(t)
		probe.focus = panel
		for tab, name := range probe.paneTabs() {
			states = append(states, state{
				name: "tab-" + strings.ToLower(panelTitles[panel]) + "-" +
					strings.ToLower(strings.ReplaceAll(name, " ", "-")),
				build: func(w, h int) *app.Runner {
					m := fixtureModel(t)
					fixtureSnapshot(t, m)
					m.focus, m.paneFocus = panel, true
					m.tabs[panel] = tab
					return run(m, w, h)
				},
			})
		}
	}
	return states
}

// run wraps a model so the harness can drive it.
//
// The model has no View: the runner owns the canvas, so Update, View and Canvas
// belong to it. SetSize goes through the runner rather than the model, so the
// model hears about the size by the same route it would in a running program —
// two paths to one fact is how they come to disagree.
func run(m *Model, w, h int) *app.Runner {
	r := app.New(m, app.WithChrome(Chrome))
	r.SetSize(w, h)
	return r
}

// The screens as goldens: a layout change is an ordinary test failure.
//
// Run with -update-goldens when the change is intended, and read the diff.
func TestFramesMatchTheirGoldens(t *testing.T) {
	for _, st := range states(t) {
		t.Run(st.name, func(t *testing.T) {
			harness.Golden(t, "testdata", st.name, st.build(132, 38).View())
		})
	}
}

// The same screens on a terminal somebody actually has.
//
// Most layout bugs are only visible when there is not enough room, and all
// three the original screenshot probe found were exactly that.
func TestFramesAtEightyColumns(t *testing.T) {
	for _, st := range states(t) {
		t.Run(st.name, func(t *testing.T) {
			harness.Golden(t, "testdata/80", st.name, st.build(80, 24).View())
		})
	}
}

// Colour must not change the shape.
//
// Goldens are captured uncoloured, so a width bug in a styled string is
// invisible to every one of them — a string measured in runes rather than
// columns is the whole class, and this caught one in the Connections panel on
// its first run.
func TestColorDoesNotChangeTheShape(t *testing.T) {
	for _, size := range []struct{ w, h int }{{132, 38}, {80, 24}} {
		for _, st := range states(t) {
			harness.ShapeSurvivesColor(t, st.name, func() string {
				return st.build(size.w, size.h).View()
			})
		}
	}
}

// Every frame fits its terminal.
//
// The one that stops a frame drawing off the side of the screen: a line wider
// than the terminal makes it scroll, which tears the whole frame rather than
// clipping a row. The canvas cannot be drawn past, so what this catches is the
// arithmetic that decides how big something should be.
func TestEveryFrameFitsItsTerminal(t *testing.T) {
	for _, size := range []struct{ w, h int }{{132, 38}, {80, 24}} {
		for _, st := range states(t) {
			frame := st.build(size.w, size.h).View()
			if w := harness.Width(frame); w > size.w {
				t.Errorf("%s at %dx%d: %d columns wide", st.name, size.w, size.h, w)
			}
			if rows := len(harness.Lines(frame)); rows > size.h {
				t.Errorf("%s at %dx%d: %d rows tall", st.name, size.w, size.h, rows)
			}
		}
	}
}

// PGCTL_FRAMES=/tmp/pgctl-frames go test ./internal/tui -run CaptureFrames
//
// Off unless asked, because writing files is not what `go test ./...` is for.
// `tuikit frames <dir>` turns the captures into a page you can look at.
func TestCaptureFrames(t *testing.T) {
	dir := harness.Enabled("PGCTL_FRAMES")
	if dir == "" {
		t.Skip("set PGCTL_FRAMES to capture frames")
	}
	s := harness.Capture(t, dir, harness.Size(132, 38), harness.At(epoch))
	for _, st := range states(t) {
		s.Shot(st.name, st.build(132, 38))
	}
	s.Done()
}

// paneText flattens a tab's content to plain text, for a test that wants to
// assert on what a tab SAYS rather than where it lands.
//
// The two shapes have to be rendered differently — a comp.Detail lays itself out
// into a rect, lines are already lines — which is exactly why the seam exists,
// so a helper is the honest way for a test to ignore it.
func paneText(content paneContent) string {
	// The width a 132-column terminal gives the pane, roughly. A parameter
	// until every caller passed the same number, which is a parameter pretending
	// the tests vary something they do not — a test that wants a narrow render
	// belongs in the narrow-terminal golden run, where the whole frame is narrow.
	const width = 90

	if content.detail != nil {
		c := comp.NewCanvas(width, 200)
		content.detail.Draw(c, c.Bounds(), comp.Region(regBody))
		return harness.Strip(c.String())
	}
	var b strings.Builder
	for _, row := range content.lines {
		b.WriteString(harness.Strip(row.Text) + "\n")
	}
	return b.String()
}
