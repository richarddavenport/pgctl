package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// openSnapshot resolves a snapshot id to its manifest and directory.
//
// An id may be abbreviated to its last component when it is unambiguous —
// `20260828T030000Z` rather than `prd/product-development/20260828T030000Z` —
// and to `<env>/latest` for the newest complete snapshot of an environment,
// which is what a nightly-plus-refresh workflow actually asks for.
func (e *Engine) openSnapshot(id string) (*snapshot.Manifest, string, error) {
	if id == "" {
		return nil, "", errors.New("no snapshot named")
	}

	all, err := e.Snapshots()
	if err != nil {
		return nil, "", err
	}
	if len(all) == 0 {
		return nil, "", fmt.Errorf("no snapshots under %s", e.storageRoot())
	}

	if env, ok := strings.CutSuffix(id, "/latest"); ok {
		for i := len(all) - 1; i >= 0; i-- {
			if all[i].Environment == env && all[i].Complete() {
				return all[i], snapshot.Path(e.storageRoot(), all[i].ID), nil
			}
		}
		return nil, "", fmt.Errorf("no complete snapshot of %q", env)
	}

	var matches []*snapshot.Manifest
	for _, m := range all {
		if m.ID == id || strings.HasSuffix(m.ID, "/"+id) {
			matches = append(matches, m)
		}
	}
	switch len(matches) {
	case 0:
		return nil, "", fmt.Errorf("no snapshot %q", id)
	case 1:
		return matches[0], snapshot.Path(e.storageRoot(), matches[0].ID), nil
	default:
		var ids []string
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		return nil, "", fmt.Errorf("%q is ambiguous: %s", id, strings.Join(ids, ", "))
	}
}

// Snapshots lists every snapshot in local storage, oldest first.
//
// Found by walking for manifests rather than by reading an index: an index is a
// second thing that can be wrong about what is on disk, and a snapshot's
// directory is already named after its id.
func (e *Engine) Snapshots() ([]*snapshot.Manifest, error) {
	root := e.storageRoot()
	var out []*snapshot.Manifest

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is not a reason to report no snapshots.
			return nil //nolint:nilerr // deliberate: skip what cannot be read
		}
		if d.IsDir() || d.Name() != snapshot.ManifestName {
			return nil
		}
		m, err := snapshot.Read(filepath.Dir(path))
		if err != nil {
			// A manifest pgctl cannot parse is not a snapshot it can offer.
			// Listing must still work: the other snapshots are fine.
			return nil //nolint:nilerr // deliberate: see above
		}
		out = append(out, m)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out, nil
}

// Group is one environment's snapshots of one database. Retention applies per
// group: an environment's history of one database says nothing about how much
// of another's to keep.
type Group struct {
	Environment string
	Database    string
	Snapshots   []*snapshot.Manifest
}

// SnapshotGroups returns the snapshots grouped by environment and database,
// optionally restricted to one environment.
func (e *Engine) SnapshotGroups(env string) ([]Group, error) {
	all, err := e.Snapshots()
	if err != nil {
		return nil, err
	}

	index := map[string]*Group{}
	var order []string
	for _, m := range all {
		if env != "" && m.Environment != env {
			continue
		}
		key := m.Environment + "/" + m.Database
		if index[key] == nil {
			index[key] = &Group{Environment: m.Environment, Database: m.Database}
			order = append(order, key)
		}
		index[key].Snapshots = append(index[key].Snapshots, m)
	}

	sort.Strings(order)
	out := make([]Group, 0, len(order))
	for _, key := range order {
		out = append(out, *index[key])
	}
	return out, nil
}

// DeleteSnapshot removes a snapshot from local storage.
//
// The manifest goes first. A directory whose manifest is gone is not a snapshot
// pgctl will offer, so an interrupted delete leaves something invisible rather
// than something restorable-looking and half-deleted.
func (e *Engine) DeleteSnapshot(id string) error {
	dir := snapshot.Path(e.storageRoot(), id)
	if err := os.Remove(filepath.Join(dir, snapshot.ManifestName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete manifest of %s: %w", id, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete snapshot %s: %w", id, err)
	}
	return nil
}
