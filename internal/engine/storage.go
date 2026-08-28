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

// remote opens the configured remote store, or returns nil when storage is
// local only.
//
// Credentials come from an environment's secrets file, since that is where the
// storage account key already lives. Which environment: the one named by
// storage.credentialsFrom, defaulting to the environment being read or written,
// so that a project with one snapshots account names it once.
func (e *Engine) remote(_ context.Context, _ string) (store.Store, error) {
	s := e.cfg.Storage
	if s.Kind != config.StorageAzureBlob {
		return nil, nil
	}

	// From the environment, for the same reason database passwords come from
	// ~/.pgpass: a credential belongs where the tools that need it already look
	// for it, not in a scheme pgctl invented. CI sets these from its secret
	// store; a person exports them, or lets `az` put them there.
	account, key := os.Getenv(s.AccountEnv), os.Getenv(s.KeyEnv)
	if account == "" || key == "" {
		return nil, fmt.Errorf("%s and %s must be set to reach the snapshot container",
			s.AccountEnv, s.KeyEnv)
	}
	return store.NewBlob(account, key, s.Container, s.Endpoint)
}

// Push uploads a local snapshot to the remote store.
func (e *Engine) Push(ctx context.Context, id string, report Reporter) error {
	remote, err := e.remote(ctx, connectionOf(id))
	if err != nil {
		return err
	}
	if remote == nil {
		// Local-only storage: the snapshot is already where it belongs.
		return nil
	}

	dir := snapshot.Path(e.storageRoot(), id)
	if _, err := snapshot.Read(dir); err != nil {
		return fmt.Errorf("snapshot %s is not in local storage: %w", id, err)
	}

	report.step("push", fmt.Sprintf("%s to %s", id, remote.Describe()))
	var files int
	var bytes int64
	err = remote.Put(ctx, id, dir, func(_ string, n int64) {
		files++
		bytes += n
	})
	if err != nil {
		return err
	}
	report.send(Event{Kind: EventDone, Step: "push", Bytes: bytes,
		Message: fmt.Sprintf("uploaded %d files, %s", files, humanBytes(bytes))})
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
	remote, err := e.remote(ctx, connectionOf(id))
	if err != nil {
		return err
	}
	if remote == nil {
		return nil
	}

	dir := snapshot.Path(e.storageRoot(), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	man, err := remote.Manifest(ctx, id)
	if err != nil {
		return err
	}

	var files []string
	if len(selection) > 0 {
		if files, err = e.filesFor(ctx, remote, id, dir, man, selection); err != nil {
			return err
		}
	}

	what := "everything"
	if files != nil {
		what = fmt.Sprintf("%d files for %d tables", len(files), len(selection))
	}
	report.step("fetch", fmt.Sprintf("%s from %s (%s)", id, remote.Describe(), what))

	var count int
	var bytes int64
	if err := remote.Get(ctx, id, dir, files, func(_ string, n int64) {
		count++
		bytes += n
	}); err != nil {
		return err
	}
	report.send(Event{Kind: EventProgress, Step: "fetch",
		Message: fmt.Sprintf("downloaded %d files, %s", count, humanBytes(bytes))})
	return nil
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

// connectionOf is the connection part of a snapshot id.
func connectionOf(id string) string {
	env, _, _ := strings.Cut(id, "/")
	return env
}
