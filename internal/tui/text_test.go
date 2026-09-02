package tui

import (
	"strings"
	"testing"

	"github.com/richarddavenport/tuikit/comp"
)

// The text helpers measure COLUMNS, not bytes or runes.
//
// These pin the bugs the old versions had, because the goldens do not: the
// fixture's strings happen not to trigger them, and "no golden changed" was the
// result of swapping all four helpers onto comp. A fixture cannot fail on a case
// it does not contain.
func TestWrapMeasuresColumnsNotBytes(t *testing.T) {
	// An em dash is one column and three bytes. The old wrap compared
	// len(line)+1+len(word) against the width, so a line of these broke early
	// by two columns for every dash on it — and detail.go is full of them.
	//
	// The width has to be above wrap's own floor of 20 to see it: at 13 the
	// floor rounds up to 20, the whole string fits in 20 BYTES too, and the
	// bug hides. This exact string at this exact width is 29 columns and 43
	// bytes, and the old version split it after "e —".
	const s = "a — b — c — d — e — f — g — h"
	if got := comp.Width(s); got != 29 {
		t.Fatalf("the fixture string is %d columns, not the 29 this test assumes", got)
	}
	if got := wrap(s, 29); got != s {
		t.Errorf("wrap(%q, 29) = %q\nit is exactly 29 columns and should not have wrapped", s, got)
	}
	for _, line := range strings.Split(wrap(s, 20), "\n") {
		if w := comp.Width(line); w > 20 {
			t.Errorf("wrap to 20 produced a %d-column line %q", w, line)
		}
	}
}

func TestPadToMeasuresColumns(t *testing.T) {
	// The key column in the help overlay was %-12s, which pads to twelve BYTES:
	// "↑ ↓" is three columns and seven bytes, so it came out five columns short
	// and every description after it sat in the wrong place.
	if got := comp.Width(padTo("↑ ↓", 12)); got != 12 {
		t.Errorf("padTo(%q, 12) is %d columns, want 12", "↑ ↓", got)
	}
	// Longer than the width is left alone here, unlike comp.Pad, because
	// callers clip separately and a pad that truncated would hide an overflow.
	if got := padTo("0123456789", 4); got != "0123456789" {
		t.Errorf("padTo shortened a long string to %q", got)
	}
}

func TestClipDoesNotCutAnEscapeSequence(t *testing.T) {
	// clip cuts the screen behind a modal. The old version cut by runes, so it
	// could end a line inside an escape sequence and leave every row after it
	// wearing that colour. The clipped result must be no wider than asked and
	// must not end mid-sequence.
	line := accentStyle.Render("aaaaaaaaaa") + mutedStyle.Render("bbbbbbbbbb")
	got := clip(line, 12)
	if w := comp.Width(got); w > 12 {
		t.Errorf("clip to 12 columns produced %d", w)
	}
	if strings.Count(got, "\x1b[") > 0 && !strings.HasSuffix(got, "m") &&
		strings.LastIndex(got, "\x1b[") > len(got)-8 {
		t.Errorf("clip ended inside an escape sequence: %q", got)
	}
	// And no ellipsis, which would draw against the modal's left edge on
	// every row.
	if strings.Contains(got, "…") {
		t.Error("clip added an ellipsis")
	}
}

func TestSpinnerAgreesWithTheTickRate(t *testing.T) {
	// The spinner's frame comes from the clock, so two spinners on one screen
	// turn together and a frozen clock gives a deterministic frame. If Every
	// and tickInterval disagree the spinner jumps several frames per redraw.
	a := spinner(epoch)
	if b := spinner(epoch); a != b {
		t.Errorf("the same instant gave two frames, %q and %q", a, b)
	}
	if c := spinner(epoch.Add(tickInterval)); c == a {
		t.Errorf("one tick did not advance the frame: still %q", a)
	}
	if d := spinner(epoch.Add(tickInterval / 4)); d != a {
		t.Errorf("a quarter tick advanced the frame from %q to %q", a, d)
	}
}
