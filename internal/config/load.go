package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// EnvVar names an alternative config path, checked before the search paths.
const EnvVar = "PGCTL_CONFIG"

// DefaultConnection is the name given to the connection pgctl invents when no
// config declares any.
const DefaultConnection = "default"

// Load finds and parses the configuration. Search order: the explicit path
// (flag), $PGCTL_CONFIG, ./pgctl.yaml, then ~/.config/pgctl/config.yaml.
//
// A missing config is not an error. pgctl then does what psql with no
// arguments does — connects wherever the environment points — which is enough
// to look at a database and take a snapshot of it.
func Load(explicit string) (*Config, error) {
	if explicit == "" {
		explicit = os.Getenv(EnvVar)
	}
	if explicit != "" {
		return loadFile(explicit)
	}
	for _, path := range searchPaths() {
		cfg, err := loadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		return cfg, err
	}
	return Parse(nil, ".")
}

func searchPaths() []string {
	paths := []string{"pgctl.yaml"}
	if dir := Home(); dir != "" {
		paths = append(paths, filepath.Join(dir, "config.yaml"))
	}
	return paths
}

// Home is the directory pgctl's own configuration lives in:
// $XDG_CONFIG_HOME/pgctl, or ~/.config/pgctl.
//
// Not os.UserConfigDir, which is what this used to be. On Linux that function
// already answers $XDG_CONFIG_HOME or ~/.config, so nothing changes there — but
// on macOS it answers ~/Library/Application Support unconditionally, which is
// where a GUI application keeps its state and not where anyone looks for a file
// they edit by hand. On this machine ~/.config holds gh, git, fish, btop and
// gcloud among others, and ~/Library/Application Support/pgctl held nothing,
// because nobody ever went there to make one.
//
// Empty when there is no home directory to hang it off, which is a container
// running as a user with no passwd entry — in which case pgctl falls back to
// ./pgctl.yaml and the environment, as it does with no config at all.
func Home() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "pgctl")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "pgctl")
}

// ServiceFile is the libpq service file pgctl keeps beside its own config, and
// where a `service=prd` DSN is resolved from.
//
// It is a LIBPQ SERVICE FILE, in libpq's format, read by libpq — not a second
// configuration scheme. Decision 8 says a connection is a libpq DSN and nothing
// else, and every parallel scheme pgctl has invented has been deleted again;
// this changes only WHERE the file is, which libpq itself parameterises with
// PGSERVICEFILE. The file name is kept as pg_service.conf so that what it is,
// and whose documentation describes it, are not in doubt.
//
// Passwords do not live here. They stay in ~/.pgpass, exactly as for psql.
func ServiceFile() string {
	dir := Home()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "pg_service.conf")
}

// UseServiceFile points libpq at [ServiceFile] for the rest of this process.
//
// One process-wide variable rather than a rewritten DSN, and that is the whole
// reason it works: pgx reads PGSERVICEFILE when it parses a connection string,
// and pg_dump and pg_restore inherit it through os.Environ(), so both halves of
// an operation resolve `service=prd` from the same file. Adding a servicefile=
// keyword to the DSN would work for pgx and not for the subprocesses, and it
// would mean pgctl rewriting a DSN a person wrote, which decision 8 says it has
// no business doing.
//
// Three things it deliberately does not do:
//
//   - override an existing PGSERVICEFILE. Someone who has already told libpq
//     where their services are has said something more specific than a default.
//   - do anything at all when the file is not there. libpq then falls back to
//     ~/.pg_service.conf as it always has, so a machine set up the old way keeps
//     working and this is strictly additive.
//   - report an error. There is nothing to fail: no file means no change, and a
//     malformed one is libpq's to complain about, by name, when a connection is
//     actually attempted.
func UseServiceFile() {
	if os.Getenv("PGSERVICEFILE") != "" {
		return
	}
	path := ServiceFile()
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	_ = os.Setenv("PGSERVICEFILE", path)
}

func loadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg, err := Parse(data, filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Source = path
	return cfg, nil
}

// Parse decodes a config and resolves it. dir is the directory relative paths
// resolve against — the config's own directory.
func Parse(data []byte, dir string) (*Config, error) {
	var cfg Config
	if len(data) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("parse: %w", err)
		}
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Defaults.Jobs == 0 {
		// Four rather than NumCPU: the parallelism that matters is the
		// database server's, not the machine running pgctl, and pg_dump -j
		// opens one connection per job — a default that scaled with a
		// developer's laptop would open sixteen connections to production.
		c.Defaults.Jobs = 4
	}
	if c.Defaults.Compression == "" {
		c.Defaults.Compression = "zstd:3"
	}
	if c.Defaults.LockTimeout == 0 {
		c.Defaults.LockTimeout = 30 * time.Second
	}
	if c.Storage.Kind == "" {
		c.Storage.Kind = StorageLocal
	}
	if c.Storage.Dir == "" {
		c.Storage.Dir = ".pgctl/snapshots"
	}
	if c.Storage.AccountEnv == "" {
		c.Storage.AccountEnv = "AZURE_STORAGE_ACCOUNT"
	}
	if c.Storage.KeyEnv == "" {
		c.Storage.KeyEnv = "AZURE_STORAGE_KEY"
	}

	// The template databases are never interesting, and `postgres` is the
	// maintenance database rather than anybody's data.
	c.Databases.Exclude = append(c.Databases.Exclude, "template0", "template1")

	if len(c.Connections.Names) == 0 {
		// No connections declared: one that says nothing, so libpq resolves it
		// entirely from the environment. This is what makes `pgctl` work in a
		// directory with no config at all.
		c.Connections = Connections{
			Names:  []string{DefaultConnection},
			ByName: map[string]Connection{DefaultConnection: {Name: DefaultConnection}},
		}
	}

	for name, conn := range c.Connections.ByName {
		if conn.MaintenanceDB == "" {
			conn.MaintenanceDB = "postgres"
			c.Connections.ByName[name] = conn
		}
	}
}

// UnmarshalYAML reads the connections map, preserving declaration order and
// accepting a bare string as shorthand for a connection that is only a DSN.
//
// The shorthand matters more than it looks: most connections have nothing to
// say beyond how to reach the server, and `qat: "service=qat"` is the whole
// truth about one of them.
func (c *Connections) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("connections must be a mapping of name to connection, at line %d", node.Line)
	}
	c.Names = nil
	c.ByName = map[string]Connection{}

	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode, valueNode := node.Content[i], node.Content[i+1]
		name := keyNode.Value
		if name == "" {
			return fmt.Errorf("a connection has no name, at line %d", keyNode.Line)
		}
		if _, exists := c.ByName[name]; exists {
			return fmt.Errorf("connection %q declared twice", name)
		}

		conn := Connection{Name: name}
		switch valueNode.Kind {
		case yaml.ScalarNode:
			conn.DSN = valueNode.Value
		case yaml.MappingNode:
			if err := valueNode.Decode(&conn); err != nil {
				return fmt.Errorf("connection %q: %w", name, err)
			}
			conn.Name = name
		default:
			return fmt.Errorf("connection %q must be a DSN string or a mapping, at line %d",
				name, valueNode.Line)
		}

		c.Names = append(c.Names, name)
		c.ByName[name] = conn
	}
	return nil
}
