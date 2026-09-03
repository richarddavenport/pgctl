package cli

import (
	"testing"

	"github.com/richarddavenport/tuikit/guard"
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
