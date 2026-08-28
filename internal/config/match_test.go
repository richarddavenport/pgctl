package config

import "testing"

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern, table string
		want           bool
	}{
		{"operations.policy_contract", "operations.policy_contract", true},
		{"operations.policy_contract", "operations.policy_contract_asset", false},
		{"operations.policy_contract*", "operations.policy_contract_asset", true},
		{"operations.policy_contract*", "operations.policy_contract", true},
		{"audit.*", "audit.logged_actions", true},
		{"audit.*", "claims.logged_actions", false},
		// A schema is never implied: the same table name in another schema is
		// a different table, and truncating the wrong one is unrecoverable.
		{"operations.*", "operations.policy", true},
		{"policy", "operations.policy", false},
		// Leading and surrounding wildcards, which the shell scripts this
		// replaces needed to exclude Hasura's event and log tables.
		{"hdb_catalog.*_log", "hdb_catalog.event_log", true},
		{"hdb_catalog.*_log", "hdb_catalog.event_invocation_logs", false},
		{"hdb_catalog.*event*", "hdb_catalog.event_invocation_logs", true},
		{"hdb_catalog.*event*", "hdb_catalog.hdb_event_log_cleanups", true},
		{"hdb_catalog.*event*", "hdb_catalog.hdb_source_catalog_version", false},
		{"hdb_catalog.*log*", "hdb_catalog.event_invocation_logs", true},
		{"operations.policy", "policy", false},
	}
	for _, c := range cases {
		if got := MatchPattern(c.pattern, c.table); got != c.want {
			t.Errorf("MatchPattern(%q, %q) = %v, want %v", c.pattern, c.table, got, c.want)
		}
	}
}

func TestSetMatchesExcludeWins(t *testing.T) {
	s := Set{
		Include: []string{"operations.*"},
		Exclude: []string{"operations.as400_*"},
	}
	if !s.Matches("operations.policy_contract") {
		t.Error("included table did not match")
	}
	if s.Matches("operations.as400_contracts") {
		t.Error("excluded table matched")
	}
	if s.Matches("claims.policy_claim") {
		t.Error("table outside the set matched")
	}
}

func TestRuleForLaterRuleWins(t *testing.T) {
	c := &Config{Rules: []Rule{
		{Table: "quotes.*", Data: DataNone},
		{Table: "quotes.quote", Where: "created_at > now() - interval '30 days'"},
	}}

	if got := c.RuleFor("quotes.quote_line"); got.Data != DataNone {
		t.Errorf("quotes.quote_line data = %q, want %q", got.Data, DataNone)
	}
	got := c.RuleFor("quotes.quote")
	if got.Data != DataFiltered {
		t.Errorf("quotes.quote data = %q, want %q (a where predicate implies it)", got.Data, DataFiltered)
	}
	if got.Where == "" {
		t.Error("quotes.quote lost its predicate")
	}
	if plain := c.RuleFor("operations.policy"); plain.Data != DataAll {
		t.Errorf("unruled table data = %q, want %q", plain.Data, DataAll)
	}
}
