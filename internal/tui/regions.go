package tui

import "github.com/richarddavenport/tuikit/comp"

// Every region pgctl draws, named once.
//
// A capture script addresses these by name — `click snapshots.row[2]` rather
// than a coordinate — and comp.Canvas reports whether one was drawn at all, so
// a script that has drifted from the interface is told which region it can no
// longer find. An agent naming a region beats an agent guessing a coordinate.
//
// The five panel regions are separate names rather than one indexed name,
// because a comp.List indexes its own rows: `connections.row[1]` is the second
// CONNECTION, not the second line of the screen. After a scroll those differ,
// and an ID meaning "row 1 of the panel" acts on whatever moved into it.
const (
	regHeader comp.Name = "header"
	regFooter comp.Name = "footer"
	regSplit  comp.Name = "split"

	regConnections comp.Name = "connections"
	regDatabases   comp.Name = "databases"
	regSnapshots   comp.Name = "snapshots"
	regSets        comp.Name = "sets"
	regRuns        comp.Name = "runs"

	// A panel's rows are a region of their own, so a script says
	// `click snapshots.row[2]` and the panel's frame stays addressable as
	// `snapshots`. The list indexes these by position in the LIST, so after a
	// scroll row[2] is still the third snapshot and not the third line.
	regConnectionsRow comp.Name = "connections.row"
	regDatabasesRow   comp.Name = "databases.row"
	regSnapshotsRow   comp.Name = "snapshots.row"
	regSetsRow        comp.Name = "sets.row"
	regRunsRow        comp.Name = "runs.row"

	regPane comp.Name = "pane"
	regTabs comp.Name = "pane.tabs"
	regBody comp.Name = "pane.body"

	// regModal and regHelp are the overlays. They are regions so that a script
	// can assert a modal is open rather than inferring it from the text that
	// happens to be on screen.
	regModal comp.Name = "modal"
	regHelp  comp.Name = "help"
)

// panelRegions maps a panel to its region, in the same order as the panel
// constants — so the region a panel draws under cannot drift from the panel,
// which a switch statement would allow.
var panelRegions = [panelCount]comp.Name{
	panelConnections: regConnections,
	panelDatabases:   regDatabases,
	panelSnapshots:   regSnapshots,
	panelSets:        regSets,
	panelRuns:        regRuns,
}

// panelRowRegions is the same, for the rows inside each panel.
var panelRowRegions = [panelCount]comp.Name{
	panelConnections: regConnectionsRow,
	panelDatabases:   regDatabasesRow,
	panelSnapshots:   regSnapshotsRow,
	panelSets:        regSetsRow,
	panelRuns:        regRunsRow,
}
