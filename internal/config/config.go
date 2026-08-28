// Package config is pgctl's contract with a project: how to reach each
// database server, which groups of tables move together, and what to do about
// the tables that are too big to move whole.
//
// It is deliberately small. PostgreSQL already has a complete, standard way to
// describe a connection — connection strings, ~/.pg_service.conf, ~/.pgpass and
// the PG* environment variables — which libpq reads and which pgx implements
// identically. pgctl uses that rather than inventing a parallel scheme, so
// there is nothing pgctl-specific to learn about credentials and no password
// anywhere in this file.
package config

import "time"

// Config is the project configuration. Every field is optional: with no config
// at all, pgctl connects wherever psql with no arguments would.
type Config struct {
	// Connections are the servers pgctl can reach, by name.
	Connections Connections `yaml:"connections"`

	Defaults  Defaults  `yaml:"defaults"`
	Storage   Storage   `yaml:"storage"`
	Databases Databases `yaml:"databases"`

	// Sets are named groups of tables that move together — the unit of a
	// table-level migration.
	Sets []Set `yaml:"sets"`

	// Rules override how individual tables are dumped. Later rules win, so a
	// project can state a schema-wide default and then except one table.
	Rules []Rule `yaml:"rules"`

	Hooks Hooks `yaml:"hooks"`

	// Source is the path the config was loaded from, empty when running on
	// built-in defaults.
	Source string `yaml:"-"`

	// Warnings are non-fatal complaints, surfaced in the UI. A config problem
	// that does not stop the tool working should not stop the tool starting.
	Warnings []string `yaml:"-"`
}

// Connections is an ordered set of named connections. Order is kept because it
// is the order they are listed in, and a UI that reordered them would be
// harder to navigate than one that did not.
type Connections struct {
	Names  []string
	ByName map[string]Connection
}

// Connection is one server pgctl can reach.
type Connection struct {
	Name string `yaml:"-"`

	// DSN is anything libpq accepts: a service name (`service=prd`), a URI
	// (`postgres://host/db`), or keyword pairs (`host=… port=…`). It is
	// resolved by pgx for pgctl's own queries and handed to pg_dump and
	// pg_restore unchanged, so both halves of an operation connect the same
	// way and a password lives only in ~/.pgpass.
	DSN string `yaml:"dsn"`

	// Guarded requires the connection's name typed in full before an apply
	// touches it.
	Guarded bool `yaml:"guarded"`

	// Protected refuses the connection as an apply target at all. Production
	// sets this; nothing turns it off.
	Protected bool `yaml:"protected"`

	// Jobs overrides Defaults.Jobs here. A dump and a restore are bounded by
	// different machines, so the useful parallelism differs per server.
	Jobs int `yaml:"jobs"`

	// MaintenanceDB is the database pgctl connects to in order to create, drop
	// or interrogate the others.
	MaintenanceDB string `yaml:"maintenanceDatabase"`
}

// Databases decides which of a server's databases pgctl works with.
//
// Discovery, not declaration: a server knows what databases it has, and a list
// in a config file can only ever disagree with it. Exclude trims the ones
// nobody wants to think about.
type Databases struct {
	// Exclude names databases to ignore entirely. Patterns may end in `*`.
	Exclude []string `yaml:"exclude"`

	// ExcludeSchemas drops schemas from every snapshot.
	//
	// Rarely what you want: a trigger on a table you are keeping may call a
	// function in the schema you are dropping, and pg_restore then fails on
	// every CREATE TRIGGER after the data has already loaded. `pgctl snapshot`
	// checks for that and says so. Excluding a table's *data* with a rule is
	// almost always the right tool instead.
	ExcludeSchemas []string `yaml:"excludeSchemas"`
}

// Defaults parameterize every snapshot and apply unless overridden.
type Defaults struct {
	// Jobs is the parallelism handed to pg_dump -j and pg_restore -j.
	Jobs int `yaml:"jobs"`

	// Compression is a pg_dump --compress value. zstd:3 rather than the gzip-6
	// default: both faster and smaller, and every supported server has it.
	Compression string `yaml:"compression"`

	// LockTimeout bounds how long an apply waits to acquire a lock. This is
	// the timeout that catches a real problem — an application still holding
	// the table pgctl is about to truncate — so it is short.
	LockTimeout time.Duration `yaml:"lockTimeout"`

	// StatementTimeout bounds a single statement. Zero, the default, means no
	// limit: rebuilding an index on a 20 GB table legitimately takes hours,
	// and a timeout that kills honest work mid-apply is worse than no timeout.
	StatementTimeout time.Duration `yaml:"statementTimeout"`
}

