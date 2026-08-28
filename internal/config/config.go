// Package config is pgctl's contract with a project: which environments exist,
// which databases matter, which groups of tables move together, and what to do
// about the tables that are too big to move whole.
//
// pgctl carries no MBPNetwork-specific values. Everything about your databases
// lives in a pgctl.yaml the team commits next to the schema it describes.
package config

import "time"

// Config is the project configuration.
type Config struct {
	// EnvironmentsFrom names a swarmctl config to read environments out of, so
	// that "which host is prd" has one answer in a repository that already
	// declares it. Defaults to swarmctl.yaml when that file exists beside the
	// pgctl config. See design/decisions.md #8.
	EnvironmentsFrom string `yaml:"environmentsFrom"`

	// Environments declared inline, for projects with no swarmctl. Merged with
	// (and overriding) anything read from EnvironmentsFrom.
	Environments []Environment `yaml:"environments"`

	// Postgres overrides connection detail per environment, and declares
	// environments that have none of their own — a name here matching no
	// swarmctl environment becomes one, which is how `local` exists. For a
	// real environment nothing needs to be said: host, port, user and password
	// come out of its sops-encrypted secrets file (see Credentials).
	Postgres map[string]Server `yaml:"postgres"`

	Credentials Credentials `yaml:"credentials"`
	Defaults    Defaults    `yaml:"defaults"`
	Storage     Storage     `yaml:"storage"`

	// Databases pgctl will snapshot, in the order a full refresh applies them.
	Databases []Database `yaml:"databases"`

	// Sets are named groups of tables that move together — the unit of a
	// table-level migration.
	Sets []Set `yaml:"sets"`

	// Rules override how individual tables are dumped. Later rules win, so a
	// project can state a schema-wide default and then except one table.
	Rules []Rule `yaml:"rules"`

	// Protect names environments that may never be an apply target. Defaults
	// to every guarded environment, so a project that marks prd guarded for
	// swarmctl gets it protected here without saying so twice. Write an empty
	// list to mean none.
	Protect *[]string `yaml:"protect"`

	Hooks Hooks `yaml:"hooks"`

	// Source is the path the config was loaded from, empty when running on
	// built-in defaults.
	Source string `yaml:"-"`

	// Warnings are non-fatal complaints, surfaced in the UI. A config problem
	// that does not stop the tool working should not stop the tool starting.
	Warnings []string `yaml:"-"`
}

// Environment is one deployment pgctl can read from or write to.
type Environment struct {
	Name   string `yaml:"name"`
	Domain string `yaml:"domain"`

	// Guarded requires the environment's name typed in full before an apply
	// touches it.
	Guarded bool `yaml:"guarded"`

	// Protected refuses the environment as an apply target at all. Production
	// sets this; nothing turns it off.
	Protected bool `yaml:"protected"`

	SSH     SSH     `yaml:"ssh"`
	Secrets Secrets `yaml:"secrets"`

	// Server is Postgres[Name], resolved at load time.
	Server Server `yaml:"-"`
}

// SSH reaches the environment's swarm manager, for hooks that run there.
type SSH struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// Secrets locates the environment's sops-encrypted dotenv, which is where
// database credentials and storage keys come from.
type Secrets struct {
	File string `yaml:"file"`
}

// Server is how to reach one environment's PostgreSQL.
type Server struct {
	// Host and Port override the environment's secrets file. Needed only for
	// an environment that has no secrets file, such as a local cluster.
	Host string `yaml:"host"`
	Port int    `yaml:"port"`

	// MaintenanceDB is the database pgctl connects to in order to create, drop
	// or interrogate the others.
	MaintenanceDB string `yaml:"maintenanceDatabase"`

	// Jobs overrides Defaults.Jobs for this server. A dump and a restore are
	// bounded by different machines, so the useful parallelism differs per
	// environment — prd has more vCPUs than a laptop.
	Jobs int `yaml:"jobs"`
}

// Credentials names the environment-file keys holding connection detail,
// rather than the detail itself. pgctl never stores a password: it decrypts the
// environment's secrets file at the moment it needs one, and holds the result
// in memory for that operation only.
//
// The host is read from there too, rather than restated in pgctl.yaml. An
// environment file that names one host while pgctl.yaml names another is a
// restore pointed at the wrong server, and the only way to be sure that cannot
// happen is for there to be one place to look.
type Credentials struct {
	HostKey     string `yaml:"hostKey"`
	PortKey     string `yaml:"portKey"`
	UserKey     string `yaml:"userKey"`
	PasswordKey string `yaml:"passwordKey"`
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
	// exists for a storage emulator and for a private endpoint with its own
	// hostname.
	Endpoint string `yaml:"endpoint"`

	// CredentialsFrom names the environment whose secrets file holds the
	// storage account and key. Empty means each environment's own, which is
	// right only when every environment has its own container; a project with
	// one snapshots account names that environment here so prd's nightly and
	// QAT's refresh reach the same place.
	CredentialsFrom string `yaml:"credentialsFrom"`

	// AccountKey and KeyKey name the environment-file keys holding the storage
	// account and its key, following Credentials' reasoning.
	AccountKey string `yaml:"accountKey"`
	KeyKey     string `yaml:"keyKey"`

	Retention Retention `yaml:"retention"`
}

// Retention is how many snapshots of each cadence survive a prune. Zero means
// keep everything of that cadence, so an unconfigured pgctl never deletes.
type Retention struct {
	Daily   int `yaml:"daily"`
	Weekly  int `yaml:"weekly"`
	Monthly int `yaml:"monthly"`
}

// Database is one database in the cluster.
type Database struct {
	Name string `yaml:"name"`

	// Schemas restricts a snapshot to these schemas. Empty means all of them
	// except those pgctl always excludes.
	Schemas []string `yaml:"schemas"`

	// ExcludeSchemas drops schemas from a snapshot entirely — Hasura's
	// hdb_catalog and an audit schema being the usual candidates.
	ExcludeSchemas []string `yaml:"excludeSchemas"`
}

// Set is a named group of tables that migrate together. Patterns are
// schema-qualified and may end in `*`.
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
	// Table is schema-qualified and may end in `*`.
	Table string `yaml:"table"`

	// Data defaults to "all", or to "filtered" when Where is set.
	Data DataMode `yaml:"data"`

	// Where is a SQL predicate selecting the rows worth carrying. It is
	// interpolated into a COPY (SELECT …) — it is configuration written by the
	// team, not input, and pgctl makes no attempt to sandbox it.
	Where string `yaml:"where"`

	// Why records what the rule is for, so that a table missing its history in
	// QAT is explicable without reading a commit log.
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

// Hook is one command. $PGCTL_ENV, $PGCTL_DATABASE and $PGCTL_SNAPSHOT are in
// its environment.
type Hook struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`

	// Timeout bounds the hook. Zero means Defaults.StatementTimeout.
	Timeout time.Duration `yaml:"timeout"`
}

// LookupEnv returns the environment with the given name.
func (c *Config) LookupEnv(name string) (Environment, bool) {
	for _, e := range c.Environments {
		if e.Name == name {
			return e, true
		}
	}
	return Environment{}, false
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
