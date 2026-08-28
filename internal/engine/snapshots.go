package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// openSnapshotIn resolves a snapshot id against everything reachable, local or
// remote, so that a laptop with no snapshots on it can still plan and run
// `pgctl apply prd/latest --to qat`.
func (e *Engine) openSnapshotIn(ctx context.Context, id string, report Reporter) (*snapshot.Manifest, bool, error) {
	if id == "" {
		return nil, false, errors.New("no snapshot named")
	}
	entries, err := e.Index(ctx, report)
	if err != nil {
		return nil, false, err
	}
	if len(entries) == 0 {
		return nil, false, fmt.Errorf("no snapshots in %s or in remote storage", e.storageRoot())
	}

	if env, ok := strings.CutSuffix(id, "/latest"); ok {
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].Manifest.Environment == env && entries[i].Manifest.Complete() {
				return entries[i].Manifest, entries[i].Local, nil
			}
		}
		return nil, false, fmt.Errorf("no complete snapshot of %q", env)
	}

	var matches []*Entry
	for _, entry := range entries {
		if entry.Manifest.ID == id || strings.HasSuffix(entry.Manifest.ID, "/"+id) {
			matches = append(matches, entry)
		}
	}
	switch len(matches) {
	case 0:
		return nil, false, fmt.Errorf("no snapshot %q", id)
	case 1:
		return matches[0].Manifest, matches[0].Local, nil
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.Manifest.ID)
		}
		return nil, false, fmt.Errorf("%q is ambiguous: %s", id, strings.Join(ids, ", "))
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
func (e *Engine) SnapshotGroups(ctx context.Context, env string, report Reporter) ([]Group, error) {
	entries, err := e.Index(ctx, report)
	if err != nil {
		return nil, err
	}

	index := map[string]*Group{}
	var order []string
	for _, entry := range entries {
		m := entry.Manifest
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

// DeleteSnapshotEverywhere removes a snapshot from local storage and from the
// remote store. A prune that only cleaned one of them would leave a retention
// policy that never actually bounds the bill.
func (e *Engine) DeleteSnapshotEverywhere(ctx context.Context, id string) error {
	if err := e.DeleteSnapshot(id); err != nil {
		return err
	}
	remote, err := e.remote(ctx, environmentOf(id))
	if err != nil || remote == nil {
		return err
	}
	return remote.Delete(ctx, id)
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

// Prune applies the retention policy, reporting what it would remove and
// removing it only when apply is set.
//
// Shared by the CLI and the TUI so that "what would go" is computed once: a
// retention policy that behaves differently depending on which front end ran
// it would be worse than none.
func (e *Engine) Prune(ctx context.Context, env string, apply bool, report Reporter) (string, error) {
	policy := snapshot.Policy{
		Daily:   e.cfg.Storage.Retention.Daily,
		Weekly:  e.cfg.Storage.Retention.Weekly,
		Monthly: e.cfg.Storage.Retention.Monthly,
	}
	if policy.Unset() {
		return "", fmt.Errorf("no storage.retention configured, so there is nothing to prune")
	}

	groups, err := e.SnapshotGroups(ctx, env, report)
	if err != nil {
		return "", err
	}

	var removed int
	var freed int64
	for _, group := range groups {
		keep, remove := snapshot.Keep(group.Snapshots, policy)
		if len(remove) == 0 {
			continue
		}
		report.step("prune", fmt.Sprintf("%s/%s: keeping %d, removing %d",
			group.Environment, group.Database, len(keep), len(remove)))
		for _, m := range remove {
			report.table("prune", m.ID, fmt.Sprintf("%s, taken %s",
				humanBytes(m.Bytes), m.StartedAt.Local().Format("2006-01-02 15:04")))
			removed++
			freed += m.Bytes
			if apply {
				if err := e.DeleteSnapshotEverywhere(ctx, m.ID); err != nil {
					return "", err
				}
			}
		}
	}

	switch {
	case removed == 0:
		return "nothing to prune", nil
	case apply:
		return fmt.Sprintf("removed %d snapshots, %s freed", removed, humanBytes(freed)), nil
	default:
		return fmt.Sprintf("%d snapshots, %s would be freed — turn on `Delete them` to do it",
			removed, humanBytes(freed)), nil
	}
}
