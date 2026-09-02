package config

import (
	"os"
	"path/filepath"
	"testing"
)

// UseServiceFile is the whole of how pgctl moves the service file without
// inventing a configuration format, so the three things it refuses to do
// matter as much as the one thing it does.
func TestUseServiceFile(t *testing.T) {
	write := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", dir)
		path := filepath.Join(dir, "pgctl", "pg_service.conf")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("[prd]\nhost=example\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("points libpq at the file beside the config", func(t *testing.T) {
		path := write(t)
		t.Setenv("PGSERVICEFILE", "")
		UseServiceFile()
		if got := os.Getenv("PGSERVICEFILE"); got != path {
			t.Errorf("PGSERVICEFILE = %q, want %q", got, path)
		}
	})

	t.Run("does not override one already set", func(t *testing.T) {
		write(t)
		t.Setenv("PGSERVICEFILE", "/somewhere/else.conf")
		UseServiceFile()
		// Someone who has told libpq where their services are has said
		// something more specific than a default.
		if got := os.Getenv("PGSERVICEFILE"); got != "/somewhere/else.conf" {
			t.Errorf("PGSERVICEFILE = %q, want the value already set", got)
		}
	})

	t.Run("does nothing when there is no file", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("PGSERVICEFILE", "")
		UseServiceFile()
		// Unset, not set-to-a-missing-path: libpq falls back to
		// ~/.pg_service.conf as it always has, so a machine set up the old way
		// keeps working. Pointing it at a file that is not there would break
		// that fallback and report nothing.
		if got := os.Getenv("PGSERVICEFILE"); got != "" {
			t.Errorf("PGSERVICEFILE = %q, want it left alone", got)
		}
	})
}

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
