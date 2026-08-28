package engine

import (
	"context"
	"fmt"
	"sort"

	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// Entry is one snapshot and where it is. A snapshot can be in both places at
// once — the nightly takes it locally and uploads it — and an operator needs to
// know which, because one of them costs a download.
type Entry struct {
	Manifest *snapshot.Manifest
	Local    bool
	Remote   bool
}

// Location describes where the snapshot is, for a listing.
func (e Entry) Location() string {
	switch {
	case e.Local && e.Remote:
		return "local+remote"
	case e.Local:
		return "local"
	default:
		return "remote"
	}
}

// Index lists every snapshot pgctl can reach, oldest first.
//
// Remote listing failures are reported as warnings rather than errors: an
// expired storage key should not stop an operator seeing, or restoring, what is
// already on their disk.
func (e *Engine) Index(ctx context.Context, report Reporter) ([]*Entry, error) {
	local, err := e.Snapshots()
	if err != nil {
		return nil, err
	}

	index := map[string]*Entry{}
	var order []string
	for _, m := range local {
		index[m.ID] = &Entry{Manifest: m, Local: true}
		order = append(order, m.ID)
	}

	remote, err := e.remote(ctx, "")
	if err != nil {
		report.warn(fmt.Sprintf("remote storage unavailable: %v", err))
	} else if remote != nil {
		ids, err := remote.List(ctx)
		if err != nil {
			report.warn(fmt.Sprintf("could not list %s: %v", remote.Describe(), err))
		}
		for _, id := range ids {
			if entry, ok := index[id]; ok {
				entry.Remote = true
				continue
			}
			man, err := remote.Manifest(ctx, id)
			if err != nil {
				report.warn(fmt.Sprintf("could not read the manifest of %s: %v", id, err))
				continue
			}
			index[id] = &Entry{Manifest: man, Remote: true}
			order = append(order, id)
		}
	}

	sort.Strings(order)
	out := make([]*Entry, 0, len(order))
	for _, id := range order {
		out = append(out, index[id])
	}
	// Ids sort chronologically within an environment and database, but not
	// across them, so the final order is by time.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Manifest.StartedAt.Before(out[j].Manifest.StartedAt)
	})
	return out, nil
}
