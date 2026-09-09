package tui

import (
	"testing"

	"github.com/richarddavenport/tuikit/guard"
)

// The whole of what pgctl has to write to keep its interface in its vocabulary:
// a colour that did not come from the palette, a character the glyph set does
// not cover, or chrome the glyph set cannot print, is a test failure rather than
// something to notice in review.
func TestTheInterfaceStaysInItsVocabulary(t *testing.T) {
	guard.Tokens(t, ".", Palette)
	guard.Glyphs(t, ".", Glyphs)
	guard.Chrome(t, Chrome, Glyphs)
	// And the other direction: a chrome character typed out HERE rather than
	// read from the chrome. guard.Glyphs cannot see it, because the character is
	// allowed — being in the box set is the point of it. What is wrong is the
	// source: hardcode a ─ and it stays a ─ on a box set that draws -, beside
	// everything else that changed.
	guard.Furniture(t, ".", Chrome)
}
