package engine

import (
	"testing"

	"github.com/richarddavenport/tuikit/guard"
)

// The engine has never heard of a terminal.
//
// No colour, no width, no keys, no framework. pgctl kept this split by hand
// from the first commit — the CLI and the TUI are peers over this package, and
// a refusal that lived in a view would be a refusal the other front end did
// not have. Holding it by test rather than by habit is what makes it survive
// the next person in a hurry.
func TestTheEngineHasNeverHeardOfATerminal(t *testing.T) {
	guard.Engine(t, ".")
}
