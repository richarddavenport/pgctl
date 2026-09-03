package tui

import (
	"strings"
	"testing"

	"github.com/richarddavenport/tuikit/app"
	"github.com/richarddavenport/tuikit/comp"
	"github.com/richarddavenport/tuikit/harness"
)

// states is every distinct thing pgctl can be showing.
//
// ONE list, walked by the goldens, the narrow-terminal run, the colour check
// and the capture. A screen added without a frame is a screen added without any
// of them, which is the only arrangement in which they cannot drift apart.
//
// The build funcs reach the state by pressing keys rather than by setting
// fields, wherever a person could: a frame reachable only by reaching into the
// model is a frame that may not be reachable at all.
// state is one thing pgctl can be showing.
type state struct {
	name  string
	build func(w, h int) *app.Runner
}

func states(t *testing.T) []state {
	// The size is set BEFORE the keys are pressed, and that is not a detail: a
	// state reached at 132x38 and then resized to 80x24 is not the state a
	// person on an eighty-column terminal reaches. The help overlay is the case
	// that proved it — it fits at 38 lines with nothing to scroll, so a
	// "scrolled" frame built at the wide size and rendered narrow showed the
	// top of the list and looked like a broken scroll.
	// A Runner, not a Model. Keys go through it so that every press REDRAWS,
	// which a running program does and which the components now depend on: a
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
		{"runs", func(w, h int) *app.Runner { return keys(loaded(w, h), "5") }},

		// An environment pgctl could not reach. The detail pane has to explain
		// itself rather than render an empty form.
		{"unreachable", func(w, h int) *app.Runner { return keys(loaded(w, h), "down", "down") }},

		// The modal forms, which are where an operator does damage.
		{"form-snapshot", func(w, h int) *app.Runner { return keys(loaded(w, h), "n") }},
		{"form-apply", func(w, h int) *app.Runner { return keys(loaded(w, h), "3", "a") }},
		{"form-move", func(w, h int) *app.Runner { return keys(loaded(w, h), "m") }},
		{"form-prune", func(w, h int) *app.Runner { return keys(loaded(w, h), "p") }},

		{"help", func(w, h int) *app.Runner { return keys(loaded(w, h), "?") }},
		// The help scrolled to the bottom. The list is longer than a short
		// terminal, so the state that matters is the one where the last group
		// is reachable at all.
		{"help-scrolled", func(w, h int) *app.Runner {
			r := keys(loaded(w, h), "?")
			for i := 0; i < 20; i++ {
				harness.Press(r, "j")
			}
			return r
		}},
		{"filter", func(w, h int) *app.Runner { return keys(loaded(w, h), "/", "q") }},
		{"filter-matches-nothing", func(w, h int) *app.Runner { return keys(loaded(w, h), "/", "z", "z") }},

		// Nothing loaded: no probe has answered and there are no snapshots.
		// Every panel's empty state at once, which is the first thing a new
		// user sees and the last thing anybody looks at.
		{"empty", func(w, h int) *app.Runner { return run(fixtureModel(t), w, h) }},
	}

	// Every tab body, generated from the panels rather than listed.
	//
	// Listed, four of the fourteen had a frame and the other ten did not — and
	// this list's own comment claimed otherwise. A tab added to paneTabs now
	// gets a frame without anybody adding one, which is the only arrangement
	// in which "a screen added without a frame is a screen added without any
	// of them" is true rather than aspirational.
	for panel := 0; panel < panelCount; panel++ {
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
// columns is the whole class.
func TestColourDoesNotChangeTheShape(t *testing.T) {
	for _, size := range []struct{ w, h int }{{132, 38}, {80, 24}} {
		for _, st := range states(t) {
			harness.ShapeSurvivesColour(t, st.name, func() string {
				return st.build(size.w, size.h).View()
			})
		}
	}
}

// PGCTL_FRAMES=/tmp/pgctl-frames go test ./internal/tui -run CaptureFrames
//
// Off unless asked, because writing files is not what `go test ./...` is for.
// `tuikit frames <dir>` turns the captures into a page you can look at.
//
// This is the fixture half. screenshot_probe_test.go is the live half, and both
// are kept: a fixture encodes the author's assumptions, which is exactly what a
// capture is meant to catch.
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
// The two shapes have to be rendered differently — a comp.Detail lays itself
// out into a rect, lines are already lines — which is exactly why the seam
// exists, so a helper is the honest way for a test to ignore it.
func paneText(content paneContent, width int) string {
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
