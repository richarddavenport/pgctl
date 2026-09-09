package cli

import (
	"testing"

	"github.com/richarddavenport/tuikit/guard"

	"github.com/richarddavenport/pgctl/internal/tui"
)

// Every action reachable by mouse has a keyboard path.
//
// It lives here rather than beside the interface's other guards because it
// reads the command tree, and the tree is this package's — internal/tui imports
// nothing from internal/cli, which is what keeps the interface from knowing how
// it was launched.
//
// The bug it prevents is not theoretical: a command whose only path is a
// context menu is unreachable for anyone running pgctl inside tmux or a
// terminal that keeps right-click for itself, and pgctl cannot detect that to
// say so. An agent cannot click either.
func TestEveryActionHasAKeyboardPath(t *testing.T) {
	guard.Reachable(t, Commands())
}

// No command binds a key that means something else everywhere.
//
// q quits, ? helps, esc leaves. A tool that binds one of them to an action sets
// a trap using every other tool's credibility — and pgctl's actions are things
// like "replace this database", so the trap is expensive.
func TestNoCommandStealsAReservedKey(t *testing.T) {
	guard.Reserved(t, Commands())
}

// Every command with a key is documented on the help screen.
//
// The failure it catches is quiet and corrosive: a command declares Key "a",
// the help screen does not list it, and a reader concludes the key does not
// exist. Both sides come from one declaration each — the command tree and
// tui.KeySections — and this is what holds them together.
func TestEveryCommandKeyIsInTheHelp(t *testing.T) {
	guard.Keys(t, Commands(), tui.KeySections())
}
