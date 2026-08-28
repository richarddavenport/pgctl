package config

import "strings"

// MatchPattern reports whether a schema-qualified table name matches a
// pattern. The table part may carry a `*` at its start, its end, or both:
//
//	operations.policy_contract    exactly that table
//	operations.policy_contract*   anything starting with it
//	hdb_catalog.*_log             anything ending with it
//	hdb_catalog.*event*           anything containing it
//	audit.*                       every table in the schema
//
// Deliberately not glob or regexp. A pattern that selects tables for
// truncation has to be legible at a glance to whoever reviews the config, and
// these four forms cover everything the rules in practice need — including the
// `*_log*` and `*event*` exclusions the shell scripts this replaces used.
func MatchPattern(pattern, table string) bool {
	pSchema, pTable, ok := strings.Cut(pattern, ".")
	if !ok {
		return false
	}
	tSchema, tTable, ok := strings.Cut(table, ".")
	if !ok || pSchema != tSchema {
		return false
	}

	body, leading := strings.CutPrefix(pTable, "*")
	body, trailing := strings.CutSuffix(body, "*")
	switch {
	case leading && trailing:
		return strings.Contains(tTable, body)
	case leading:
		return strings.HasSuffix(tTable, body)
	case trailing:
		return strings.HasPrefix(tTable, body)
	default:
		return pTable == tTable
	}
}

// Matches reports whether a table belongs to the set: included by some
// include pattern and excluded by none.
func (s Set) Matches(table string) bool {
	for _, pat := range s.Exclude {
		if MatchPattern(pat, table) {
			return false
		}
	}
	for _, pat := range s.Include {
		if MatchPattern(pat, table) {
			return true
		}
	}
	return false
}

// RuleFor returns the effective rule for a table. Later rules win, so a config
// can state a schema-wide default and then except a table from it; a table
// with no matching rule gets DataAll.
//
// The returned rule's Data is always resolved — never the empty string — so
// callers never have to repeat the "Where implies filtered" inference.
func (c *Config) RuleFor(table string) Rule {
	effective := Rule{Table: table, Data: DataAll}
	for _, r := range c.Rules {
		if !MatchPattern(r.Table, table) {
			continue
		}
		effective = r
	}
	if effective.Data == "" {
		if effective.Where != "" {
			effective.Data = DataFiltered
		} else {
			effective.Data = DataAll
		}
	}
	return effective
}
