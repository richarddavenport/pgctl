package tui

import (
	"testing"

	"github.com/richarddavenport/tuikit/harness"
)

// TestFitProbe reports every frame that does not fit the terminal it was drawn
// for. An instrument, not an assertion: run it with -v and read it.
//
// It is a probe rather than a test because it currently has findings. See the
// commit that follows this one; when they are fixed it becomes an assertion and
// this comment goes.
func TestFitProbe(t *testing.T) {
	for _, size := range []struct{ w, h int }{{132, 38}, {80, 24}} {
		for _, st := range states(t) {
			frame := sized(st.build(), size.w, size.h).View()
			if w := harness.Width(frame); w > size.w {
				t.Logf("%-24s at %dx%d: %d columns wide, %d too many",
					st.name, size.w, size.h, w, w-size.w)
			}
			if h := len(harness.Lines(frame)); h > size.h {
				t.Logf("%-24s at %dx%d: %d lines, %d too many",
					st.name, size.w, size.h, h, h-size.h)
			}
		}
	}
}
