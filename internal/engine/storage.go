package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/snapshot"
	"github.com/richarddavenport/pgctl/internal/store"
)

// Remote is one opened destination: its declared name, and the store behind it.
//
// A name rather than an index, everywhere. A snapshot records nothing about
// where it was sent — the index works that out by asking each destination what
// it holds — so the name is the only handle, and it is what a front end offers,
// what --to-storage takes, and what an event says.
type Remote struct {
	Name  string
	Store store.Store
}

// remotes opens every declared remote.
//
// A remote whose credentials are missing is an ERROR from this function and a
// warning at most call sites: an expired key on the archive container should not
// stop an operator listing, or restoring, what is on their disk or in the other
// one. Each caller decides, which is why this returns them one by one rather
// than deciding here.
func (e *Engine) remotes(ctx context.Context) ([]Remote, error) {
	var out []Remote
	var firstErr error
	for _, r := range e.cfg.Remotes() {
		st, err := e.openRemote(ctx, r)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, Remote{Name: r.Name, Store: st})
	}
	return out, firstErr
}

// remoteNamed opens one remote by name.
func (e *Engine) remoteNamed(ctx context.Context, name string) (Remote, error) {
	r, ok := e.cfg.RemoteByName(name)
	if !ok {
		return Remote{}, fmt.Errorf("no storage destination named %q — declared: %s",
			name, strings.Join(e.cfg.Destinations(), ", "))
	}
	st, err := e.openRemote(ctx, r)
	if err != nil {
		return Remote{}, err
	}
	return Remote{Name: r.Name, Store: st}, nil
}

// openRemote builds the store for one declared remote.
//
// Credentials come from where they already are — an environment variable, or a
// command that prints one — for the same reason database passwords come from
// ~/.pgpass rather than from a scheme pgctl invented. See credential.go for
// which wins and what is never logged.
func (e *Engine) openRemote(ctx context.Context, r config.Remote) (store.Store, error) {
	if r.Kind != config.StorageAzureBlob {
		return nil, fmt.Errorf("storage remote %q: unknown kind %q", r.Name, r.Kind)
	}
	account, err := e.credential(ctx, "account", r.AccountCommand, r.AccountEnv)
	if err != nil {
		return nil, fmt.Errorf("storage remote %q: %w", r.Name, err)
	}
	key, err := e.credential(ctx, "key", r.KeyCommand, r.KeyEnv)
	if err != nil {
		return nil, fmt.Errorf("storage remote %q: %w", r.Name, err)
	}
	return store.NewBlob(account, key, r.Container, r.Endpoint)
}

// Place puts a finished snapshot where it was asked to go.
//
// destinations are names — config.LocalStorage plus any declared remote. What
// makes this more than a loop is the last line: a snapshot NOT destined for
// local is deleted from disk once every push has succeeded, which is what makes
// "send it to the shared account and do not fill my laptop" expressible. The
// order matters and is the only safe one — upload, verify every upload, then
// delete. A failure anywhere leaves the local copy, which is the recoverable
// state.
func (e *Engine) Place(ctx context.Context, id string, destinations []string, report Reporter) error {
	keepLocal := false
	var wanted []string
	for _, d := range destinations {
		if d == config.LocalStorage {
			keepLocal = true
			continue
		}
		wanted = append(wanted, d)
	}
	if !keepLocal && len(wanted) == 0 {
		return fmt.Errorf("a snapshot needs somewhere to be: %s",
			strings.Join(e.cfg.Destinations(), ", "))
	}

	for _, name := range wanted {
		if err := e.Push(ctx, id, name, report); err != nil {
			return err
		}
	}

	if !keepLocal {
		report.step("place", fmt.Sprintf("%s is in %s; removing the local copy",
			id, strings.Join(wanted, ", ")))
		return e.DeleteSnapshot(id)
	}
	return nil
}

// Push uploads a local snapshot to one remote.
func (e *Engine) Push(ctx context.Context, id, destination string, report Reporter) error {
	remote, err := e.remoteNamed(ctx, destination)
	if err != nil {
		return err
	}

	dir := snapshot.Path(e.storageRoot(), id)
	if _, err := snapshot.Read(dir); err != nil {
		return fmt.Errorf("snapshot %s is not in local storage: %w", id, err)
	}

	report.step("push", fmt.Sprintf("%s to %s", id, remote.Store.Describe()))
	var files int
	var bytes int64
	err = remote.Store.Put(ctx, id, dir, func(_ string, n int64) {
		files++
		bytes += n
	})
	if err != nil {
		return err
	}
	report.send(Event{Kind: EventDone, Step: "push", Bytes: bytes,
		Message: fmt.Sprintf("uploaded %d files to %s, %s", files, remote.Name, humanBytes(bytes))})
	return nil
}

