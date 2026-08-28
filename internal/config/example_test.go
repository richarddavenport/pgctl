package config

import (
	"os"
	"testing"
)

// The example config is documentation that can be wrong, so it is parsed by the
// real loader with strict field checking. A key renamed in the code and not in
// the example fails here rather than in someone's terminal.
func TestExampleConfigParses(t *testing.T) {
	data, err := os.ReadFile("../../pgctl.example.yaml")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	cfg, err := Parse(data, t.TempDir())
	if err != nil {
		t.Fatalf("parse example: %v", err)
	}

	// Both connection forms, and the flags that make one safe.
	for _, name := range []string{"latest", "qat", "prd", "local"} {
		if _, ok := cfg.Lookup(name); !ok {
			t.Errorf("connection %q missing", name)
		}
	}
	if prd, _ := cfg.Lookup("prd"); !prd.Protected {
		t.Error("prd is not protected in the example")
	}
	if qat, _ := cfg.Lookup("qat"); !qat.Guarded {
		t.Error("qat is not guarded in the example")
	}
	if latest, _ := cfg.Lookup("latest"); latest.DSN != "service=latest" {
		t.Errorf("the bare-string shorthand did not become a DSN: %q", latest.DSN)
	}

	// No secret may appear in a config that gets committed.
	for _, conn := range cfg.All() {
		if containsFold(conn.DSN, "password") {
			t.Errorf("connection %q has a password in its DSN", conn.Name)
		}
	}

	if len(cfg.Sets) == 0 {
		t.Error("the example declares no sets")
	}
	if r := cfg.RuleFor("quotes.quote"); r.Data != DataFiltered || r.Where == "" {
		t.Errorf("quotes.quote rule = %+v, want a filtered rule with a predicate", r)
	}
	if r := cfg.RuleFor("audit.logged_actions"); r.Data != DataNone {
		t.Errorf("audit.logged_actions rule = %+v, want data: none", r)
	}
	for _, table := range []string{"hdb_catalog.event_log", "hdb_catalog.event_invocation_logs"} {
		if r := cfg.RuleFor(table); r.Data != DataNone {
			t.Errorf("%s rule = %+v, want data: none", table, r)
		}
	}
	// hdb_catalog's own bookkeeping must survive: dropping it would leave
	// Hasura unable to find its metadata.
	if r := cfg.RuleFor("hdb_catalog.hdb_source_catalog_version"); r.Data != DataAll {
		t.Errorf("hdb_source_catalog_version rule = %+v, want the default", r)
	}

	// No schema is excluded, which the example explains at length.
	if len(cfg.Databases.ExcludeSchemas) != 0 {
		t.Errorf("the example excludes schemas: %v", cfg.Databases.ExcludeSchemas)
	}
	if cfg.ManagesDatabase("postgres") {
		t.Error("the maintenance database is not excluded")
	}
}

func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFold(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
