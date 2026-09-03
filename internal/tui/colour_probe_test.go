package tui

import (
	"strings"
	"testing"
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

	var pending, done []string
	for _, p := range panels {
		for i, tab := range p.tabs {
			m := fixtureModel(t)
			fixtureSnapshot(t, m)
			m.focus, m.paneFocus = p.panel, true
			m.tabs[p.panel] = i

			label := p.name + "/" + tab
			if m.paneBody(i, 76).detail != nil {
				done = append(done, label)
				continue
			}
			pending = append(pending, label)
		}
	}

	t.Logf("converted (%d): %s", len(done), strings.Join(done, ", "))
	if len(pending) == 0 {
		t.Log("every tab draws in colour — delete this probe and lineContent's ansi.Strip")
		return
	}
	t.Logf("still a styled string, so drawn in one colour (%d): %s",
		len(pending), strings.Join(pending, ", "))
}
