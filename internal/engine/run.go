package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// A Run is one press of `n`: every database snapshotted from one connection at
// one instant.
//
// It is what a person calls "a snapshot", and both front ends use that word for
// it — the type is called Run because the interface already has a Runs panel
// meaning OPERATIONS, and two things called runs on one screen would be worse
// than a type name nobody types.
//
// # Why this exists
//
// It is what a person means by "a snapshot", and the model did not have it. A
// snapshot id is `connection/database/timestamp`, so a six-database run was six
// snapshots that merely shared a timestamp — and the comment in dump.go claiming
// they were "one snapshot set rather than six unrelated ones" was describing an
// intention nothing enforced.
//
// Three things followed from the gap, and all three were reported by somebody
// using it rather than found by reading:
//
//   - the interface made you choose databases twice, once to take a snapshot
//     and again to restore it, when the thing being restored is the run;
//   - `pgctl ls` listed six rows for one act;
//   - `pgctl apply prd/latest --to qat` restored ONE database — whichever
//     sorted last — which is a latent bug in the command a nightly restore
//     would use.
//
// # Why it needs no migration
//
// The members already share a timestamp, so a run is DERIVED rather than
// stored: `prd/20260909T153059Z` is the set of `prd/<db>/20260909T153059Z`. The
// storage layout, the manifests and the ids are untouched, and a script naming
// a single-database id still gets exactly that.
type Run struct {
	// ID is `connection/timestamp`, which is the members' ids with the database
	// removed — so it is addressable, sorts chronologically, and cannot collide
	// with a member id, which has three segments to this one's two.
	ID         string
	Connection string
	At         time.Time

	// Members are the per-database snapshots, ordered by database name.
	Members []*Entry
}

// runID is a run's id from its parts.
func runID(connection string, at time.Time) string {
	return connection + "/" + at.UTC().Format(snapshot.TimeLayout)
}

// Databases is the databases the run covers, in order.
func (r *Run) Databases() []string {
	out := make([]string, 0, len(r.Members))
	for _, m := range r.Members {
		out = append(out, m.Manifest.Database)
	}
	return out
}

// Bytes is the whole run's size.
func (r *Run) Bytes() int64 {
	var total int64
	for _, m := range r.Members {
		total += m.Manifest.Bytes
	}
	return total
}

// Complete reports a run every member of which finished.
//
// One unfinished member makes the RUN incomplete, because a run is what gets
// restored: five good databases and one truncated dump is not a state of the
// estate anybody wants restored, and the alternative — calling it complete and
// refusing one member at apply time — is a refusal an hour into the work.
func (r *Run) Complete() bool {
	for _, m := range r.Members {
		if !m.Manifest.Complete() {
			return false
		}
	}
	return len(r.Members) > 0
}

// Local reports a run every member of which is on this machine, which is what
// decides whether applying it costs a download.
func (r *Run) Local() bool {
	for _, m := range r.Members {
		if !m.Local() {
			return false
		}
	}
	return len(r.Members) > 0
}

// Locations is where the run is, by name, for a listing: the destinations every
// member of it is in.
func (r *Run) Locations() []string {
	if len(r.Members) == 0 {
		return nil
	}
	// The intersection, not the union: a run is somewhere only if all of it is.
	// A union would report `local+snapshots` for a run whose product-development
	// member never uploaded, which is the one member you would need.
	counts := map[string]int{}
	for _, m := range r.Members {
		for _, at := range m.At {
			counts[at]++
		}
	}
	var out []string
	for _, at := range r.Members[0].At {
		if counts[at] == len(r.Members) {
			out = append(out, at)
		}
	}
	return out
}

// Member is the run's snapshot of one database.
func (r *Run) Member(database string) (*Entry, bool) {
	for _, m := range r.Members {
		if m.Manifest.Database == database {
			return m, true
		}
	}
	return nil, false
}

// Runs groups everything the index holds into runs, oldest first.
func (e *Engine) Runs(ctx context.Context, report Reporter) ([]*Run, error) {
	entries, err := e.Index(ctx, report)
	if err != nil {
		return nil, err
	}
	return groupRuns(entries), nil
}

// GroupRuns groups an index into runs, oldest first.
//
// Exported and separated from the reading, so a front end that already holds an
// index groups it without a second listing, and a test groups a fixture without
// a store.
func GroupRuns(entries []*Entry) []*Run { return groupRuns(entries) }

// groupRuns is the grouping, separated from the reading so a test can group a
// fixture without a store.
func groupRuns(entries []*Entry) []*Run {
	index := map[string]*Run{}
	var order []string
	for _, entry := range entries {
		m := entry.Manifest
		id := runID(m.Connection, m.StartedAt)
		if index[id] == nil {
			index[id] = &Run{
				ID:         id,
				Connection: m.Connection,
				// The run's instant is the shared one the members were given,
				// not the earliest member's finish: `pgctl snapshot` stamps one
				// `at` across the whole run for exactly this reason.
				At: m.StartedAt.UTC(),
			}
			order = append(order, id)
		}
		index[id].Members = append(index[id].Members, entry)
	}

	out := make([]*Run, 0, len(order))
	for _, id := range order {
		run := index[id]
		sort.Slice(run.Members, func(i, j int) bool {
			return run.Members[i].Manifest.Database < run.Members[j].Manifest.Database
		})
		out = append(out, run)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// OpenRun resolves a run id: `<connection>/<timestamp>`,
// `<connection>/latest`, a bare timestamp when it is unambiguous, or a
// single-database snapshot id, which resolves to a run of one member.
//
// The last case is what keeps every script that names a full id working, and it
// is not a special case in the caller: an apply of one database and an apply of
// six differ in how many plans they produce, not in kind.
func (e *Engine) OpenRun(ctx context.Context, id string, report Reporter) (*Run, error) {
	if id == "" {
		return nil, fmt.Errorf("no snapshot named")
	}
	runs, err := e.Runs(ctx, report)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, fmt.Errorf("no snapshots in %s or in remote storage", e.storageRoot())
	}

	// `<connection>/latest`: the newest COMPLETE run, which is the whole point
	// of the word. The version this replaces returned the newest single
	// database snapshot of the connection, so `apply prd/latest` restored
	// whichever database happened to sort last.
	if connection, ok := strings.CutSuffix(id, "/latest"); ok {
		for i := len(runs) - 1; i >= 0; i-- {
			if runs[i].Connection == connection && runs[i].Complete() {
				return runs[i], nil
			}
		}
		return nil, fmt.Errorf("no complete run of %q", connection)
	}

	var matches []*Run
	for _, run := range runs {
		switch {
		case run.ID == id, strings.HasSuffix(run.ID, "/"+id):
			matches = append(matches, run)
			continue
		}
		// A member's own id names a run of one: the database is the selection.
		for _, m := range run.Members {
			if m.Manifest.ID == id || strings.HasSuffix(m.Manifest.ID, "/"+id) {
				matches = append(matches, &Run{
					ID:         run.ID,
					Connection: run.Connection,
					At:         run.At,
					Members:    []*Entry{m},
				})
			}
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no snapshot %q", id)
	case 1:
		return matches[0], nil
	default:
		var ids []string
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		return nil, fmt.Errorf("%q is ambiguous: %s", id, strings.Join(ids, ", "))
	}
}
