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
	if c.Storage.Dir == "" {
		c.Storage.Dir = ".pgctl/snapshots"
	}
	for i := range c.Storage.Remotes {
		r := &c.Storage.Remotes[i]
		if r.Kind == "" {
			r.Kind = StorageAzureBlob
		}
		// The defaults are the variables `az` itself sets, so a remote that
		// uses the account you are already logged into names nothing.
		if r.AccountEnv == "" {
			r.AccountEnv = "AZURE_STORAGE_ACCOUNT"
		}
		if r.KeyEnv == "" {
			r.KeyEnv = "AZURE_STORAGE_KEY"
		}
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
