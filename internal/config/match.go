package config

import "strings"

// MatchPattern reports whether a schema-qualified table name matches a
// pattern. Patterns are exact, or a trailing `*` on the table part
// (`operations.policy_contract*`), or a whole schema (`audit.*`).
//
// Deliberately not glob or regexp: a pattern that selects tables for
// truncation should be legible at a glance to whoever reviews the config, and
// `*` at the end is the only form anyone has needed.
func MatchPattern(pattern, table string) bool {
	pSchema, pTable, ok := strings.Cut(pattern, ".")
	if !ok {
		return false
	}
	tSchema, tTable, ok := strings.Cut(table, ".")
	if !ok || pSchema != tSchema {
		return false
	}
	if prefix, wild := strings.CutSuffix(pTable, "*"); wild {
		return strings.HasPrefix(tTable, prefix)
	}
	return pTable == tTable
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
