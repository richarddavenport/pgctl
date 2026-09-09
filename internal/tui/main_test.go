package tui

import (
	"os"
	"testing"
	"time"
)

// TestMain pins the test process to UTC.
//
// The screens show a snapshot's instant in LOCAL time, which is right for a
// person reading it — `.Local()` in rows.go, detail.go and formview.go — and
// wrong for a golden file. A fixture snapshot taken at a fixed UTC instant
// renders as `08-27 22:00` on the author's machine and `08-28 03:00` on a CI
// runner, so 21 of the 52 goldens failed on the runner and passed everywhere
// they were written. A golden that only holds on one machine is worse than no
// golden: it fails for whoever did not write it, and it never fails for whoever
// did.
//
// So the goldens are UTC by construction, and the local-time rendering is
// asserted on its own where the timezone is the subject rather than a
// background condition.
// Version is pinned for the same reason as the timezone: it is drawn in the
// corner of every frame, and "dev" is what a golden captured from a checkout
// says while a release binary says something else. Pinned to a release, so the
// local-build case is a deviation a test asks for rather than the default.
func TestMain(m *testing.M) {
	time.Local = time.UTC
	Version = "v0.3.0"
	os.Exit(m.Run())
}

// A snapshot row shows the instant in the reader's own timezone.
//
// The subject of this test is the thing TestMain switches off, so it says so
// itself rather than depending on the process default: a snapshot taken at
// 03:00 UTC was taken at 22:00 the previous day for somebody in Chicago, and a
// row that showed them 03:00 would be answering a question nobody asked. The
// goldens are UTC because a golden cannot be in two zones at once; the product
// is not.
func TestASnapshotRowIsInTheReadersTimezone(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skipf("no zone database: %v", err)
	}
	defer func(was *time.Location) { time.Local = was }(time.Local)

	m := fixtureModel(t)
	fixtureSnapshot(t, m)

	read := func() string {
		t.Helper()
		rows := m.snapshotRows(panelSnapshots)
		for _, row := range rows {
			if !row.Skip {
				return row.Text
			}
		}
		t.Fatal("no snapshot row")
		return ""
	}

	time.Local = time.UTC
	if got := read(); got != "08-28 03:00" {
		t.Errorf("in UTC the row reads %q, want the instant it was taken", got)
	}
	time.Local = chicago
	if got := read(); got != "08-27 22:00" {
		t.Errorf("in Chicago the row reads %q, want 22:00 the previous day", got)
	}
}
