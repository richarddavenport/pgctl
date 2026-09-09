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

	"github.com/richarddavenport/pgctl/internal/config"
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
			if entries[i].Manifest.Connection == env && entries[i].Manifest.Complete() {
				return entries[i].Manifest, entries[i].Local(), nil
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
		return matches[0].Manifest, matches[0].Local(), nil
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
	Connection string
	Database   string
	Snapshots  []*snapshot.Manifest
}

// SnapshotGroups returns the snapshots grouped by environment and database,
// optionally restricted to one environment.
func (e *Engine) SnapshotGroups(ctx context.Context, connection string, report Reporter) ([]Group, error) {
	return e.snapshotGroupsIn(ctx, connection, "", report)
}

// snapshotGroupsIn is the same, restricted to the snapshots present in one
// destination.
//
// Retention is per destination now, so the grouping has to be too: a snapshot
// the local policy has finished with may be the only copy the archive has, and
// a group built from the merged index would count it once and delete it twice.
func (e *Engine) snapshotGroupsIn(ctx context.Context, connection, destination string,
	report Reporter) ([]Group, error) {

	entries, err := e.Index(ctx, report)
	if err != nil {
		return nil, err
	}

	index := map[string]*Group{}
	var order []string
	for _, entry := range entries {
		m := entry.Manifest
		if connection != "" && m.Connection != connection {
			continue
		}
		if destination != "" && !entry.isAt(destination) {
			continue
		}
		key := m.Connection + "/" + m.Database
		if index[key] == nil {
			index[key] = &Group{Connection: m.Connection, Database: m.Database}
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
	remotes, err := e.remotes(ctx)
	for _, remote := range remotes {
		if dErr := remote.Store.Delete(ctx, id); dErr != nil && err == nil {
			err = dErr
		}
	}
	return err
}

// retentionPolicies is the destinations that have a policy, by name.
//
// Local's is storage.retention; a remote's is its own. A destination missing
// from this map keeps everything.
func (e *Engine) retentionPolicies() map[string]snapshot.Policy {
	out := map[string]snapshot.Policy{}
	local := policyOf(e.cfg.Storage.Retention)
	if !local.Unset() {
		out[config.LocalStorage] = local
	}
	for _, r := range e.cfg.Remotes() {
		if p := policyOf(r.Retention); !p.Unset() {
			out[r.Name] = p
		}
	}
	return out
}

func policyOf(r config.Retention) snapshot.Policy {
	return snapshot.Policy{Daily: r.Daily, Weekly: r.Weekly, Monthly: r.Monthly}
}

// DeleteSnapshotFrom removes a snapshot from ONE destination.
//
// What a per-destination retention policy needs: a snapshot the local policy
// has finished with is not one the archive policy has finished with, and the
// version of prune that deleted everywhere could not express the difference.
func (e *Engine) DeleteSnapshotFrom(ctx context.Context, id, destination string) error {
	if destination == config.LocalStorage {
		return e.DeleteSnapshot(id)
	}
	remote, err := e.remoteNamed(ctx, destination)
	if err != nil {
		return err
	}
	return remote.Store.Delete(ctx, id)
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

// Prune applies each destination's retention policy, reporting what it would
// remove and removing it only when apply is set.
//
// Per destination, because the policies are: a laptop keeping two days and an
// archive container keeping a year is the ordinary arrangement, and the version
// of this that ran one policy and then called DeleteSnapshotEverywhere could
// not express it — it deleted the archive's only copy on the day the local one
// aged out.
//
// A destination with no policy is skipped and SAID to be skipped. An unset
// policy deletes nothing, which is what makes a fresh config safe to prune, and
// silence would make that indistinguishable from a policy that found nothing to
// do.
//
// Shared by the CLI and the TUI so that "what would go" is computed once: a
// retention policy that behaved differently depending on which front end ran it
// would be worse than none.
func (e *Engine) Prune(ctx context.Context, env string, apply bool, report Reporter) (string, error) {
	policies := e.retentionPolicies()
	if len(policies) == 0 {
		return "", fmt.Errorf("no retention configured on any destination (%s), "+
			"so there is nothing to prune", strings.Join(e.cfg.Destinations(), ", "))
	}

	var removed int
	var freed int64
	for _, destination := range e.cfg.Destinations() {
		policy, ok := policies[destination]
		if !ok {
			report.step("prune", fmt.Sprintf("%s: no retention configured, keeping everything",
				destination))
			continue
		}

		groups, err := e.snapshotGroupsIn(ctx, env, destination, report)
		if err != nil {
			return "", err
		}
		for _, group := range groups {
			keep, remove := snapshot.Keep(group.Snapshots, policy)
			if len(remove) == 0 {
				continue
			}
			report.step("prune", fmt.Sprintf("%s %s/%s: keeping %d, removing %d",
				destination, group.Connection, group.Database, len(keep), len(remove)))
			for _, m := range remove {
				report.table("prune", m.ID, fmt.Sprintf("%s from %s, taken %s",
					humanBytes(m.Bytes), destination,
					m.StartedAt.Local().Format("2006-01-02 15:04")))
				removed++
				freed += m.Bytes
				if apply {
					if err := e.DeleteSnapshotFrom(ctx, m.ID, destination); err != nil {
						return "", err
					}
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
