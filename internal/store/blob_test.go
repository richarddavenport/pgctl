package store

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// Azurite's well-known development credentials. Not a secret: they are
// published in Microsoft's documentation and only ever reach a local emulator.
const (
	azuriteAccount = "devstoreaccount1"
	azuriteKey     = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
)

// TestBlobRoundTrip exercises the blob store against Azurite.
//
//	docker run -d --name azurite -p 10000:10000 \
//	  mcr.microsoft.com/azure-storage/azurite azurite-blob --blobHost 0.0.0.0
//	PGCTL_TEST_BLOB_ENDPOINT=http://127.0.0.1:10000/devstoreaccount1 go test ./internal/store/
func TestBlobRoundTrip(t *testing.T) {
	endpoint := os.Getenv("PGCTL_TEST_BLOB_ENDPOINT")
	if endpoint == "" {
		t.Skip("set PGCTL_TEST_BLOB_ENDPOINT to run against a blob emulator")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	container := "pgctl-test-" + time.Now().UTC().Format("20060102150405")
	blob, err := NewBlob(azuriteAccount, azuriteKey, container, endpoint)
	if err != nil {
		t.Fatalf("NewBlob: %v", err)
	}

	id := "prd/product-development/20260828T030000Z"
	local := t.TempDir()
	dir := snapshot.Path(local, id)
	writeFakeSnapshot(t, dir, id)

	if err := blob.Put(ctx, id, dir, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Listing walks the prefix hierarchy rather than every blob.
	ids, err := blob.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{id}) {
		t.Errorf("List = %v, want [%s]", ids, id)
	}

	// The manifest must be readable without downloading the snapshot: that is
	// what makes planning against a remote snapshot cheap.
	man, err := blob.Manifest(ctx, id)
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if man.ID != id || len(man.Tables) != 2 {
		t.Errorf("manifest came back as %+v", man)
	}

	// A selective download takes what it asks for and nothing else — the
	// property that makes moving one table out of a 2 GB nightly cheap.
	partial := t.TempDir()
	want := []string{"dump/toc.dat", "dump/17.dat.zst", snapshot.ManifestName}
	if err := blob.Get(ctx, id, partial, want, nil); err != nil {
		t.Fatalf("selective Get: %v", err)
	}
	for _, rel := range want {
		if _, err := os.Stat(filepath.Join(partial, filepath.FromSlash(rel))); err != nil {
			t.Errorf("selective Get did not fetch %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(partial, "dump", "18.dat.zst")); err == nil {
		t.Error("selective Get fetched a file that was not asked for")
	}

	// A full download restores the whole tree.
	full := t.TempDir()
	if err := blob.Get(ctx, id, full, nil, nil); err != nil {
		t.Fatalf("full Get: %v", err)
	}
	back, err := snapshot.Read(full)
	if err != nil {
		t.Fatalf("read downloaded manifest: %v", err)
	}
	if back.ID != id {
		t.Errorf("downloaded manifest id = %q", back.ID)
	}
	for _, rel := range []string{"dump/toc.dat", "dump/17.dat.zst", "dump/18.dat.zst", "filtered/quotes.quote.bin.zst"} {
		if _, err := os.Stat(filepath.Join(full, filepath.FromSlash(rel))); err != nil {
			t.Errorf("full Get missing %s: %v", rel, err)
		}
	}

	if err := blob.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ids, err := blob.List(ctx); err != nil {
		t.Fatalf("List after Delete: %v", err)
	} else if len(ids) != 0 {
		t.Errorf("List after Delete = %v, want empty", ids)
	}
	if _, err := blob.Manifest(ctx, id); err == nil {
		t.Error("the manifest of a deleted snapshot still reads")
	}
}

func writeFakeSnapshot(t *testing.T, dir, id string) {
	t.Helper()
	for rel, body := range map[string]string{
		"dump/toc.dat":                  "table of contents",
		"dump/17.dat.zst":               "claims data",
		"dump/18.dat.zst":               "contract data",
		"filtered/quotes.quote.bin.zst": "recent quotes",
	} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	man := &snapshot.Manifest{
		ID:         id,
		Connection: "prd",
		Database:   "product-development",
		StartedAt:  time.Date(2026, 8, 28, 3, 0, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 8, 28, 3, 12, 0, 0, time.UTC),
		Tables: []snapshot.TableEntry{
			{Name: "claims.policy_claim"},
			{Name: "operations.policy_contract"},
		},
	}
	if err := snapshot.Write(dir, man); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}
