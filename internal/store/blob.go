package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// Blob keeps snapshots in an Azure Blob Storage container, one blob per file of
// the snapshot, named "<snapshot id>/<path within the snapshot>".
//
// Flat blobs with slashes in their names, not a tarball: a set-level restore
// then downloads the table of contents and the files for the tables it needs.
// See design/decisions.md #12.
type Blob struct {
	client    *azblob.Client
	container string
	account   string

	// concurrency bounds parallel transfers. A snapshot is a few hundred files
	// of very uneven size, so several at once matters more than a large buffer
	// for any one of them.
	concurrency int
}

// NewBlob opens a container using a storage account key.
//
// Key auth rather than a managed identity: the keys are already in the
// environment files pgctl decrypts, the same ones swarmctl hands to `az storage
// file upload`, and a nightly job that runs wherever CI puts it cannot rely on
// an identity attached to a particular VM.
func NewBlob(account, key, containerName, endpoint string) (*Blob, error) {
	if account == "" || key == "" {
		return nil, fmt.Errorf("azure storage account and key are required")
	}
	cred, err := azblob.NewSharedKeyCredential(account, key)
	if err != nil {
		return nil, fmt.Errorf("azure credential: %w", err)
	}
	url := endpoint
	if url == "" {
		url = fmt.Sprintf("https://%s.blob.core.windows.net/", account)
	}
	client, err := azblob.NewClientWithSharedKeyCredential(url, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azure blob client: %w", err)
	}
	return &Blob{client: client, container: containerName, account: account, concurrency: 8}, nil
}

// Describe names the store.
func (b *Blob) Describe() string {
	return fmt.Sprintf("azure blob %s/%s", b.account, b.container)
}

// List enumerates snapshots.
//
// Walked hierarchically — environments, then databases, then timestamps —
// rather than by listing every blob and filtering. A snapshot is a few hundred
// blobs, so a flat listing of a year of nightlies is tens of thousands of names
// to fetch in order to learn a few dozen ids.
func (b *Blob) List(ctx context.Context) ([]string, error) {
	envs, err := b.children(ctx, "")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, env := range envs {
		databases, err := b.children(ctx, env)
		if err != nil {
			return nil, err
		}
		for _, db := range databases {
			stamps, err := b.children(ctx, db)
			if err != nil {
				return nil, err
			}
			ids = append(ids, stamps...)
		}
	}
	// Trailing slashes come off, and the ids sort chronologically because the
	// last element is a sortable UTC timestamp.
	for i := range ids {
		ids[i] = strings.TrimSuffix(ids[i], "/")
	}
	sort.Strings(ids)
	return ids, nil
}

// children lists the immediate sub-prefixes of a prefix.
func (b *Blob) children(ctx context.Context, prefix string) ([]string, error) {
	cc := b.client.ServiceClient().NewContainerClient(b.container)
	opts := &container.ListBlobsHierarchyOptions{}
	if prefix != "" {
		opts.Prefix = &prefix
	}
	pager := cc.NewListBlobsHierarchyPager("/", opts)

	var out []string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", prefix, err)
		}
		for _, p := range page.Segment.BlobPrefixes {
			if p.Name != nil {
				out = append(out, *p.Name)
			}
		}
	}
	return out, nil
}

// Manifest reads one snapshot's manifest without downloading the rest of it.
func (b *Blob) Manifest(ctx context.Context, id string) (*snapshot.Manifest, error) {
	resp, err := b.client.DownloadStream(ctx, b.container, blobName(id, snapshot.ManifestName), nil)
	if err != nil {
		if bloberror.HasCode(err, bloberror.BlobNotFound) {
			return nil, fmt.Errorf("%s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("read manifest of %s: %w", id, err)
	}
	body := resp.NewRetryReader(ctx, nil)
	defer body.Close() //nolint:errcheck // read-only

	var m snapshot.Manifest
	if err := json.NewDecoder(body).Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest of %s: %w", id, err)
	}
	return &m, nil
}

// Put uploads a snapshot. The manifest is uploaded last, so a snapshot that
// List can see is a snapshot that arrived whole.
func (b *Blob) Put(ctx context.Context, id, dir string, progress func(string, int64)) error {
	if err := b.EnsureContainer(ctx); err != nil {
		return err
	}
	files, err := walkFiles(dir)
	if err != nil {
		return err
	}
	return b.each(ctx, files, progress, func(ctx context.Context, rel string) (int64, error) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		f, err := os.Open(path)
		if err != nil {
			return 0, err
		}
		defer f.Close() //nolint:errcheck // read-only

		info, err := f.Stat()
		if err != nil {
			return 0, err
		}
		if _, err := b.client.UploadFile(ctx, b.container, blobName(id, rel), f, nil); err != nil {
			return 0, fmt.Errorf("upload %s: %w", rel, err)
		}
		return info.Size(), nil
	})
}

