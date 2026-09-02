package tui

import (
	"testing"

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
func states(t *testing.T) []struct {
	name  string
	build func() *Model
} {
	loaded := func() *Model {
		m := fixtureModel(t)
		fixtureSnapshot(t, m)
		return m
	}
	keys := func(m *Model, k ...string) *Model {
		harness.Press(m, k...)
		return m
	}
	return []struct {
		name  string
		build func() *Model
	}{
		{"connections", loaded},
		{"databases", func() *Model { return keys(loaded(), "2") }},
		{"snapshots", func() *Model { return keys(loaded(), "3") }},
		{"sets", func() *Model { return keys(loaded(), "4") }},
		{"runs", func() *Model { return keys(loaded(), "5") }},

		// The right pane with the keys, and its tabs. Remembered per panel, so
		// the tab strip is part of what a panel means.
		{"detail-pane", func() *Model { return keys(loaded(), "right") }},
		{"snapshot-tables", func() *Model { return keys(loaded(), "3", "right", "tab") }},
		{"snapshot-warnings", func() *Model { return keys(loaded(), "3", "right", "tab", "tab") }},
		{"snapshot-drift", func() *Model { return keys(loaded(), "3", "right", "tab", "tab", "tab") }},

		// An environment pgctl could not reach. The detail pane has to explain
		// itself rather than render an empty form.
		{"unreachable", func() *Model { return keys(loaded(), "down", "down") }},

		// The modal forms, which are where an operator does damage.
		{"form-snapshot", func() *Model { return keys(loaded(), "n") }},
		{"form-apply", func() *Model { return keys(loaded(), "3", "a") }},
		{"form-move", func() *Model { return keys(loaded(), "m") }},
		{"form-prune", func() *Model { return keys(loaded(), "p") }},

		{"help", func() *Model { return keys(loaded(), "?") }},
		{"filter", func() *Model { return keys(loaded(), "/", "q") }},
		{"filter-matches-nothing", func() *Model { return keys(loaded(), "/", "z", "z") }},

		// Nothing loaded: no probe has answered and there are no snapshots.
		// Every panel's empty state at once, which is the first thing a new
		// user sees and the last thing anybody looks at.
		{"empty", func() *Model { return fixtureModel(t) }},
	}
}

func sized(m *Model, w, h int) *Model {
	m.SetSize(w, h)
	return m
}

// The screens as goldens: a layout change is an ordinary test failure.
//
// Run with -update-goldens when the change is intended, and read the diff.
func TestFramesMatchTheirGoldens(t *testing.T) {
	for _, st := range states(t) {
		t.Run(st.name, func(t *testing.T) {
			harness.Golden(t, "testdata", st.name, sized(st.build(), 132, 38).View())
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
			harness.Golden(t, "testdata/80", st.name, sized(st.build(), 80, 24).View())
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
				return sized(st.build(), size.w, size.h).View()
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
		s.Shot(st.name, st.build())
	}
	s.Done()
}
