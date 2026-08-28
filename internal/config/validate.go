package config

import (
	"fmt"
	"strings"
)

// Storage kinds.
const (
	StorageLocal     = "local"
	StorageAzureBlob = "azureblob"
)

// Validate reports the config problems that would make a run meaningless.
// Problems that only make it worse are recorded in Warnings instead.
func (c *Config) Validate() error {
	if len(c.Databases) == 0 {
		return fmt.Errorf("no databases declared")
	}
	switch c.Storage.Kind {
	case StorageLocal:
	case StorageAzureBlob:
		if c.Storage.Container == "" {
			return fmt.Errorf("storage.container is required for kind %q", StorageAzureBlob)
		}
	default:
		return fmt.Errorf("unknown storage.kind %q (want %q or %q)",
			c.Storage.Kind, StorageLocal, StorageAzureBlob)
	}

	seen := map[string]bool{}
	for _, d := range c.Databases {
		if d.Name == "" {
			return fmt.Errorf("a database has no name")
		}
		if seen[d.Name] {
			return fmt.Errorf("database %q declared twice", d.Name)
		}
		seen[d.Name] = true
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
		case !seen[s.Database]:
			return fmt.Errorf("set %q names undeclared database %q", s.Name, s.Database)
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

	// An environment needs a host from somewhere: its secrets file, or an
	// override here. Neither is not fatal — the other environments still work,
	// and a config that fails to load helps nobody.
	for _, e := range c.Environments {
		if e.Server.Host == "" && e.Secrets.File == "" {
			c.Warnings = append(c.Warnings, fmt.Sprintf(
				"environment %q has neither a secrets file nor postgres.%s.host, so pgctl cannot reach it",
				e.Name, e.Name))
		}
	}
	for _, r := range c.Retentions() {
		if r < 0 {
			return fmt.Errorf("storage.retention values cannot be negative")
		}
	}
	return nil
}

// Retentions is the retention policy as a slice, for uniform checking.
func (c *Config) Retentions() []int {
	return []int{c.Storage.Retention.Daily, c.Storage.Retention.Weekly, c.Storage.Retention.Monthly}
}

// validPattern enforces schema qualification. An unqualified pattern would
// resolve against search_path at some later moment, which for a tool that
// truncates tables is not a risk worth carrying.
func validPattern(pat string) error {
	if pat == "" {
		return fmt.Errorf("empty table pattern")
	}
	schema, table, ok := strings.Cut(pat, ".")
	if !ok || schema == "" || table == "" {
		return fmt.Errorf("table pattern %q is not schema-qualified (want schema.table)", pat)
	}
	if strings.Contains(schema, "*") {
		return fmt.Errorf("table pattern %q: wildcards are allowed in the table part only", pat)
	}
	if inner := strings.Trim(table, "*"); strings.Contains(inner, "*") {
		return fmt.Errorf("table pattern %q: `*` is only allowed at the start or the end", pat)
	}
	return nil
}
