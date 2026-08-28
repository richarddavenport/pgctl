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
func (m *Model) environments() []config.Environment {
	return filtered(m.cfg.Environments, m.focus == panelEnvironments, m.filter,
		func(e config.Environment) string { return e.Name })
}

// selectedEnv is the focused environment.
func (m *Model) selectedEnv() (config.Environment, bool) {
	list := m.environments()
	if i := m.cursors[panelEnvironments]; i >= 0 && i < len(list) {
		return list[i], true
	}
	return config.Environment{}, false
}

// databases returns the selected environment's databases.
//
// From the live probe when there is one, so the list is what is actually on the
// server; from the config when there is not, so the panel is never empty just
// because a network call has not come back.
func (m *Model) databases() []engine.DatabaseInfo {
	env, ok := m.selectedEnv()
	if !ok {
		return nil
	}
	if p := m.probes[env.Name]; p != nil && p.Reachable {
		return filtered(p.Databases, m.focus == panelDatabases, m.filter,
			func(d engine.DatabaseInfo) string { return d.Name })
	}
	declared := make([]engine.DatabaseInfo, 0, len(m.cfg.Databases))
	for _, d := range m.cfg.Databases {
		declared = append(declared, engine.DatabaseInfo{Name: d.Name, Declared: true})
	}
	return filtered(declared, m.focus == panelDatabases, m.filter,
		func(d engine.DatabaseInfo) string { return d.Name })
}

func (m *Model) selectedDatabase() (engine.DatabaseInfo, bool) {
	list := m.databases()
	if i := m.cursors[panelDatabases]; i >= 0 && i < len(list) {
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
	env, hasEnv := m.selectedEnv()
	db, hasDB := m.selectedDatabase()

	var out []*engine.Entry
	for i := len(m.entries) - 1; i >= 0; i-- {
		entry := m.entries[i]
		if hasEnv && entry.Manifest.Environment != env.Name {
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
	if i := m.cursors[panelSnapshots]; i >= 0 && i < len(list) {
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
	if i := m.cursors[panelSets]; i >= 0 && i < len(list) {
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
	if i := m.cursors[panelRuns]; i >= 0 && i < len(list) {
		return list[i], true
	}
	return nil, false
}

// panelLen is how many rows a panel has, which the cursor is clamped to.
func (m *Model) panelLen(panel int) int {
	switch panel {
	case panelEnvironments:
		return len(m.environments())
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
func liveKey(env, database string) string { return env + "/" + database }

// setKey identifies a cached set resolution.
func setKey(env, database, set string) string { return env + "/" + database + "/" + set }

// sortedRules returns the rules that match a table, for the detail views.
func (m *Model) ruleFor(table string) config.Rule { return m.cfg.RuleFor(table) }

// declaredDatabaseNames is the set of databases pgctl is configured to manage.
func (m *Model) declaredDatabaseNames() []string {
	out := make([]string, 0, len(m.cfg.Databases))
	for _, d := range m.cfg.Databases {
		out = append(out, d.Name)
	}
	sort.Strings(out)
	return out
}
