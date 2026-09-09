package tui

import (
	"sort"
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

// snapshotRow is one row of the Snapshots panel: a snapshot, or the database
// heading above a group of them.
//
// One list of both, because comp.List's cursor is an index into the ROWS it was
// given — so a headings-and-snapshots panel whose selection reads from a
// separate slice of snapshots is a panel where the cursor and the detail pane
// disagree by however many headings sit above it.
type snapshotRow struct {
	// heading is a database name on a row that is a label rather than a thing.
	// comp.Row.Skip keeps the cursor off it.
	heading string
	entry   *engine.Entry
}

// snapshots returns the selected connection's snapshots, grouped by database.
//
// Grouped rather than FILTERED by the selected database, which is what this did
// and what made the interface contradict itself: on a server with ten
// databases the database cursor lands on the first alphabetically —
// `azure_maintenance` — while every snapshot anyone takes is of
// `product-development`. The connection's own detail pane read "1 snapshot,
// newest just now" beside a Snapshots panel reading "none — press n", and the
// panel was the one that was wrong.
//
// The hierarchy survives, because the selected database's group comes FIRST:
// moving the database cursor reorders the panel instead of emptying it. What it
// can no longer do is hide a snapshot that exists.
func (m *Model) snapshots() []snapshotRow {
	conn, hasConn := m.selectedConn()
	if !hasConn {
		return nil
	}

	// Newest first within a database, which is the order m.entries is not in.
	byDatabase := map[string][]*engine.Entry{}
	for i := len(m.entries) - 1; i >= 0; i-- {
		entry := m.entries[i]
		if entry.Manifest.Connection != conn.Name {
			continue
		}
		db := entry.Manifest.Database
		byDatabase[db] = append(byDatabase[db], entry)
	}
	// The filter narrows the snapshots and the groups are rebuilt from what is
	// left, so a group whose every snapshot was filtered out does not leave its
	// heading behind with nothing under it.
	if m.focus == panelSnapshots && m.filter.Text != "" {
		for db, entries := range byDatabase {
			kept := filtered(entries, true, m.filter.Text,
				func(e *engine.Entry) string { return e.Manifest.ID })
			if len(kept) == 0 {
				delete(byDatabase, db)
				continue
			}
			byDatabase[db] = kept
		}
	}

	// The selected database first, then the rest by their newest snapshot. A
	// name sort would be stable and would also bury the database somebody
	// snapshotted this morning under three nobody has ever touched.
	selected, _ := m.selectedDatabase()
	names := make([]string, 0, len(byDatabase))
	for db := range byDatabase {
		names = append(names, db)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == selected.Name) != (names[j] == selected.Name) {
			return names[i] == selected.Name
		}
		a, b := byDatabase[names[i]][0], byDatabase[names[j]][0]
		if !a.Manifest.StartedAt.Equal(b.Manifest.StartedAt) {
			return a.Manifest.StartedAt.After(b.Manifest.StartedAt)
		}
		return names[i] < names[j]
	})

	var out []snapshotRow
	for _, db := range names {
		// A heading per database, and only when there is more than one: with a
		// single group the heading repeats what the panel above already says,
		// on a panel twenty-eight columns wide.
		if len(names) > 1 {
			out = append(out, snapshotRow{heading: db})
		}
		for _, entry := range byDatabase[db] {
			out = append(out, snapshotRow{entry: entry})
		}
	}
	return out
}

// snapshotCount is how many snapshots the panel shows, which is not how many
// rows it has: the headings are rows and are not snapshots. The panel title
// says this number.
func (m *Model) snapshotCount() int {
	n := 0
	for _, r := range m.snapshots() {
		if r.entry != nil {
			n++
		}
	}
	return n
}

func (m *Model) selectedSnapshot() (*engine.Entry, bool) {
	list := m.snapshots()
	if i := m.cursor(panelSnapshots); i >= 0 && i < len(list) {
		return list[i].entry, list[i].entry != nil
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
	if panel == panelSnapshots {
		return m.snapshotCount()
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
