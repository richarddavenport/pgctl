package engine

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// entryAt is one member of a run: a connection, a database, an instant, and
// where it is.
func entryAt(connection, database string, at time.Time, finished bool, where ...string) *Entry {
	man := &snapshot.Manifest{
		ID:         snapshot.NewID(connection, database, at),
		Connection: connection,
		Database:   database,
		StartedAt:  at,
		Bytes:      1 << 20,
	}
	if finished {
		man.FinishedAt = at.Add(time.Minute)
	}
	if len(where) == 0 {
		where = []string{config.LocalStorage}
	}
	return &Entry{Manifest: man, At: where}
}

// A run is every database snapshotted at one instant from one connection.
//
// It is derived from the shared timestamp rather than stored, which is what
// makes it free: the ids, the manifests and the storage layout are untouched.
func TestARunGroupsTheDatabasesOfOneSnapshot(t *testing.T) {
	at := time.Date(2026, 9, 9, 15, 30, 0, 0, time.UTC)
	earlier := at.Add(-time.Hour)

	runs := groupRuns([]*Entry{
		entryAt("prd", "claims", earlier, true),
		entryAt("prd", "product-development", at, true),
		entryAt("prd", "claims", at, true),
		entryAt("prd", "hasura", at, true),
		entryAt("latest", "product-development", at, true),
	})

	if len(runs) != 3 {
		var ids []string
		for _, r := range runs {
			ids = append(ids, r.ID)
		}
		t.Fatalf("%d runs (%v), want three: prd's two and latest's one", len(runs), ids)
	}

	// Oldest first, like the index.
	if runs[0].ID != "prd/20260909T143000Z" {
		t.Errorf("the first run is %q, want the earlier one", runs[0].ID)
	}

	// The three-database run, with its members in database order.
	var run *Run
	for _, r := range runs {
		if r.ID == "prd/20260909T153000Z" {
			run = r
		}
	}
	if run == nil {
		t.Fatal("no run for prd at 15:30")
	}
	if got := strings.Join(run.Databases(), ","); got != "claims,hasura,product-development" {
		t.Errorf("databases = %s, want them in name order", got)
	}
	if run.Bytes() != 3<<20 {
		t.Errorf("bytes = %d, want the members' total", run.Bytes())
	}
	// A run id has two segments and a member id three, so they cannot collide.
	if strings.Count(run.ID, "/") != 1 {
		t.Errorf("run id %q is not <connection>/<timestamp>", run.ID)
	}
}

// An unfinished member makes the RUN incomplete.
//
// Because a run is what gets restored: five good databases and one truncated
// dump is not a state of the estate anybody wants, and the alternative — call
// it complete, refuse that member at apply time — is a refusal an hour into the
// work.
func TestARunIsCompleteOnlyIfEveryMemberIs(t *testing.T) {
	at := time.Date(2026, 9, 9, 15, 30, 0, 0, time.UTC)
	whole := groupRuns([]*Entry{
		entryAt("prd", "claims", at, true),
		entryAt("prd", "hasura", at, true),
	})[0]
	if !whole.Complete() {
		t.Error("a run of two finished members is not complete")
	}

	partial := groupRuns([]*Entry{
		entryAt("prd", "claims", at, true),
		entryAt("prd", "hasura", at, false),
	})[0]
	if partial.Complete() {
		t.Error("a run with an unfinished member calls itself complete")
	}
}

// Where a run IS is the intersection of its members, not the union.
//
// A union would report `local+snapshots` for a run whose product-development
// member never uploaded — and that is the member you would need.
func TestARunIsSomewhereOnlyIfAllOfItIs(t *testing.T) {
	at := time.Date(2026, 9, 9, 15, 30, 0, 0, time.UTC)
	run := groupRuns([]*Entry{
		entryAt("prd", "claims", at, true, config.LocalStorage, "snapshots"),
		entryAt("prd", "product-development", at, true, config.LocalStorage),
	})[0]

	if got := strings.Join(run.Locations(), "+"); got != config.LocalStorage {
		t.Errorf("locations = %q, want local alone — one member never uploaded", got)
	}
	if !run.Local() {
		t.Error("every member is local and the run says it is not")
	}

	remoteOnly := groupRuns([]*Entry{
		entryAt("prd", "claims", at, true, "snapshots"),
		entryAt("prd", "hasura", at, true, "snapshots"),
	})[0]
	if remoteOnly.Local() {
		t.Error("a run in a remote only says it is local")
	}
}

