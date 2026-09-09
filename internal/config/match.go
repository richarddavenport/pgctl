package config

import "strings"

// MatchPattern reports whether a schema-qualified table name matches a pattern.
//
// Either part may carry a `*` at its start, its end, or both, and a pattern
// with no `.` in it matches that table name in EVERY schema:
//
//	operations.policy_contract    exactly that table
//	operations.policy_contract*   anything in operations starting with it
//	hdb_catalog.*_log             anything in hdb_catalog ending with it
//	audit.*                       every table in audit
//	*.*as400*                     anything containing as400, in any schema
//	*as400*                       the same thing, said shorter
//
// Deliberately not glob or regexp. A pattern that selects tables for truncation
// has to be legible at a glance to whoever reviews the config, and these forms
// cover everything the rules in practice need — including the `*_log*` and
// `*event*` exclusions the shell scripts this replaces used.
//
// # Why the schema may be a wildcard
//
// It could not be, and the reasoning was that a rule should never be able to
// reach into a schema it does not name. The first real config wanted the
// opposite on its first day: tables that sync to an AS/400 are worth nobody's
// backup, and there are nine of them across FIVE schemas — claims, operations,
// ory, public and shared. Five rules would cover them and would silently stop
// covering them the day a sixth schema gained one, which is the failure mode a
// rule is supposed to prevent rather than reproduce.
//
// What was traded away is real, so it is worth naming: `*.*log*` blanks tables
// in every schema, and `hdb_catalog.*log*` — the cata·log trap — was already
// the mistake this repo has made once. What replaces the safety is the count:
// the interface's Rules tab says how many tables each rule matched, and the
// manifest records the fate of every table individually.
func MatchPattern(pattern, table string) bool {
	pSchema, pTable := "*", pattern
	if schema, name, ok := strings.Cut(pattern, "."); ok {
		pSchema, pTable = schema, name
	} else if !strings.Contains(pattern, "*") {
		// A bare EXACT name never implies a schema, and that half of the old
		// rule is kept: `policy` is a table somebody forgot to qualify, and the
		// same name in another schema is a different table whose truncation is
		// unrecoverable. A bare pattern with a wildcard in it is different —
		// nobody writes `*as400*` meaning one schema — so that is the form that
		// spans them, and `*.policy` says "every schema" for an exact name.
		return false
	}
	tSchema, tTable, ok := strings.Cut(table, ".")
	if !ok {
		return false
	}
	return matchPart(pSchema, tSchema) && matchPart(pTable, tTable)
}

// matchPart matches one half of a pattern: a name, or a name with `*` at either
// end. `*` alone is every name, which falls out of "contains the empty string"
// rather than being a case of its own.
func matchPart(pattern, name string) bool {
	body, leading := strings.CutPrefix(pattern, "*")
	body, trailing := strings.CutSuffix(body, "*")
	switch {
	case leading && trailing:
		return strings.Contains(name, body)
	case leading:
		return strings.HasSuffix(name, body)
	case trailing:
		return strings.HasPrefix(name, body)
	default:
		return pattern == name
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
	effective.Data = effective.Mode()
	return effective
}