// Get downloads a snapshot, or the named files of one.
func (b *Blob) Get(ctx context.Context, id, dir string, files []string, progress func(string, int64)) error {
	if files == nil {
		var err error
		if files, err = b.files(ctx, id); err != nil {
			return err
		}
	}
	return b.each(ctx, files, progress, func(ctx context.Context, rel string) (int64, error) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return 0, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return 0, err
		}
		defer f.Close() //nolint:errcheck // the error that matters comes from DownloadFile

		n, err := b.client.DownloadFile(ctx, b.container, blobName(id, rel), f, nil)
		if err != nil {
			if bloberror.HasCode(err, bloberror.BlobNotFound) {
				return 0, fmt.Errorf("%s in %s: %w", rel, id, ErrNotFound)
			}
			return 0, fmt.Errorf("download %s: %w", rel, err)
		}
		return n, f.Close()
	})
}

// files lists a snapshot's blobs as relative paths.
func (b *Blob) files(ctx context.Context, id string) ([]string, error) {
	prefix := id + "/"
	pager := b.client.NewListBlobsFlatPager(b.container, &azblob.ListBlobsFlatOptions{Prefix: &prefix})

	var out []string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", id, err)
		}
		for _, item := range page.Segment.BlobItems {
			if item.Name == nil {
				continue
			}
			out = append(out, strings.TrimPrefix(*item.Name, prefix))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	sort.Strings(out)
	return out, nil
}

// Delete removes a snapshot's blobs, manifest first.
func (b *Blob) Delete(ctx context.Context, id string) error {
	if err := b.deleteBlob(ctx, blobName(id, snapshot.ManifestName)); err != nil {
		return err
	}
	files, err := b.files(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	for _, rel := range files {
		if err := b.deleteBlob(ctx, blobName(id, rel)); err != nil {
			return err
		}
	}
	return nil
}

func (b *Blob) deleteBlob(ctx context.Context, name string) error {
	_, err := b.client.DeleteBlob(ctx, b.container, name, nil)
	if err != nil && !bloberror.HasCode(err, bloberror.BlobNotFound) {
		return fmt.Errorf("delete %s: %w", name, err)
	}
	return nil
}

// each runs one transfer per file, several at a time, and stops at the first
// failure.
//
// The manifest is deliberately serialised last: walkFiles puts it at the end,
// and holding it back until every other file has succeeded is what makes an
// interrupted transfer invisible rather than half-visible.
func (b *Blob) each(ctx context.Context, files []string, progress func(string, int64),
	transfer func(context.Context, string) (int64, error)) error {

	body, last := files, ""
	if n := len(files); n > 0 && files[n-1] == snapshot.ManifestName {
		body, last = files[:n-1], files[n-1]
	}

	if err := b.parallel(ctx, body, progress, transfer); err != nil {
		return err
	}
	if last == "" {
		return nil
	}
	n, err := transfer(ctx, last)
	if err != nil {
		return err
	}
	if progress != nil {
		progress(last, n)
	}
	return nil
}

func (b *Blob) parallel(ctx context.Context, files []string, progress func(string, int64),
	transfer func(context.Context, string) (int64, error)) error {

	type result struct {
		file  string
		bytes int64
		err   error
	}
	sem := make(chan struct{}, b.concurrency)
	results := make(chan result, len(files))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, rel := range files {
		sem <- struct{}{}
		go func(rel string) {
			defer func() { <-sem }()
			n, err := transfer(ctx, rel)
			results <- result{file: rel, bytes: n, err: err}
		}(rel)
	}

	var firstErr error
	for range files {
		r := <-results
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
				// Stop the rest: a partial upload is expected on failure, and
				// finishing the remaining files would only make it slower to
				// find out.
				cancel()
			}
			continue
		}
		if progress != nil {
			progress(r.file, r.bytes)
		}
	}
	return firstErr
}

// EnsureContainer creates the container if it does not exist.
//
// Called before an upload rather than assumed: the first nightly of a new
// project should not fail on a container nobody created, and creating one that
// exists is not an error worth reporting.
func (b *Blob) EnsureContainer(ctx context.Context) error {
	_, err := b.client.CreateContainer(ctx, b.container, nil)
	if err != nil && !bloberror.HasCode(err, bloberror.ContainerAlreadyExists) {
		return fmt.Errorf("create container %s: %w", b.container, err)
	}
	return nil
}
