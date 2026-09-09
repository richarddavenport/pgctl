package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// Entry is one snapshot and every place it is.
//
// A snapshot can be in several destinations at once — the nightly takes it
// locally and pushes it, and a config can declare two remotes — and an operator
// needs to know which, because a download from one costs what a download from
// another does not.
type Entry struct {
	Manifest *snapshot.Manifest

	// At is the destination names holding it, in config order with local first.
	//
	// Names rather than the Local and Remote bools this replaces: two bools
	// cannot say WHICH remote, and the moment there are two of them "remote"
	// stops being an answer. The index works this out by asking each
	// destination what it holds — a snapshot records nothing about where it was
	// sent, so there is no field anywhere that can go stale.
	At []string
}

// Local reports whether the snapshot is on this machine.
func (e Entry) Local() bool { return e.isAt(config.LocalStorage) }

// Remote reports whether it is in any declared remote.
func (e Entry) Remote() bool {
	for _, name := range e.At {
		if name != config.LocalStorage {
			return true
		}
	}
	return false
}

func (e Entry) isAt(name string) bool {
	for _, at := range e.At {
		if at == name {
			return true
		}
	}
	return false
}

// Location describes where the snapshot is, for a listing.
//
// The names, joined — "local", "local+snapshots", "archive". Not "local+remote",
// which was the whole answer when there could only be one remote and is now a
// question rather than an answer.
func (e Entry) Location() string {
	if len(e.At) == 0 {
		return "nowhere"
	}
	return strings.Join(e.At, "+")
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
		index[m.ID] = &Entry{Manifest: m, At: []string{config.LocalStorage}}
		order = append(order, m.ID)
	}

	// Every declared remote, asked in config order, so Entry.At comes out in
	// that order too and a listing does not shuffle its "where" column between
	// runs.
	remotes, err := e.remotes(ctx)
	if err != nil {
		// A warning rather than a failure: an expired key on one container
		// should not stop an operator seeing, or restoring, what is on their
		// disk or in the others.
		report.warn(fmt.Sprintf("storage remote unavailable: %v", err))
	}
	for _, remote := range remotes {
		ids, err := remote.Store.List(ctx)
		if err != nil {
			report.warn(fmt.Sprintf("could not list %s: %v", remote.Name, err))
			continue
		}
		for _, id := range ids {
			if entry, ok := index[id]; ok {
				entry.At = append(entry.At, remote.Name)
				continue
			}
			man, err := remote.Store.Manifest(ctx, id)
			if err != nil {
				report.warn(fmt.Sprintf("could not read the manifest of %s in %s: %v",
					id, remote.Name, err))
				continue
			}
			index[id] = &Entry{Manifest: man, At: []string{remote.Name}}
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
