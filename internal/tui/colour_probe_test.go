package tui

import (
	"strings"
	"testing"

	"github.com/richarddavenport/tuikit/harness"
)

// TestTheDetailPaneHasNoColourYet records what the canvas port has not finished.
//
// An instrument, not an assertion, and it exists because nothing else can see
// this: the goldens are colour-stripped, and TestColourDoesNotChangeTheShape
// only asks whether colour changes the SHAPE. A detail pane drawn entirely in
// the terminal's foreground passes both.
//
// The cause is one line in drawPane and one in drawOverlay: detail.go and
// actionview.go still build styled strings, and the canvas draws clusters into
// cells rather than replaying escape sequences, so those strings are stripped
// on the way in. The fix is for the tab renderers to emit []comp.Row with
// spans, the way rows.go now does — 528 lines of detail.go, and its own commit.
//
// Run with -v. When it reports nothing, delete it.
func TestTheDetailPaneHasNoColourYet(t *testing.T) {
	m := fixtureModel(t)
	fixtureSnapshot(t, m)
	r := run(m, 132, 38)
	harness.Press(r, "3", "right")

	frame := r.View()
	var plain int
	for _, line := range harness.Lines(frame) {
		// Rows of the detail pane sit right of the panel column. A line with
		// content there and no escape sequence in it is a line drawn in one
		// colour.
		if len(line) > leftWidth && !strings.Contains(line, "\x1b[") {
			plain++
		}
	}
	if plain > 0 {
		t.Logf("%d lines of the frame carry no colour — detail.go and actionview.go "+
			"still build styled strings, which the canvas strips. See drawPane.", plain)
	}
}
