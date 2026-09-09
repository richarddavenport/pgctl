package tui

import (
	"strings"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
)

// The panels are a hierarchy: the selected environment decides which databases
// are listed, and the two together decide which snapshots and sets are. These
// accessors are the single definition of that, so a renderer and a key handler
// can never disagree about what is selected.

// environments returns the environments, in config order.
func (m *Model) connections() []config.Connection {
	return filtered(m.cfg.All(), m.focus == panelConnections, m.filter.Text,
		func(e config.Connection) string { return e.Name })
}

// selectedConn is the focused connection.
func (m *Model) selectedConn() (config.Connection, bool) {
	list := m.connections()
	if i := m.cursor(panelConnections); i >= 0 && i < len(list) {
		return list[i], true
	}
	return config.Connection{}, false
}

// databases returns the selected connection's databases, as the server reports
// them. There is no fallback list: the config no longer claims to know what
// databases exist, so an unreachable server has nothing to show rather than
// something that might be wrong.
func (m *Model) databases() []engine.DatabaseInfo {
	conn, ok := m.selectedConn()
	if !ok {
		return nil
	}
	p := m.probes[conn.Name]
	if p == nil || !p.Reachable {
		return nil
	}
	return filtered(p.Databases, m.focus == panelDatabases, m.filter.Text,
		func(d engine.DatabaseInfo) string { return d.Name })
}

func (m *Model) selectedDatabase() (engine.DatabaseInfo, bool) {
	list := m.databases()
	if i := m.cursor(panelDatabases); i >= 0 && i < len(list) {
		return list[i], true
	}
	return engine.DatabaseInfo{}, false
}

// snapshotRow is one row of a snapshot panel: a snapshot, or the heading above
// a group of them.
//
// One list of both, because comp.List's cursor is an index into the ROWS it was
// given — so a headings-and-snapshots panel whose selection reads from a
// separate slice is a panel where the cursor and the detail pane disagree by
// however many headings sit above it.
type snapshotRow struct {
	// heading is a source connection, on a row that is a label rather than a
	// thing. comp.Row.Skip keeps the cursor off it.
	heading string
	run     *engine.Run
}

// snapshots is what was taken FROM the selected connection, newest first.
//
// One row per snapshot, where a snapshot is a RUN — every database taken at one
// instant. It listed one row per database until somebody pointed out that
// choosing databases to take them and again to restore them is the same choice
// twice: the thing taken is the run, and `pgctl ls` and `apply` now agree.
func (m *Model) snapshots() []snapshotRow {
	conn, ok := m.selectedConn()
	if !ok {
		return nil
	}
	var out []snapshotRow
	for i := len(m.snaps) - 1; i >= 0; i-- {
		if m.snaps[i].Connection != conn.Name {
			continue
		}
		out = append(out, snapshotRow{run: m.snaps[i]})
	}
	return m.filterRuns(out, panelSnapshots)
}

// restorable is what can be put ON the selected connection: every OTHER
// connection's snapshots, grouped by where they came from.
//
// The panel exists because the hierarchy could not answer the question anybody
// actually has. Standing on qat, the Snapshots panel is empty and right —
// nothing is ever taken from qat — and a reader looking for what to restore
// there found nothing at all. Two panels, two questions.
//
// A protected connection is excluded as a SOURCE of nothing: prd's snapshots
// are exactly what you restore elsewhere. What is excluded is the selected
// connection itself, because applying a snapshot back to the environment it
// came from is a different act — the engine warns about it, and it is reachable
// from the Snapshots panel where it belongs.
func (m *Model) restorable() []snapshotRow {
	conn, ok := m.selectedConn()
	if !ok {
		return nil
	}

	bySource := map[string][]*engine.Run{}
	var sources []string
	for i := len(m.snaps) - 1; i >= 0; i-- {
		run := m.snaps[i]
		if run.Connection == conn.Name {
			continue
		}
		if _, seen := bySource[run.Connection]; !seen {
			sources = append(sources, run.Connection)
		}
		bySource[run.Connection] = append(bySource[run.Connection], run)
	}

	var out []snapshotRow
	for _, source := range sources {
		out = append(out, snapshotRow{heading: source})
		for _, run := range bySource[source] {
			out = append(out, snapshotRow{run: run})
		}
	}
	return m.filterRuns(out, panelRestorable)
}

// filterRuns applies the / filter to a snapshot panel, and drops a heading
// whose every row went with it.
func (m *Model) filterRuns(rows []snapshotRow, panel int) []snapshotRow {
	if m.focus != panel || m.filter.Text == "" {
		return rows
	}
	needle := strings.ToLower(m.filter.Text)

	var out []snapshotRow
	for i, row := range rows {
		if row.run != nil {
			if strings.Contains(strings.ToLower(row.run.ID), needle) ||
				strings.Contains(strings.ToLower(strings.Join(row.run.Databases(), " ")), needle) {
				out = append(out, row)
			}
			continue
		}
		// A heading survives only if something under it does.
		for _, under := range rows[i+1:] {
			if under.run == nil {
				break
			}
			if strings.Contains(strings.ToLower(under.run.ID), needle) ||
				strings.Contains(strings.ToLower(strings.Join(under.run.Databases(), " ")), needle) {
				out = append(out, row)
				break
			}
		}
	}
	return out
}

