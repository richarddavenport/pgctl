package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// Local keeps snapshots in a directory tree, which is also the staging area a
// remote store uploads from and downloads into.
type Local struct{ Root string }

// NewLocal opens a local store rooted at dir.
func NewLocal(dir string) *Local { return &Local{Root: dir} }

// Describe names the store.
func (l *Local) Describe() string { return l.Root }

// Dir is where a snapshot lives.
func (l *Local) Dir(id string) string { return snapshot.Path(l.Root, id) }

// List finds snapshots by walking for manifests rather than by reading an
// index. An index is a second thing that can be wrong about what is on disk,
// and a snapshot's directory is already named after its id.
func (l *Local) List(_ context.Context) ([]string, error) {
	var ids []string
	err := filepath.WalkDir(l.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip what cannot be read
		}
		if d.IsDir() || d.Name() != snapshot.ManifestName {
			return nil
		}
		rel, err := filepath.Rel(l.Root, filepath.Dir(path))
		if err != nil {
			return nil //nolint:nilerr // as above
		}
		ids = append(ids, filepath.ToSlash(rel))
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	sort.Strings(ids)
	return ids, nil
}

// Manifest reads one snapshot's manifest.
func (l *Local) Manifest(_ context.Context, id string) (*snapshot.Manifest, error) {
	m, err := snapshot.Read(l.Dir(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	return m, err
}

// Put copies a snapshot into the store, which for a local store means copying
// only when the source is somewhere else.
func (l *Local) Put(_ context.Context, id, dir string, progress func(string, int64)) error {
	target := l.Dir(id)
	if sameDir(dir, target) {
		return nil
	}
	files, err := walkFiles(dir)
	if err != nil {
		return err
	}
	for _, rel := range files {
		n, err := copyFile(filepath.Join(dir, filepath.FromSlash(rel)),
			filepath.Join(target, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		if progress != nil {
			progress(rel, n)
		}
	}
	return nil
}

// Get copies a snapshot out of the store.
func (l *Local) Get(_ context.Context, id, dir string, files []string, progress func(string, int64)) error {
	source := l.Dir(id)
	if sameDir(source, dir) {
		return nil
	}
	if files == nil {
		var err error
		if files, err = walkFiles(source); err != nil {
			return err
		}
	}
	for _, rel := range files {
		n, err := copyFile(filepath.Join(source, filepath.FromSlash(rel)),
			filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		if progress != nil {
			progress(rel, n)
		}
	}
	return nil
}

// Delete removes a snapshot. The manifest goes first, so an interrupted delete
// leaves something invisible rather than something restorable-looking and
// half-deleted.
func (l *Local) Delete(_ context.Context, id string) error {
	dir := l.Dir(id)
	if err := os.Remove(filepath.Join(dir, snapshot.ManifestName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete manifest of %s: %w", id, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete snapshot %s: %w", id, err)
	}
	return nil
}

func sameDir(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}

func copyFile(from, to string) (int64, error) {
	f, err := os.Open(from)
	if err != nil {
		return 0, err
	}
	defer f.Close() //nolint:errcheck // read-only
	return copyToFile(to, f)
}
