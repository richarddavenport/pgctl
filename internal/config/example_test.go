package config

import (
	"os"
	"path/filepath"
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
	// The example takes its environments from swarmctl's config, so the test
	// supplies one — which also exercises the merge.
	dir := t.TempDir()
	swarmctl := []byte(`
environments:
  - name: latest
    domain: mbpi-latest.com
    ssh: { host: mbpi-latest.com, port: 50120 }
    secrets: { file: envs/latest.env }
  - name: qat
    domain: mbpi-qat.com
    ssh: { host: mbpi-qat.com, port: 50120 }
    secrets: { file: envs/qat.env }
  - name: prd
    domain: mbpnetwork.com
    guarded: true
    secrets: { file: envs/prd.env }
`)
	if err := os.WriteFile(filepath.Join(dir, "swarmctl.yaml"), swarmctl, 0o600); err != nil {
		t.Fatalf("write stub swarmctl config: %v", err)
	}

	cfg, err := Parse(data, dir)
	if err != nil {
		t.Fatalf("parse example: %v", err)
	}

	// Environments arrive from swarmctl, and `local` from pgctl's own postgres
	// block, so the picker shows all four.
	for _, name := range []string{"latest", "qat", "prd", "local"} {
		if _, ok := cfg.LookupEnv(name); !ok {
			t.Errorf("environment %q missing after the merge", name)
		}
	}
	// prd is guarded upstream and protected here; nothing else is a target
	// nobody may write to.
	if env, _ := cfg.LookupEnv("qat"); env.Protected {
		t.Error("qat came out protected, which would make a QAT refresh impossible")
	}
	if env, _ := cfg.LookupEnv("prd"); !env.Guarded {
		t.Error("prd lost its guarded flag in the merge")
	}

	if len(cfg.Databases) == 0 {
		t.Error("the example declares no databases")
	}
	if len(cfg.Sets) == 0 {
		t.Error("the example declares no sets")
	}

	// The rule that motivated the whole filtered-COPY mechanism.
	if r := cfg.RuleFor("quotes.quote"); r.Data != DataFiltered || r.Where == "" {
		t.Errorf("quotes.quote rule = %+v, want a filtered rule with a predicate", r)
	}
	if r := cfg.RuleFor("audit.logged_actions"); r.Data != DataNone {
		t.Errorf("audit.logged_actions rule = %+v, want data: none", r)
	}
	// The Hasura exclusions have to actually match the tables that exist.
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

	// Every environment named in `protect:` must exist, or the protection is a
	// typo that protects nothing.
	if cfg.Protect != nil {
		for _, name := range *cfg.Protect {
			env, ok := cfg.LookupEnv(name)
			if !ok {
				t.Errorf("protect names %q, which is not an environment in this config", name)
				continue
			}
			if !env.Protected {
				t.Errorf("%q is in protect but did not come out protected", name)
			}
		}
	}
}