// Storage is where snapshots live once taken.
type Storage struct {
	// Kind is "local" or "azureblob".
	Kind string `yaml:"kind"`

	// Dir is the local root, and doubles as the staging area for a remote
	// destination.
	Dir string `yaml:"dir"`

	Container string `yaml:"container"`

	// Endpoint overrides the blob service URL. Azure needs nothing here; it
	// exists for a storage emulator and for a private endpoint.
	Endpoint string `yaml:"endpoint"`

	// AccountEnv and KeyEnv name the environment variables holding the storage
	// account and its key, following the same principle as the database
	// credentials: pgctl reads them from where they already are rather than
	// storing them.
	AccountEnv string `yaml:"accountEnv"`
	KeyEnv     string `yaml:"keyEnv"`

	Retention Retention `yaml:"retention"`
}

// Retention is how many snapshots of each cadence survive a prune. Zero means
// keep everything of that cadence, so an unconfigured pgctl never deletes.
type Retention struct {
	Daily   int `yaml:"daily"`
	Weekly  int `yaml:"weekly"`
	Monthly int `yaml:"monthly"`
}

// Set is a named group of tables that migrate together. Patterns are
// schema-qualified and may carry a `*` at either end of the table part.
type Set struct {
	Name        string   `yaml:"name"`
	Database    string   `yaml:"database"`
	Description string   `yaml:"description"`
	Include     []string `yaml:"include"`
	Exclude     []string `yaml:"exclude"`
}

// DataMode is how much of a table's data a snapshot carries.
type DataMode string

const (
	// DataAll dumps every row, the default for any table without a rule.
	DataAll DataMode = "all"
	// DataNone dumps the table's definition and no rows.
	DataNone DataMode = "none"
	// DataFiltered dumps the rows matching Rule.Where, via COPY — pg_dump has
	// no row predicate. See design/decisions.md #4.
	DataFiltered DataMode = "filtered"
)

// Rule overrides how one table, or one pattern of tables, is dumped.
type Rule struct {
	// Table is schema-qualified and may carry a `*` at either end of the table
	// part.
	Table string `yaml:"table"`

	// Data defaults to "all", or to "filtered" when Where is set.
	Data DataMode `yaml:"data"`

	// Where is a SQL predicate selecting the rows worth carrying. It is
	// interpolated into a COPY (SELECT …) — it is configuration written by the
	// team, not input, and pgctl makes no attempt to sandbox it.
	Where string `yaml:"where"`

	// Why records what the rule is for, so that a table missing its history in
	// a copied environment is explicable without reading a commit log.
	Why string `yaml:"why"`
}

// Hooks are shell commands run around an apply. They exist because quiescing a
// target is project-specific — scaling swarm services is one answer, and does
// not belong inside pgctl. See design/decisions.md #7.
type Hooks struct {
	// PreApply runs before anything is touched; a failure aborts the apply.
	PreApply []Hook `yaml:"preApply"`

	// PostApply runs after the data has landed. A failure is reported but does
	// not fail the apply — the data is already in.
	PostApply []Hook `yaml:"postApply"`

	// OnFailure runs when an apply fails, so that a target quiesced by
	// PreApply can be brought back up.
	OnFailure []Hook `yaml:"onFailure"`
}

// Hook is one command. $PGCTL_CONNECTION, $PGCTL_DATABASE and $PGCTL_SNAPSHOT
// are in its environment.
type Hook struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`

	// Timeout bounds the hook. Zero means ten minutes.
	Timeout time.Duration `yaml:"timeout"`
}

// Lookup returns the connection with the given name.
func (c *Config) Lookup(name string) (Connection, bool) {
	conn, ok := c.Connections.ByName[name]
	return conn, ok
}

// All returns the connections in the order they were declared.
func (c *Config) All() []Connection {
	out := make([]Connection, 0, len(c.Connections.Names))
	for _, name := range c.Connections.Names {
		out = append(out, c.Connections.ByName[name])
	}
	return out
}

// LookupSet returns the set with the given name.
func (c *Config) LookupSet(name string) (Set, bool) {
	for _, s := range c.Sets {
		if s.Name == name {
			return s, true
		}
	}
	return Set{}, false
}

// ManagesDatabase reports whether a database is one pgctl works with.
func (c *Config) ManagesDatabase(name string) bool {
	for _, pattern := range c.Databases.Exclude {
		if matchName(pattern, name) {
			return false
		}
	}
	return true
}
