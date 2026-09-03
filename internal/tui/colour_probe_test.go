package tui

import (
	"strings"
	"testing"

	"github.com/richarddavenport/tuikit/comp"
)

// TestWhichTabsStillBuildStrings records what the canvas port has not finished.
//
// An instrument, not an assertion. It exists because nothing else can see this:
// the goldens are colour-stripped, and TestColourDoesNotChangeTheShape only asks
// whether colour changes the SHAPE. A detail pane drawn entirely in the
// terminal's foreground passes both, which is how it went unnoticed for a
// commit.
//
// It asks the model rather than counting escape sequences in the frame — the
// panel column beside the pane is full of colour, so a frame-wide count says
// everything is fine while the pane is grey. A tab that returns lines is a tab
// still building a styled string, which ansi.Strip flattens on the way into the
// canvas; one that returns a comp.Detail has been converted.
//
// Run with -v. When it reports nothing, delete it and lineContent's ansi.Strip
// with it.
func TestWhichTabsStillBuildStrings(t *testing.T) {
	panels := []struct {
		panel int
		name  string
		tabs  []string
	}{
		{panelConnections, "connections", []string{"Overview", "Databases", "Config"}},
		{panelDatabases, "databases", []string{"Tables", "Rules", "Foreign keys"}},
		{panelSnapshots, "snapshots", []string{"Manifest", "Tables", "Warnings", "Drift"}},
		{panelSets, "sets", []string{"Members", "Closure", "Load order"}},
		{panelRuns, "runs", []string{"Log"}},
	}

	var detail, table, flat []string
	for _, p := range panels {
		for i, tab := range p.tabs {
			m := fixtureModel(t)
			fixtureSnapshot(t, m)
			m.focus, m.paneFocus = p.panel, true
			m.tabs[p.panel] = i

			label := p.name + "/" + tab
			content := m.paneBody(i, 76)
			switch {
			case content.detail != nil:
				detail = append(detail, label)
			case styled(content.lines):
				// A comp.Table tab. It returns lines by design, because 213
				// rows of a manifest need a viewport rather than a layout, so
				// this is converted and not pending.
				table = append(table, label)
			default:
				flat = append(flat, label)
			}
		}
	}

	t.Logf("comp.Detail (%d): %s", len(detail), strings.Join(detail, ", "))
	t.Logf("comp.Table  (%d): %s", len(table), strings.Join(table, ", "))
	if len(flat) == 0 {
		t.Log("every tab is a component — delete this probe and lineContent's ansi.Strip")
		return
	}
	t.Logf("still a styled string, so drawn in one colour (%d): %s",
		len(flat), strings.Join(flat, ", "))
	t.Log("note: a comp.Table tab reached through mixedContent still has a flat " +
		"prose prefix above its table — see viewDatabaseTab's Tables branch")
}

// styled reports whether any row carries a style of its own, which is what a
// table built through tableRows has and a stripped string does not.
func styled(rows []comp.Row) bool {
	for _, r := range rows {
		if r.Style != nil {
			return true
		}
	}
	return false
}
