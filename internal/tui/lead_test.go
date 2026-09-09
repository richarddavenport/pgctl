package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/richarddavenport/tuikit/harness"
)

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// The reachability glyph keeps its colour under the selection highlight.
//
// Reported by a person using it — "when highlighting I can't see the color of
// the dot" — and it was not a legibility nicety: a black ● on light grey reads
// as a DIFFERENT state, off or disabled, rather than as a green one that
// happens to be selected. The one row a reader is looking at was the one row
// whose status they could not read. tuikit #44, fixed by Row.LeadStyle.
func TestTheSelectedRowKeepsItsStateColour(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)

	m := fixtureModel(t)
	fixtureSnapshot(t, m)
	r := run(m, 100, 24)

	// prd is row 0 and the cursor starts there, so the first Connections row
	// is the selected one.
	var selected string
	for _, line := range harness.Lines(r.View()) {
		if strings.Contains(harness.Strip(line), "prd") &&
			strings.Contains(harness.Strip(line), "protected") {
			selected = line
			if i := strings.Index(line, "│ ‹"); i > 0 {
				selected = line[:i]
			}
			break
		}
	}
	if selected == "" {
		t.Fatal("did not find the selected connection row")
	}

	codes := strings.Join(sgr.FindAllString(selected, -1), " ")
	// The selection is reverse video — SelectionFG 0 on SelectionBG 7 — so the
	// highlight is still there.
	if !strings.Contains(codes, "47m") {
		t.Errorf("the row is not highlighted: %q", codes)
	}
	// And the status glyph is still green: prd is reachable in the fixture.
	if !strings.Contains(codes, "32m") {
		t.Errorf("the ● lost its colour under the highlight.\ncodes: %s", codes)
	}
}
