package tui

import "testing"

// panelHeights is a constraint solve now, and these are the three rules it has
// to keep. The goldens did not move when it changed, which proves it agrees
// with the old arithmetic on ONE set of panel lengths at two sizes — not that
// it agrees under squeeze, which is the case the old code had twenty lines for.
func TestPanelHeightsKeepsItsThreeRules(t *testing.T) {
	m := fixtureModel(t)
	fixtureSnapshot(t, m)

	const minRows = 2
	for _, rows := range []int{4, 10, 11, 14, 15, 20, 26, 60} {
		got := m.panelHeights(rows)

		sum := 0
		for panel, h := range got {
			sum += h
			want := max(m.panelLen(panel)+1, minRows)

			// Never stretched past what it has to show, so slack goes to the
			// bottom of the column instead of into a panel with nothing in it.
			if h > want {
				t.Errorf("rows=%d panel %d got %d rows, wants only %d", rows, panel, h, want)
			}
			// A title and one row, always — unless there is not even room for
			// every panel's minimum, in which case the overflow is the frame's
			// to clip and every panel still asks for its minimum.
			if h < minRows && rows >= minRows*panelCount {
				t.Errorf("rows=%d panel %d got %d rows, below the minimum of %d",
					rows, panel, h, minRows)
			}
		}

		// Never more than it was given, once there is room for the minimums.
		if rows >= minRows*panelCount && sum > rows {
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
	fixtureSnapshot(t, m)
	for len(m.entries) < 40 {
		m.entries = append(m.entries, m.entries[0])
	}

	got := m.panelHeights(14)
	if got[panelSnapshots] <= got[panelSets] {
		t.Errorf("snapshots wants %d rows and got %d; sets wants %d and got %d — "+
			"the squeeze is not proportional",
			m.panelLen(panelSnapshots), got[panelSnapshots],
			m.panelLen(panelSets), got[panelSets])
	}
}
