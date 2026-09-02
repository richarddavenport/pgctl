package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/pg"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// A fixed world, so a captured frame says the same thing tomorrow.
//
// pgctl already had fixtures — app_test.go's model() and withSnapshot() — but
// they are built on time.Now(), which is right for an assertion about
// behaviour and fatal for a golden: "14h ago" becomes "15h ago" while you are
// at lunch. Everything here hangs off epoch, and the model's clock is pinned to
// it, so the only thing that can change a frame is a change to the interface.
//
// The one thing a fixture cannot do is catch what the author did not think of.
// pgctl's own header-overflow bug was invisible in its first capture because
// the fixture shortened the config path to "pgctl.yaml", and only a real
// temp-dir path was long enough to overflow — which is why the live capture in
// screenshot_probe_test.go is kept rather than replaced.
var epoch = time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC)

// errUnreachable is what a probe that could not connect carries. The real one
// comes out of libpq and names a host and a port; the frames only need it to be
// non-nil and to read like a connection failure.
var errUnreachable = errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")

// fixtureModel is the model with a world already in it, at a fixed instant.
func fixtureModel(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Parse([]byte(uiConfig), dir)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	// A short, stable path. The real one is a temp directory whose name changes
	// every run, and a golden cannot hold that — the live capture is what
	// renders a path long enough to overflow the header.
	cfg.Source = "pgctl.yaml"
	cfg.Storage.Dir = filepath.Join(dir, "snapshots")

	m := New(engine.New(cfg))
	m.Now(epoch)
	m.SetSize(132, 38)

	m.probes["prd"] = &engine.Probe{
		Connection: "prd", Reachable: true, ServerVersion: 170004,
		Host: "prd.example", Port: 5432, User: "mbpiadmin",
		ProbedAt: epoch.Add(-30 * time.Second),
		Databases: []engine.DatabaseInfo{
			{Name: "product-development", Bytes: 11 << 30},
			{Name: "claims", Bytes: 48 << 20},
			{Name: "quote", Bytes: 20 << 30},
		},
	}
	m.probes["qat"] = &engine.Probe{
		Connection: "qat", Reachable: true, ServerVersion: 170004,
		Host: "qat.example", Port: 5432, User: "mbpiadmin",
		ProbedAt:  epoch.Add(-time.Minute),
		Databases: []engine.DatabaseInfo{{Name: "product-development", Bytes: 9 << 30}},
	}
	// One unreachable environment, because the marker in the Connections panel
	// answers "can I reach it" before the name answers "which is it", and a
	// capture where every row is reachable never shows that.
	m.probes["scratch"] = &engine.Probe{Connection: "scratch", Err: errUnreachable}

	m.setInfo[setKey("prd", "product-development", "claims")] = &setSummary{
		members: []string{"claims.policy_claim", "claims.claim_detail"},
		added:   []string{"operations.policy_contract", "public.address"},
	}
	m.liveTable["prd/product-development"] = []pg.TableInfo{
		{Name: "claims.policy_claim", Bytes: 180 << 20, EstimatedRows: 483187},
		{Name: "quotes.quote", Bytes: 20 << 30, EstimatedRows: 146597},
		{Name: "audit.logged_actions", Bytes: 4 << 30, EstimatedRows: 9_100_000},
	}
	return m
}

// fixtureSnapshot writes a manifest to the model's storage and loads it, at
// fixed times.
func fixtureSnapshot(t *testing.T, m *Model) *snapshot.Manifest {
	t.Helper()
	id := "prd/product-development/20260828T030000Z"
	dir := snapshot.Path(m.cfg.Storage.Dir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	man := &snapshot.Manifest{
		ID:          id,
		Connection:  "prd",
		Database:    "product-development",
		StartedAt:   epoch.Add(-14 * time.Hour),
		FinishedAt:  epoch.Add(-14*time.Hour + 202*time.Second),
		Bytes:       2_040_893_635,
		Compression: "zstd:3",
		Jobs:        4,
		Tables: []snapshot.TableEntry{
			{Name: "claims.policy_claim", Data: config.DataAll, SourceBytes: 180 << 20, SourceRows: 483187},
			{Name: "quotes.quote", Data: config.DataFiltered, SourceBytes: 20 << 30, SourceRows: 146597, Rows: 4200},
			{Name: "audit.logged_actions", Data: config.DataNone},
		},
		Warnings: []string{"rule \"operations.gone\" matches no table in this database"},
	}
	if err := snapshot.Write(dir, man); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	m.entries = []*engine.Entry{{Manifest: man, Local: true}}
	m.clampCursors()
	return man
}
