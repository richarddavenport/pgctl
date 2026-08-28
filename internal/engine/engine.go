package engine

import (
	"path/filepath"

	"github.com/richarddavenport/pgctl/internal/config"
)

// Engine performs pgctl's operations against a project.
type Engine struct {
	cfg *config.Config
	// root is the directory the config's relative paths resolve against —
	// normally the repository root, since that is where envs/ lives.
	root string
}

// New builds an engine for a loaded config.
func New(cfg *config.Config) *Engine {
	root := "."
	if cfg.Source != "" {
		root = filepath.Dir(cfg.Source)
	}
	return &Engine{cfg: cfg, root: root}
}

// Config exposes the loaded config to the front ends.
func (e *Engine) Config() *config.Config { return e.cfg }

// Root is the directory relative config paths resolve against.
func (e *Engine) Root() string { return e.root }

func (e *Engine) storageRoot() string {
	dir := e.cfg.Storage.Dir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(e.root, dir)
	}
	return dir
}

// StorageDir is where snapshots are written, for a message that tells an
// operator where their gigabytes went.
func (e *Engine) StorageDir() string { return e.storageRoot() }