// Fetch downloads a snapshot from the remote store into local storage, taking
// only the files an apply of this plan will read.
//
// This is what keeping a snapshot as a tree buys: moving the claims tables out
// of a 2 GB nightly downloads the claims tables and the table of contents, not
// 2 GB. A whole-database apply needs everything, and says so by passing no
// selection.
func (e *Engine) Fetch(ctx context.Context, id string, selection []string, report Reporter) error {
	remotes, err := e.remotes(ctx)
	if len(remotes) == 0 {
		// No remote could be opened. When none is declared that is not a
		// failure — the snapshot is local or the plan would not have got here.
		return err
	}

	// The first remote that HAS it, in config order. A snapshot can be in
	// several and they are not equivalent — a cool-tier archive and a working
	// container differ in what a download costs — so the order in the file is
	// read as an order of preference, which is the only statement of one
	// anybody has made.
	source, man, err := e.sourceFor(ctx, id, remotes)
	if err != nil {
		return err
	}

	dir := snapshot.Path(e.storageRoot(), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	var files []string
	if len(selection) > 0 {
		if files, err = e.filesFor(ctx, source.Store, id, dir, man, selection); err != nil {
			return err
		}
	}

	what := "everything"
	if files != nil {
		what = fmt.Sprintf("%d files for %d tables", len(files), len(selection))
	}
	report.step("fetch", fmt.Sprintf("%s from %s (%s)", id, source.Name, what))

	var count int
	var bytes int64
	if err := source.Store.Get(ctx, id, dir, files, func(_ string, n int64) {
		count++
		bytes += n
	}); err != nil {
		return err
	}
	report.send(Event{Kind: EventProgress, Step: "fetch",
		Message: fmt.Sprintf("downloaded %d files from %s, %s",
			count, source.Name, humanBytes(bytes))})
	return nil
}

// sourceFor is the remote a download should come from, and its manifest.
//
// Reading the manifest IS the test for "does this remote have it": a store that
// cannot produce one either does not hold the snapshot or cannot be reached,
// and in both cases the next remote is the thing to try. The last error is kept
// so that "in none of them" can say what went wrong on the way.
func (e *Engine) sourceFor(ctx context.Context, id string, remotes []Remote) (Remote, *snapshot.Manifest, error) {
	var names []string
	var lastErr error
	for _, r := range remotes {
		names = append(names, r.Name)
		man, err := r.Store.Manifest(ctx, id)
		if err != nil {
			lastErr = err
			continue
		}
		return r, man, nil
	}
	if lastErr != nil {
		return Remote{}, nil, fmt.Errorf("snapshot %s is in none of %s: %w",
			id, strings.Join(names, ", "), lastErr)
	}
	return Remote{}, nil, fmt.Errorf("snapshot %s is in none of %s",
		id, strings.Join(names, ", "))
}

// filesFor works out which of a snapshot's files an apply of the selection
// reads: the table of contents, the archive files holding those tables' data,
// and any COPY sidecars.
//
// The table of contents has to be downloaded first, because it is the only
// thing that maps a table to the file its data is in — a directory-format
// archive names data files after the dump id, not after the table.
func (e *Engine) filesFor(ctx context.Context, remote store.Store, id, dir string,
	man *snapshot.Manifest, selection []string) ([]string, error) {

	toc := filepath.ToSlash(filepath.Join(snapshot.DumpDir, "toc.dat"))
	if err := remote.Get(ctx, id, dir, []string{toc}, nil); err != nil {
		return nil, err
	}

	entries, err := ReadTOC(ctx, filepath.Join(dir, snapshot.DumpDir))
	if err != nil {
		return nil, err
	}

	wanted := set(selection)
	files := []string{toc}
	for _, entry := range entries {
		if entry.Desc != "TABLE DATA" || !wanted[entry.Qualified()] {
			continue
		}
		// A directory-format archive stores each entry's data as
		// <dumpId>.dat, compressed with the suffix its compression implies.
		files = append(files, filepath.ToSlash(filepath.Join(snapshot.DumpDir, entry.DumpID+".dat"+
			compressionSuffix(man.Compression))))
	}
	for _, table := range man.Filtered() {
		if wanted[table.Name] && table.File != "" {
			files = append(files, table.File)
		}
	}
	return append(files, snapshot.ManifestName), nil
}

// compressionSuffix is the extension pg_dump gives a directory archive's data
// files for a --compress value.
func compressionSuffix(compression string) string {
	method, _, _ := strings.Cut(compression, ":")
	switch method {
	case "zstd":
		return ".zst"
	case "lz4":
		return ".lz4"
	case "none", "0":
		return ""
	default:
		// gzip is pg_dump's default, and a bare level ("6") means gzip.
		return ".gz"
	}
}
