package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Home is ~/.config/pgctl rather than os.UserConfigDir()/pgctl, which on
// macOS is ~/Library/Application Support — where a GUI keeps state, not where
// anyone looks for a file they edit by hand.
func TestHomeIsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := Home(); got != "/xdg/pgctl" {
		t.Errorf("Home() = %q, want /xdg/pgctl", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, want := Home(), filepath.Join(home, ".config", "pgctl"); got != want {
		t.Errorf("Home() = %q, want %q", got, want)
	}
}
