package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/tuikit/comp"
	"github.com/richarddavenport/tuikit/harness"
)

// A click lands on the thing that drew the row, not on the row.
//
// The distinction is the whole reason the rows carry regions: after a scroll,
// "row 2 of the screen" and "item 2 of the list" are different items, and a
// mouse wired to the screen acts on whatever moved into that line.
func TestAClickSelectsTheRowItLandedOn(t *testing.T) {
	m := fixtureModel(t)
	fixtureSnapshot(t, m)
	r := run(m, 132, 38)
	_ = r.View()

	// Databases is panel 2, and its second row is claims.
	harness.Click(t, r, "databases.row[1]")
	if m.focus != panelDatabases {
		t.Errorf("clicking a database row left focus on panel %d", m.focus)
	}
	if got := m.cursor(panelDatabases); got != 1 {
		t.Errorf("cursor is on row %d, want 1", got)
	}
	db, ok := m.selectedDatabase()
	if !ok || db.Name != "claims" {
		t.Errorf("selected %q, want claims", db.Name)
	}
}

// Clicking a tab selects that tab, not the next one.
func TestAClickOnATabSelectsIt(t *testing.T) {
	m := fixtureModel(t)
	fixtureSnapshot(t, m)
	r := run(m, 132, 38)
	harness.Press(r, "3") // the Snapshots panel: Manifest · Tables · Warnings · Drift

	harness.Click(t, r, "pane.tabs[2]")
	if got := m.tabs[panelSnapshots]; got != 2 {
		t.Errorf("tab %d selected, want 2 (Warnings)", got)
	}
	if !m.paneFocus {
		t.Error("clicking a tab did not focus the pane")
	}
}

// The wheel scrolls what the pointer is over and never moves the selection.
func TestTheWheelScrollsWithoutSelecting(t *testing.T) {
	m := fixtureModel(t)
	fixtureSnapshot(t, m)
	r := run(m, 132, 38)
	_ = r.View()

	before := m.cursor(panelConnections)
	harness.Wheel(t, r, "connections.row[0]", 2)
	if got := m.cursor(panelConnections); got != before {
		t.Errorf("the wheel moved the cursor from %d to %d", before, got)
	}
}

// A modal takes the mouse as well as the keyboard. The panel behind a
// confirmation is exactly what the confirmation is about, which is what makes
// clicking it tempting and wrong.
func TestAModalBlocksTheMouse(t *testing.T) {
	m := fixtureModel(t)
	fixtureSnapshot(t, m)
	r := run(m, 132, 38)
	harness.Press(r, "3", "a")
	if m.action == nil {
		t.Fatal("a did not open the apply form")
	}

	before := m.cursor(panelConnections)
	// Addressed by coordinate rather than by name, because the modal is drawn
	// over the panel and the region is no longer there to click.
	m.onMouse(tea.MouseMsg{X: 1, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if got := m.cursor(panelConnections); got != before {
		t.Errorf("a click behind the modal moved the cursor from %d to %d", before, got)
	}
}

// Every region a capture script or a click can name was actually drawn.
func TestTheNamedRegionsAreDrawn(t *testing.T) {
	m := fixtureModel(t)
	fixtureSnapshot(t, m)
	r := run(m, 132, 38)
	_ = r.View()

	for _, name := range []comp.Name{
		regHeader, regFooter, regSplit, regPane, regTabs, regBody,
		regConnections, regDatabases, regSnapshots, regSets, regRuns,
	} {
		if _, ok := r.Canvas().Region(comp.Region(name)); !ok {
			t.Errorf("region %q is named but never drawn", name)
		}
	}

	// A list's rows are only ever INDEXED — connections.row[0] — because the
	// index is the row's place in the list rather than on the screen. The
	// un-indexed name used to be drawn too, by the status row; NoStatus
	// removed it, and nothing should be addressing it.
	for _, name := range []comp.Name{
		regConnectionsRow, regDatabasesRow, regSnapshotsRow, regSetsRow,
	} {
		if _, ok := r.Canvas().Region(comp.Region(name).At(0)); !ok {
			t.Errorf("region %q[0] is named but never drawn", name)
		}
	}
}
