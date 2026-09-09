package config

import (
	"fmt"
	"strings"
)

// StorageAzureBlob is a remote's kind, and so far the only one. The local
// directory is not a kind: it is always there and is not a remote.
const StorageAzureBlob = "azureblob"

// orDefault is a value or a placeholder, for an error message that shows the
// shape to write rather than describing it.
func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// Validate reports the config problems that would make a run meaningless.
// Problems that only make it worse are recorded in Warnings instead.
func (c *Config) Validate() error {
	// The single-remote keys, refused by name with the shape to write instead.
	// A config half-migrated is worse than one that will not load: it would
	// keep pushing to the destination it always did while the interface offered
	// a list that did not include it.
	//
	// EVERY legacy key present is named, in a fixed order. This was a range
	// over a map returning at the first one it happened to find, which meant a
	// config with `kind` and `container` was told about one of them at random —
	// so the operator fixed that key, re-ran, and got a different complaint,
	// and the test asserting the message named `storage.kind` passed on four
	// runs out of five. Go randomises map iteration for exactly this reason and
	// it took a CI runner to collect the fifth.
	legacy := []struct {
		key   string
		value string
	}{
		{"kind", c.Storage.LegacyKind},
		{"container", c.Storage.LegacyContainer},
		{"endpoint", c.Storage.LegacyEndpoint},
		{"accountEnv", c.Storage.LegacyAccountEnv},
		{"keyEnv", c.Storage.LegacyKeyEnv},
	}
	var found []string
	for _, l := range legacy {
		if l.value != "" {
			found = append(found, "storage."+l.key)
		}
	}
	if len(found) > 0 {
		verb, them := "is", "it"
		if len(found) > 1 {
			verb, them = "are", "them"
		}
		return fmt.Errorf("%s %s no longer read: storage now declares a "+
			"list of destinations. Move %s under storage.remotes:\n\n"+
			"  storage:\n"+
			"    dir: %s\n"+
			"    remotes:\n"+
			"      - name: snapshots\n"+
			"        kind: %s\n"+
			"        container: %s\n\n"+
			"and see docs/config.md. A local-only config declares no remotes at all",
			strings.Join(found, ", "), verb, them,
			orDefault(c.Storage.Dir, ".pgctl/snapshots"),
			StorageAzureBlob, orDefault(c.Storage.LegacyContainer, "pg-snapshots"))
	}

	names := map[string]bool{LocalStorage: true}
	for _, r := range c.Storage.Remotes {
		switch {
		case r.Name == "":
			return fmt.Errorf("a storage remote has no name")
		case names[r.Name]:
			return fmt.Errorf("storage remote %q declared twice, or named after "+
				"the local directory", r.Name)
		case r.Kind != StorageAzureBlob:
			return fmt.Errorf("storage remote %q: unknown kind %q (want %q)",
				r.Name, r.Kind, StorageAzureBlob)
		case r.Container == "":
			return fmt.Errorf("storage remote %q: container is required", r.Name)
		}
		names[r.Name] = true
	}

	setNames := map[string]bool{}
	for _, s := range c.Sets {
		switch {
		case s.Name == "":
			return fmt.Errorf("a set has no name")
		case setNames[s.Name]:
			return fmt.Errorf("set %q declared twice", s.Name)
		case s.Database == "":
			return fmt.Errorf("set %q names no database", s.Name)
		case len(s.Include) == 0:
			return fmt.Errorf("set %q includes nothing", s.Name)
		}
		setNames[s.Name] = true
		for _, pat := range append(append([]string{}, s.Include...), s.Exclude...) {
			if err := validPattern(pat); err != nil {
				return fmt.Errorf("set %q: %w", s.Name, err)
			}
		}
	}

	for _, r := range c.Rules {
		if err := validPattern(r.Table); err != nil {
			return fmt.Errorf("rule: %w", err)
		}
		switch r.Data {
		case "", DataAll, DataNone, DataFiltered:
		default:
			return fmt.Errorf("rule %q: unknown data mode %q", r.Table, r.Data)
		}
		if r.Data == DataFiltered && r.Where == "" {
			return fmt.Errorf("rule %q: data %q needs a where predicate", r.Table, DataFiltered)
		}
		if r.Where != "" && r.Data == DataNone {
			return fmt.Errorf("rule %q: a where predicate contradicts data %q", r.Table, DataNone)
		}
	}

	for _, r := range c.Retentions() {
		if r < 0 {
			return fmt.Errorf("storage.retention values cannot be negative")
		}
	}

	// A set naming a database nothing else mentions is not an error — the
	// databases are discovered from the server, so the config cannot know
	// whether it exists until something connects.
	return nil
}

// Retentions is the retention policy as a slice, for uniform checking.
func (c *Config) Retentions() []int {
	return []int{c.Storage.Retention.Daily, c.Storage.Retention.Weekly, c.Storage.Retention.Monthly}
}

// validPattern checks the shape of a table pattern.
//
// A pattern is `schema.table`, either part optionally wildcarded at its start
// or end, or a bare WILDCARDED table pattern that applies in every schema —
// `*as400*`. A bare exact name is still refused, because that is a table
// somebody forgot to qualify rather than a statement about every schema. It is not
// resolved against search_path at any point — an unqualified pattern means
// "every schema" explicitly rather than "whichever one the connection happens
// to be looking at", which for a tool that truncates tables is the distinction
// that matters.
//
// What is still refused is a `*` in the middle: `oper*ions.foo` is either a
// typo or a regexp somebody expected to work, and both are better answered now
// than by a rule that matches nothing.
func validPattern(pat string) error {
	if pat == "" {
		return fmt.Errorf("empty table pattern")
	}
	schema, table := "*", pat
	if s, t, ok := strings.Cut(pat, "."); ok {
		schema, table = s, t
	} else if !strings.Contains(pat, "*") {
		return fmt.Errorf("table pattern %q is not schema-qualified (want schema.table, "+
			"or *.%s if you mean that table in every schema)", pat, pat)
	}
	if schema == "" || table == "" {
		return fmt.Errorf("table pattern %q has an empty half (want schema.table, "+
			"or a bare table pattern for every schema)", pat)
	}
	// A slice, not a map: `a*b.c*d` is wrong in both halves and the message has
	// to name the same one every time somebody re-runs it. Same bug as the
	// legacy storage keys above, one function apart.
	for _, half := range []struct{ part, value string }{
		{"schema", schema}, {"table", table},
	} {
		if inner := strings.Trim(half.value, "*"); strings.Contains(inner, "*") {
			return fmt.Errorf("table pattern %q: in the %s part, `*` is only allowed "+
				"at the start or the end", pat, half.part)
		}
	}
	return nil
}

// matchName matches a bare name against a pattern that may end in `*`. Used for
// database exclusions, which are not schema-qualified.
func matchName(pattern, name string) bool {
	if prefix, wild := strings.CutSuffix(pattern, "*"); wild {
		return strings.HasPrefix(name, prefix)
	}
	return pattern == name
}
