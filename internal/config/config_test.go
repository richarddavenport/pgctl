package config

import (
	"strings"
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
	// An empty config has exactly one destination: this machine.
	if got := cfg.Destinations(); len(got) != 1 || got[0] != LocalStorage {
		t.Errorf("destinations = %v, want just %q", got, LocalStorage)
	}
}

// The single-remote keys are refused by name, with the shape to write instead.
//
// A config half-migrated is worse than one that will not load: it would keep
// pushing to the destination it always did while the interface offered a list
// that did not include it.
func TestTheOldStorageKeysAreRefusedWithTheNewShape(t *testing.T) {
	_, err := Parse([]byte("storage:\n  kind: azureblob\n  container: pg-snapshots\n"), t.TempDir())
	if err == nil {
		t.Fatal("storage.kind loaded; it is no longer read")
	}
	for _, want := range []string{"storage.kind", "storage.remotes", "pg-snapshots"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%s", want, err)
		}
	}
}

// Two destinations, named, each with its own retention.
func TestRemotesAreNamedAndCarryTheirOwnRetention(t *testing.T) {
	cfg, err := Parse([]byte(`
storage:
  dir: .pgctl/snapshots
  retention: { daily: 2 }
  remotes:
    - name: snapshots
      kind: azureblob
      container: pg-snapshots
      retention: { daily: 7, weekly: 4 }
    - name: archive
      container: pg-archive
      retention: { monthly: 12 }
`), t.TempDir())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if got := cfg.Destinations(); strings.Join(got, ",") != "local,snapshots,archive" {
		t.Errorf("destinations = %v, want local first then config order", got)
	}
	// kind defaults, because there is one.
	archive, ok := cfg.RemoteByName("archive")
	if !ok {
		t.Fatal("no remote named archive")
	}
	if archive.Kind != StorageAzureBlob {
		t.Errorf("archive kind = %q, want the default", archive.Kind)
	}
	if archive.AccountEnv != "AZURE_STORAGE_ACCOUNT" || archive.KeyEnv != "AZURE_STORAGE_KEY" {
		t.Errorf("archive credentials = %q/%q, want the az defaults",
			archive.AccountEnv, archive.KeyEnv)
	}
	if archive.Retention.Monthly != 12 || archive.Retention.Daily != 0 {
		t.Errorf("archive retention = %+v, want twelve monthly and nothing else",
			archive.Retention)
	}
	if cfg.Storage.Retention.Daily != 2 {
		t.Errorf("local retention = %+v, want the two days declared for it",
			cfg.Storage.Retention)
	}
}

// A remote may not be called "local", and two may not share a name: the name is
// the only handle a destination has.
func TestRemoteNamesAreUnique(t *testing.T) {
	for _, body := range []string{
		"storage:\n  remotes:\n    - name: local\n      container: c\n",
		"storage:\n  remotes:\n    - name: a\n      container: c\n    - name: a\n      container: d\n",
		"storage:\n  remotes:\n    - container: c\n",
	} {
		if _, err := Parse([]byte(body), t.TempDir()); err == nil {
			t.Errorf("accepted:\n%s", body)
		}
	}
}

func TestUnknownKeysAreRejected(t *testing.T) {
	// A typo in a config that silently does nothing is worse than one that
	// refuses to start.
	if _, err := Parse([]byte("connectons:\n  qat: x\n"), t.TempDir()); err == nil {
		t.Error("a misspelled top-level key was accepted")
	}
}
