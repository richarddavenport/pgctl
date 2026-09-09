package tui

import (
	"testing"
	"time"

	"github.com/richarddavenport/tuikit/comp"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// panelBands is a constraint solve, and these are the three rules it has to
// keep. The goldens did not move when the arithmetic became constraints, which
// proves it agrees on ONE set of panel lengths at two sizes — not that it
// agrees under squeeze, which is the case the old code had twenty lines for.
func TestPanelBandsKeepsItsThreeRules(t *testing.T) {
	m := fixtureModel(t)
	fixtureSnapshot(t, m)

	// Two borders, and whatever the list spends on itself — nothing, with
	// NoStatus set. Asked for rather than assumed; see panelBands.
	chrome := 2 + m.lists[panelConnections].StatusRows()
	minBand := 1 + chrome
	for _, rows := range []int{4, 20, 21, 24, 30, 40, 60, 120} {
		got := m.panelBands(comp.Rect{W: 1, H: rows})

		sum := 0
		for panel, band := range got {
			h := band.H
			sum += h
			want := max(m.panelLen(panel), 1) + chrome

			// Never stretched past what it has to show, so slack goes to the
			// bottom of the column instead of into a panel with nothing in it.
			if h > want {
				t.Errorf("rows=%d panel %d got %d rows, wants only %d", rows, panel, h, want)
			}
			// A title and one row, always — unless there is not even room for
			// every panel's minimum, in which case the overflow is the frame's
			// to clip and every panel still asks for its minimum.
			if h < minBand && rows >= minBand*panelCount {
				t.Errorf("rows=%d panel %d got %d rows, below the minimum of %d",
					rows, panel, h, minBand)
			}
		}

		// Never more than it was given, once there is room for the minimums.
		if rows >= minBand*panelCount && sum > rows {
			t.Errorf("rows=%d shared out %d rows, %d too many", rows, sum, sum-rows)
		}
	}
}

// A panel with more to show than the column has room for gets a bigger share
// than one with less — the proportional rule, which is the whole reason this is
// not an even split.
func TestASqueezedColumnSharesInProportion(t *testing.T) {
	m := fixtureModel(t)
	// Snapshots asks for far more than the others: 40 rows against 3 and 1.
	//
	// Forty distinct RUNS, each at its own instant. Forty copies of one entry
	// is what this used to build, and since a snapshot became a run they all
	// grouped into one row — the panel asked for 1 and the test still claimed
	// it asked for 40.
	man := fixtureSnapshot(t, m)
	entries := m.entries
	for i := 1; i < 40; i++ {
		another := *man
		another.StartedAt = man.StartedAt.Add(-time.Duration(i) * time.Hour)
		another.ID = snapshot.NewID(man.Connection, man.Database, another.StartedAt)
		entries = append(entries, &engine.Entry{
			Manifest: &another, At: []string{config.LocalStorage},
		})
	}
	m.setEntries(entries)

	got := m.panelBands(comp.Rect{W: 1, H: 30})
	if got[panelSnapshots].H <= got[panelSets].H {
		t.Errorf("snapshots wants %d rows and got %d; sets wants %d and got %d — "+
			"the squeeze is not proportional",
			m.panelLen(panelSnapshots), got[panelSnapshots].H,
			m.panelLen(panelSets), got[panelSets].H)
	}
}