// snapshotCount is how many snapshots a panel is showing, which is not how many
// rows it has: a heading is a row and is not a snapshot.
func (m *Model) snapshotCount(panel int) int {
	n := 0
	for _, r := range m.panelSnapshotRows(panel) {
		if r.run != nil {
			n++
		}
	}
	return n
}

// panelSnapshotRows is whichever of the two snapshot panels is meant.
func (m *Model) panelSnapshotRows(panel int) []snapshotRow {
	if panel == panelRestorable {
		return m.restorable()
	}
	return m.snapshots()
}

// selectedSnapshot is the snapshot under the cursor of whichever snapshot panel
// has focus — or, when neither does, of the Snapshots panel.
//
// Which panel matters, because they hold different things: `a` on a Restorable
// row applies somebody else's snapshot to the connection you are standing on,
// and `a` on a Snapshots row applies this connection's own snapshot somewhere
// else. The form shows the target either way.
func (m *Model) selectedSnapshot() (*engine.Run, bool) {
	panel := m.focus
	if panel != panelSnapshots && panel != panelRestorable {
		panel = panelSnapshots
	}
	rows := m.panelSnapshotRows(panel)
	if i := m.cursor(panel); i >= 0 && i < len(rows) {
		return rows[i].run, rows[i].run != nil
	}
	return nil, false
}

// sets returns the sets declared for the selected database.
func (m *Model) sets() []config.Set {
	db, ok := m.selectedDatabase()
	var out []config.Set
	for _, s := range m.cfg.Sets {
		if ok && s.Database != db.Name {
			continue
		}
		out = append(out, s)
	}
	return filtered(out, m.focus == panelSets, m.filter.Text,
		func(s config.Set) string { return s.Name })
}

func (m *Model) selectedSet() (config.Set, bool) {
	list := m.sets()
	if i := m.cursor(panelSets); i >= 0 && i < len(list) {
		return list[i], true
	}
	return config.Set{}, false
}

// runList returns this session's operations, newest first.
func (m *Model) runList() []*runRecord {
	out := make([]*runRecord, 0, len(m.runs))
	for i := len(m.runs) - 1; i >= 0; i-- {
		out = append(out, m.runs[i])
	}
	return filtered(out, m.focus == panelRuns, m.filter.Text,
		func(r *runRecord) string { return r.kind })
}

func (m *Model) selectedRun() (*runRecord, bool) {
	list := m.runList()
	if i := m.cursor(panelRuns); i >= 0 && i < len(list) {
		return list[i], true
	}
	return nil, false
}

// panelItems is how many THINGS a panel is showing, which the title says.
//
// Not panelLen, which is rows: the Snapshots panel draws a heading per database
// and a heading is a row and not a snapshot. A title reading "Snapshots (5)"
// over three snapshots in two groups is a number nobody can reconcile with
// what is under it.
func (m *Model) panelItems(panel int) int {
	if panel == panelSnapshots || panel == panelRestorable {
		return m.snapshotCount(panel)
	}
	return m.panelLen(panel)
}

// panelLen is how many rows a panel has, which the cursor is clamped to.
func (m *Model) panelLen(panel int) int {
	switch panel {
	case panelConnections:
		return len(m.connections())
	case panelDatabases:
		return len(m.databases())
	case panelSnapshots:
		return len(m.snapshots())
	case panelRestorable:
		return len(m.restorable())
	case panelSets:
		return len(m.sets())
	case panelRuns:
		return len(m.runList())
	}
	return 0
}

// filtered applies the / filter, but only to the panel that has focus.
//
// Filtering every panel from one box would silently empty the panels above and
// below the one being searched, which reads as data loss rather than as a
// filter.
func filtered[T any](items []T, focused bool, filter string, name func(T) string) []T {
	if !focused || filter == "" {
		return items
	}
	needle := strings.ToLower(filter)
	out := make([]T, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(name(item)), needle) {
			out = append(out, item)
		}
	}
	return out
}

// databaseNames is what the selected connection actually has, which is what the
// snapshot form offers.
//
// From the PROBE, not from the config: the config no longer claims to know what
// databases exist (decision 8a), so an unreachable connection has nothing to
// offer and says so rather than offering a list that might be wrong.
//
// Unfiltered by the / filter, deliberately. The filter narrows the panel a
// reader is looking through; a form built from it would silently offer three of
// six databases because somebody had typed "cl" a minute ago.
func (m *Model) databaseNames() []string {
	conn, ok := m.selectedConn()
	if !ok {
		return nil
	}
	p := m.probes[conn.Name]
	if p == nil || !p.Reachable {
		return nil
	}
	out := make([]string, 0, len(p.Databases))
	for _, db := range p.Databases {
		out = append(out, db.Name)
	}
	return out
}

// liveKey identifies a cached live table listing.
func liveKey(connection, database string) string { return connection + "/" + database }

// setKey identifies a cached set resolution.
func setKey(connection, database, set string) string {
	return connection + "/" + database + "/" + set
}

// sortedRules returns the rules that match a table, for the detail views.
func (m *Model) ruleFor(table string) config.Rule { return m.cfg.RuleFor(table) }