// `<connection>/latest` is the newest COMPLETE run.
//
// It used to be the newest single-database snapshot of the connection, so
// `pgctl apply prd/latest --to qat` restored one database — whichever sorted
// last. That is the command a nightly restore would use.
func TestLatestIsTheNewestCompleteRun(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Parse(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Source = dir + "/pgctl.yaml"
	e := New(cfg)

	at := time.Date(2026, 9, 9, 15, 30, 0, 0, time.UTC)
	older := at.Add(-time.Hour)
	writeRun(t, e, older, true, "claims", "hasura")
	writeRun(t, e, at, false, "claims", "hasura") // the newest, and it broke

	run, err := e.OpenRun(t.Context(), "prd/latest", nil)
	if err != nil {
		t.Fatalf("prd/latest: %v", err)
	}
	if !run.At.Equal(older) {
		t.Errorf("prd/latest is the run at %s, want the older COMPLETE one at %s",
			run.At.Format(time.TimeOnly), older.Format(time.TimeOnly))
	}
	if len(run.Members) != 2 {
		t.Errorf("prd/latest covers %d databases, want both", len(run.Members))
	}
}

// A single-database id still names exactly that database, as a run of one.
//
// Every script that names a full id keeps working, and the caller does not
// branch: an apply of one database and an apply of six differ in how many plans
// they produce, not in kind.
func TestAMemberIdIsARunOfOne(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Parse(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Source = dir + "/pgctl.yaml"
	e := New(cfg)

	at := time.Date(2026, 9, 9, 15, 30, 0, 0, time.UTC)
	writeRun(t, e, at, true, "claims", "hasura", "product-development")

	run, err := e.OpenRun(t.Context(), "prd/hasura/20260909T153000Z", nil)
	if err != nil {
		t.Fatalf("member id: %v", err)
	}
	if got := run.Databases(); len(got) != 1 || got[0] != "hasura" {
		t.Errorf("databases = %v, want hasura alone", got)
	}
	// It still belongs to its run, so a screen can say which one it came from.
	if run.ID != "prd/20260909T153000Z" {
		t.Errorf("run id = %q, want the run the member belongs to", run.ID)
	}

	// And the run id itself covers all three.
	whole, err := e.OpenRun(t.Context(), "prd/20260909T153000Z", nil)
	if err != nil {
		t.Fatalf("run id: %v", err)
	}
	if len(whole.Members) != 3 {
		t.Errorf("the run covers %d databases, want three", len(whole.Members))
	}
}

// A database the run does not have is an error that says what it does have.
func TestNamingADatabaseTheRunDoesNotHave(t *testing.T) {
	at := time.Date(2026, 9, 9, 15, 30, 0, 0, time.UTC)
	run := groupRuns([]*Entry{
		entryAt("prd", "claims", at, true),
		entryAt("prd", "hasura", at, true),
	})[0]

	_, err := selectMembers(run, []string{"claims", "quote"})
	if err == nil {
		t.Fatal("selecting a database the run does not have was accepted")
	}
	for _, want := range []string{"quote", "claims, hasura"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, does not mention %q", err, want)
		}
	}
}

// writeRun puts a run's manifests on disk, which is what Index reads.
func writeRun(t *testing.T, e *Engine, at time.Time, finished bool, databases ...string) {
	t.Helper()
	for _, database := range databases {
		id := snapshot.NewID("prd", database, at)
		dir := snapshot.Path(e.StorageDir(), id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		man := &snapshot.Manifest{
			ID: id, Connection: "prd", Database: database,
			StartedAt: at, Bytes: 1 << 20,
		}
		if finished {
			man.FinishedAt = at.Add(time.Minute)
		}
		if err := snapshot.Write(dir, man); err != nil {
			t.Fatal(err)
		}
	}
}
