package tui

import (
	"testing"

	"github.com/richarddavenport/tuikit/harness"
)

// Every frame fits the terminal it was drawn for.
//
// An assertion rather than a probe, which it was for exactly one commit. It
// found four bugs the moment it could see the frames, and the last line of the
// frame is the one that matters most: an overflow there makes the terminal
// scroll, which tears the whole screen rather than clipping one row.
func TestEveryFrameFitsItsTerminal(t *testing.T) {
	for _, size := range []struct{ w, h int }{{132, 38}, {80, 24}} {
		for _, st := range states(t) {
			frame := st.build(size.w, size.h).View()
			if w := harness.Width(frame); w > size.w {
				t.Errorf("%s at %dx%d: %d columns wide, %d too many",
					st.name, size.w, size.h, w, w-size.w)
			}
			if h := len(harness.Lines(frame)); h > size.h {
				t.Errorf("%s at %dx%d: %d lines, %d too many",
					st.name, size.w, size.h, h, h-size.h)
			}
		}
	}
}
