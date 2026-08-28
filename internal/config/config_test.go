package config

import (
	"testing"
	"time"
)

func TestBareStringIsShorthandForADSN(t *testing.T) {
	// Most connections have nothing to say beyond how to reach the server.
	cfg, err := Parse([]byte(`
connections:
  qat: "service=qat"
  prd:
    dsn: "service=prd"
    protected: true
`), t.TempDir())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	qat, ok := cfg.Lookup("qat")
	if !ok {
		t.Fatal("qat is missing")
	}
	if qat.DSN != "service=qat" {
		t.Errorf("qat dsn = %q", qat.DSN)
	}
	if qat.Name != "qat" {
		t.Errorf("qat name = %q — the map key is the name", qat.Name)
	}
	prd, _ := cfg.Lookup("prd")
	if !prd.Protected {
		t.Error("prd came out unprotected")
	}
}

func TestDeclarationOrderIsKept(t *testing.T) {
	// A YAML mapping has no inherent order, and a picker that reordered the
	// connections on every run would be harder to navigate than one that did
	// not.
	cfg, err := Parse([]byte(`
connections:
  latest: "service=latest"
  qat: "service=qat"
  prd: "service=prd"
`), t.TempDir())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var names []string
	for _, c := range cfg.All() {
		names = append(names, c.Name)
	}
	want := []string{"latest", "qat", "prd"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}

func TestNoConfigStillConnects(t *testing.T) {
	// With nothing declared, pgctl does what psql with no arguments does. This
	// is what makes the tool usable in a directory with no config at all.
	cfg, err := Parse(nil, t.TempDir())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	conn, ok := cfg.Lookup(DefaultConnection)
	if !ok {
		t.Fatalf("no %q connection was invented", DefaultConnection)
	}
	if conn.DSN != "" {
		t.Errorf("the default connection carries a DSN %q; it should say nothing "+
			"and let libpq resolve everything", conn.DSN)
	}
	if conn.MaintenanceDB != "postgres" {
		t.Errorf("maintenance database = %q", conn.MaintenanceDB)
	}
}

func TestDatabasesAreDiscoveredNotDeclared(t *testing.T) {
	cfg, err := Parse([]byte(`
databases:
  exclude: [postgres, scratch_*]
`), t.TempDir())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	for name, want := range map[string]bool{
		"product-development": true,
		"claims":              true,
		// A database nobody mentioned is still managed: the server is the
		// authority on what exists.
		"something-new": true,
		"postgres":      false,
		"scratch_bob":   false,
		// The templates are excluded whatever the config says.
		"template0": false,
		"template1": false,
	} {
		if got := cfg.ManagesDatabase(name); got != want {
			t.Errorf("ManagesDatabase(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDefaults(t *testing.T) {
	cfg, err := Parse(nil, t.TempDir())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Defaults.Compression != "zstd:3" {
		t.Errorf("compression = %q", cfg.Defaults.Compression)
	}
	if cfg.Defaults.Jobs != 4 {
		t.Errorf("jobs = %d", cfg.Defaults.Jobs)
	}
	if cfg.Defaults.LockTimeout != 30*time.Second {
		t.Errorf("lockTimeout = %s", cfg.Defaults.LockTimeout)
	}
	if cfg.Storage.Kind != StorageLocal {
		t.Errorf("storage kind = %q", cfg.Storage.Kind)
	}
}

func TestUnknownKeysAreRejected(t *testing.T) {
	// A typo in a config that silently does nothing is worse than one that
	// refuses to start.
	if _, err := Parse([]byte("connectons:\n  qat: x\n"), t.TempDir()); err == nil {
		t.Error("a misspelled top-level key was accepted")
	}
}
