package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// EnvVar names an alternative config path, checked before the search paths.
const EnvVar = "PGCTL_CONFIG"

// Load finds and parses the configuration. Search order: the explicit path
// (flag), $PGCTL_CONFIG, ./pgctl.yaml, then the user config dir.
//
// Unlike swarmctl, a missing config is an error: swarmctl without a config can
// still browse the local Docker daemon, but pgctl with no databases declared
// has nothing whatsoever to do.
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
	return nil, fmt.Errorf("no pgctl.yaml found (looked in %s)", strings.Join(searchPaths(), ", "))
}

func searchPaths() []string {
	paths := []string{"pgctl.yaml"}
	if dir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "pgctl", "config.yaml"))
	}
	return paths
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

// Parse decodes a config and resolves it: defaults applied, environments
// merged in from swarmctl, servers attached, protection settled. dir is the
// directory relative paths (EnvironmentsFrom, Storage.Dir) resolve against —
// the config's own directory, which for this project is the monorepo root.
func Parse(data []byte, dir string) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.mergeEnvironments(dir); err != nil {
		return nil, err
	}
	cfg.resolveServers()
	cfg.resolveProtection()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Defaults.Jobs == 0 {
		// Four rather than NumCPU: the parallelism that matters is the
		// database server's, not the machine running pgctl, and pg_dump -j
		// opens one connection per job — a default that scales with a
		// developer's laptop would open 16 connections to production.
		c.Defaults.Jobs = 4
	}
	if c.Defaults.Compression == "" {
		c.Defaults.Compression = "zstd:3"
	}
	if c.Defaults.LockTimeout == 0 {
		c.Defaults.LockTimeout = 30 * time.Second
	}
	if c.Credentials.HostKey == "" {
		c.Credentials.HostKey = "POSTGRES_HOST"
	}
	if c.Credentials.PortKey == "" {
		c.Credentials.PortKey = "POSTGRES_PORT"
	}
	if c.Credentials.UserKey == "" {
		c.Credentials.UserKey = "POSTGRES_USERNAME"
	}
	if c.Credentials.PasswordKey == "" {
		c.Credentials.PasswordKey = "POSTGRES_PASSWORD"
	}
	if c.Storage.Kind == "" {
		c.Storage.Kind = StorageLocal
	}
	if c.Storage.Dir == "" {
		c.Storage.Dir = "snapshots"
	}
	if c.Storage.AccountKey == "" {
		c.Storage.AccountKey = "AZURE_STORAGE_ACCOUNT"
	}
	if c.Storage.KeyKey == "" {
		c.Storage.KeyKey = "AZURE_STORAGE_KEY"
	}
	for i := range c.Postgres {
		s := c.Postgres[i]
		if s.MaintenanceDB == "" {
			s.MaintenanceDB = "postgres"
		}
		c.Postgres[i] = s
	}
}

// mergeEnvironments folds in the environments declared by a swarmctl config.
// Inline environments win on conflict: a project that has said something
// locally meant it.
func (c *Config) mergeEnvironments(dir string) error {
	path := c.EnvironmentsFrom
	implicit := path == ""
	if implicit {
		path = "swarmctl.yaml"
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}

	found, err := readSwarmctlEnvironments(path)
	if err != nil {
		// The default is a guess about the project's layout, so its absence is
		// not a failure. An explicitly named file that is missing is.
		if implicit && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range found {
		if _, ok := c.LookupEnv(e.Name); ok {
			continue
		}
		c.Environments = append(c.Environments, e)
	}
	return nil
}

// resolveServers attaches connection detail to each environment, and declares
// an environment for any Postgres entry that matched none — which is how a
// `local` environment exists without a swarm behind it.
func (c *Config) resolveServers() {
	for i := range c.Environments {
		if s, ok := c.Postgres[c.Environments[i].Name]; ok {
			c.Environments[i].Server = s
		}
	}
	names := make([]string, 0, len(c.Postgres))
	for name := range c.Postgres {
		if _, ok := c.LookupEnv(name); !ok {
			names = append(names, name)
		}
	}
	// Map iteration order is random and the environment list is user-facing.
	sort.Strings(names)
	for _, name := range names {
		c.Environments = append(c.Environments, Environment{Name: name, Server: c.Postgres[name]})
	}
}

// resolveProtection settles which environments may never be written to. With
// no `protect:` key, every guarded environment is protected — the safe reading
// of a project that has marked prd guarded for swarmctl. See decisions #9.
func (c *Config) resolveProtection() {
	for i := range c.Environments {
		e := &c.Environments[i]
		if c.Protect == nil {
			e.Protected = e.Protected || e.Guarded
			continue
		}
		e.Protected = e.Protected || slices.Contains(*c.Protect, e.Name)
	}
}
