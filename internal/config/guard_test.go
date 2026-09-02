package config

import (
	"testing"

	"github.com/richarddavenport/tuikit/guard"
)

// This package is engine-side: it has never heard of a terminal.
//
// pgctl's domain is spread across five packages, not one, so guarding only
// internal/engine would guard a fifth of the split. See the same test there.
func TestThisPackageHasNeverHeardOfATerminal(t *testing.T) {
	guard.Engine(t, ".")
}
