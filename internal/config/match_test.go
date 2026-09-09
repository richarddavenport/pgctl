package config

import (
	"strings"
	"testing"
)

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
		{"hdb_catalog.*_log*", "hdb_catalog.event_invocation_logs", true},
		{"hdb_catalog.*_log*", "hdb_catalog.event_log", true},
		{"hdb_catalog.*_log*", "hdb_catalog.hdb_event_log_cleanups", true},
		// The underscore is load-bearing: hdb_source_catalog_version contains
		// "log" but not "_log", and blanking it breaks Hasura.
		{"hdb_catalog.*_log*", "hdb_catalog.hdb_source_catalog_version", false},
		{"hdb_catalog.*log*", "hdb_catalog.hdb_source_catalog_version", true},
		{"operations.policy", "policy", false},

		// A wildcard schema, which the first real config wanted on its first
		// day: nine as400 tables across five schemas, and five rules would
		// stop covering them the day a sixth schema gained one.
		{"*.*as400*", "claims.policy_claim_as400", true},
		{"*.*as400*", "operations.as400_contracts", true},
		{"*.*as400*", "shared.payment_as400", true},
		{"*.*as400*", "claims.policy_claim", false},
		// The same thing without the `*.`, which is the form somebody actually
		// writes.
		{"*as400*", "ory.identity_as400", true},
		{"*as400*", "public.as400_data_sync", true},
		{"*as400*", "public.address", false},
		// A wildcarded schema half, for a project whose schemas are prefixed.
		{"claims*.*", "claims_archive.invoice", true},
		{"claims*.*", "operations.invoice", false},
		// And the safety that is kept: a bare EXACT name is not a statement
		// about every schema, it is a table somebody forgot to qualify.
		{"policy", "operations.policy", false},
		{"*.policy", "operations.policy", true},
		{"*.policy", "claims.policy_claim", false},
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

// The pattern shapes the validator accepts and refuses.
//
// Paired with TestMatchPattern deliberately: a shape the matcher understands and
// the validator rejects is a feature nobody can use, and the reverse is a
// pattern that loads and matches nothing.
func TestValidPatternShapes(t *testing.T) {
	for _, pat := range []string{
		"operations.policy_contract",
		"operations.*",
		"hdb_catalog.*_log*",
		"*.*as400*",
		"*as400*",
		"claims*.*",
		"*.policy",
	} {
		if err := validPattern(pat); err != nil {
			t.Errorf("validPattern(%q) = %v, want it accepted", pat, err)
		}
	}

	for _, pat := range []string{
		"",
		"policy",            // unqualified exact name
		"operations.",       // empty half
		".policy",           // empty half
		"oper*ions.policy",  // `*` in the middle of the schema
		"operations.pol*cy", // `*` in the middle of the table
	} {
		if err := validPattern(pat); err == nil {
			t.Errorf("validPattern(%q) accepted", pat)
		}
	}
}

// The unqualified-name error says how to mean "every schema", because that is
// the next thing the reader wants and the answer is not guessable.
func TestTheUnqualifiedPatternErrorTeachesTheWildcard(t *testing.T) {
	err := validPattern("policy")
	if err == nil {
		t.Fatal("accepted an unqualified name")
	}
	if !strings.Contains(err.Error(), "*.policy") {
		t.Errorf("err = %v, does not offer the wildcard form", err)
	}
}
