// Package store is where snapshots live once taken: on local disk, or in Azure
// Blob Storage.
//
// A snapshot is kept as a tree of files rather than one tarball, because that
// is the property the whole design rests on — see design/decisions.md #2 and
// #12. Moving the claims tables out of a 2 GB nightly should cost the claims
// tables, not 2 GB.
package store

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// Store holds snapshots.
//
// Paths crossing this interface are always slash-separated and relative to a
// snapshot's root — "manifest.json", "dump/2345.dat.zst" — so that a blob name
// and a file path are the same string on either side.
type Store interface {
	// Describe names the store for a log line.
	Describe() string

	// List returns the snapshot ids present, oldest first by id (which sorts
	// chronologically, since the last path element is a UTC timestamp).
	List(ctx context.Context) ([]string, error)

	// Manifest reads one snapshot's manifest without downloading the rest.
	Manifest(ctx context.Context, id string) (*snapshot.Manifest, error)

	// Put uploads a snapshot from a local directory.
	Put(ctx context.Context, id, dir string, progress func(file string, bytes int64)) error

	// Get downloads a snapshot into a local directory. files restricts it to
	// those relative paths; nil means everything.
	Get(ctx context.Context, id, dir string, files []string, progress func(file string, bytes int64)) error

	// Delete removes a snapshot.
	Delete(ctx context.Context, id string) error
}

// walkFiles lists a snapshot directory's files as slash-separated relative
// paths, manifest last.
//
// The manifest goes last on upload for the same reason it is written last
// locally: a snapshot whose manifest is present is a snapshot that is complete.
// An interrupted upload leaves files nothing points at, which List does not
// report and a restore therefore cannot pick.
func walkFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == snapshot.ManifestName {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return append(files, snapshot.ManifestName), nil
}

// copyToFile writes a reader to a path, creating parents.
func copyToFile(path string, r io.Reader) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close() //nolint:errcheck // the error that matters is from Copy
	n, err := io.Copy(f, r)
	if err != nil {
		return n, err
	}
	return n, f.Close()
}

// blobName is a file's full name in a flat namespace.
func blobName(id, rel string) string { return id + "/" + rel }

// ErrNotFound is returned when a snapshot or file is not in the store.
var ErrNotFound = fmt.Errorf("not found in store")
