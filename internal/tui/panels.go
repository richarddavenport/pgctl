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
	return filtered(m.cfg.All(), m.focus == panelConnections, m.filter,
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
	return filtered(p.Databases, m.focus == panelDatabases, m.filter,
		func(d engine.DatabaseInfo) string { return d.Name })
}

func (m *Model) selectedDatabase() (engine.DatabaseInfo, bool) {
	list := m.databases()
	if i := m.cursor(panelDatabases); i >= 0 && i < len(list) {
		return list[i], true
	}
	return engine.DatabaseInfo{}, false
}

// snapshots returns the snapshots of the selected environment and database,
// newest first.
//
// Filtered by the selection rather than showing everything: the panel above
// says which environment you are looking at, and a list that ignored it would
// make the hierarchy a lie.
func (m *Model) snapshots() []*engine.Entry {
	env, hasEnv := m.selectedConn()
	db, hasDB := m.selectedDatabase()

	var out []*engine.Entry
	for i := len(m.entries) - 1; i >= 0; i-- {
		entry := m.entries[i]
		if hasEnv && entry.Manifest.Connection != env.Name {
			continue
		}
		if hasDB && entry.Manifest.Database != db.Name {
			continue
		}
		out = append(out, entry)
	}
	return filtered(out, m.focus == panelSnapshots, m.filter,
		func(e *engine.Entry) string { return e.Manifest.ID })
}

func (m *Model) selectedSnapshot() (*engine.Entry, bool) {
	list := m.snapshots()
	if i := m.cursor(panelSnapshots); i >= 0 && i < len(list) {
		return list[i], true
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
	return filtered(out, m.focus == panelSets, m.filter,
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
	return filtered(out, m.focus == panelRuns, m.filter,
		func(r *runRecord) string { return r.kind })
}

func (m *Model) selectedRun() (*runRecord, bool) {
	list := m.runList()
	if i := m.cursor(panelRuns); i >= 0 && i < len(list) {
		return list[i], true
	}
	return nil, false
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

// liveKey identifies a cached live table listing.
func liveKey(connection, database string) string { return connection + "/" + database }

// setKey identifies a cached set resolution.
func setKey(connection, database, set string) string {
	return connection + "/" + database + "/" + set
}

// sortedRules returns the rules that match a table, for the detail views.
func (m *Model) ruleFor(table string) config.Rule { return m.cfg.RuleFor(table) }

// databaseNames is what the selected connection actually has, which is what a
// form offers as choices.
func (m *Model) databaseNames() []string {
	conn, ok := m.selectedConn()
	if !ok {
		return nil
	}
	p := m.probes[conn.Name]
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.Databases))
	for _, db := range p.Databases {
		out = append(out, db.Name)
	}
	sort.Strings(out)
	return out
}
