// Package pg models a database's referential structure. Nothing here talks to
// a server: a Graph is built from rows read elsewhere (introspect.go) so that
// the ordering rules — the part a human gets wrong and the reason pgctl exists
// — are testable without a cluster.
package pg

import (
	"fmt"
	"sort"
	"strings"
)

// FK is one foreign key: Child's rows reference Parent's.
type FK struct {
	// Name is the constraint name, needed to drop and re-add it.
	Name string
	// Child holds the referencing columns, Parent the referenced ones. Both
	// are schema-qualified.
	Child  string
	Parent string
	// Columns and ParentColumns are recorded for reporting, not for rebuilding
	// — a rebuild uses the definition pg_get_constraintdef returns.
	Columns       []string
	ParentColumns []string
	// Def is the constraint's full definition, used verbatim to re-add it.
	Def string
	// NotValid records a constraint that is already NOT VALID upstream, so
	// that pgctl does not silently validate something production has not.
	NotValid bool
}

// SelfReferential reports a table whose foreign key points at itself — a
// parent_id hierarchy. These constrain row order inside one table's load, not
// the order tables load in, so the ordering code has to ignore them or it sees
// a cycle in every tree.
func (f FK) SelfReferential() bool { return f.Child == f.Parent }

// Graph is the referential structure of one database.
type Graph struct {
	tables  map[string]bool
	fks     []FK
	parents map[string]map[string]bool
	kids    map[string]map[string]bool
}

// NewGraph builds a graph over the given tables. A foreign key naming a table
// outside the list is kept — it is exactly the case that breaks a restore, and
// callers ask about it through MissingParents.
func NewGraph(tables []string, fks []FK) *Graph {
	g := &Graph{
		tables:  make(map[string]bool, len(tables)),
		parents: map[string]map[string]bool{},
		kids:    map[string]map[string]bool{},
	}
	for _, t := range tables {
		g.tables[t] = true
	}
	for _, fk := range fks {
		g.fks = append(g.fks, fk)
		if fk.SelfReferential() {
			continue
		}
		add(g.parents, fk.Child, fk.Parent)
		add(g.kids, fk.Parent, fk.Child)
	}
	return g
}

func add(m map[string]map[string]bool, from, to string) {
	if m[from] == nil {
		m[from] = map[string]bool{}
	}
	m[from][to] = true
}

// Tables returns every table in the graph, sorted.
func (g *Graph) Tables() []string { return sortedKeys(g.tables) }

// Has reports whether the graph knows the table.
func (g *Graph) Has(table string) bool { return g.tables[table] }

// Parents returns the tables a table references directly, sorted.
func (g *Graph) Parents(table string) []string { return sortedKeys(g.parents[table]) }

// Children returns the tables that reference a table directly, sorted.
func (g *Graph) Children(table string) []string { return sortedKeys(g.kids[table]) }

// FKsWithin returns the foreign keys whose child is in the selection —
// the constraints a set-level apply must drop before it can truncate and
// reload those tables.
func (g *Graph) FKsWithin(selection []string) []FK {
	in := set(selection)
	var out []FK
	for _, fk := range g.fks {
		if in[fk.Child] {
			out = append(out, fk)
		}
	}
	return byName(out)
}

// FKsInbound returns foreign keys pointing *into* the selection from tables
// outside it. These are the reason a truncate fails: the rows being replaced
// are referenced by rows that are staying.
func (g *Graph) FKsInbound(selection []string) []FK {
	in := set(selection)
	var out []FK
	for _, fk := range g.fks {
		if in[fk.Parent] && !in[fk.Child] {
			out = append(out, fk)
		}
	}
	return byName(out)
}

// MissingParents returns the tables the selection references but does not
// contain, transitively and sorted. An empty result means the selection is
// referentially closed and can be loaded on its own.
func (g *Graph) MissingParents(selection []string) []string {
	closure := g.Closure(selection)
	in := set(selection)
	var missing []string
	for _, t := range closure {
		if !in[t] {
			missing = append(missing, t)
		}
	}
	return missing
}

// Closure returns the selection plus every table it transitively references,
// sorted. This is the smallest set that can be restored without leaving a
// foreign key pointing at data nobody loaded.
func (g *Graph) Closure(selection []string) []string {
	seen := map[string]bool{}
	queue := append([]string{}, selection...)
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		if seen[t] {
			continue
		}
		seen[t] = true
		for p := range g.parents[t] {
			if !seen[p] {
				queue = append(queue, p)
			}
		}
	}
	return sortedKeys(seen)
}

// Layer is a group of tables with no referential dependency on each other, so
// every table in it can be loaded at the same time.
type Layer []string

// Plan is a load order.
type Plan struct {
	// Layers cover every table in the selection, parents first. Tables within
	// a layer have no dependency on each other and can load in parallel.
	Layers []Layer

	// Cycles are the groups of two or more tables that reference each other,
	// which no ordering satisfies. They are placed in Layers all the same — as
	// one group, at the position the rest of the graph demands — because a
	// set-level apply drops the selection's foreign keys before loading, so a
	// cycle is loadable. It is not, however, describable as a sequence, and an
	// operator who has been told about it can reason about a failure that an
	// operator who has not cannot.
	Cycles []Layer
}

// LoadOrder returns the order to load the selection in: parents before
// children, tables within a layer in parallel.
//
// Cycles do not defeat it. The selection is condensed into its strongly
// connected components — each a table on its own, or a group that references
// itself in a ring — and the components, which always form a DAG, are ordered.
// A ring therefore holds up only the tables genuinely behind it, rather than
// dragging every downstream table into one unorderable heap.
//
// Only dependencies *inside* the selection order it. A parent outside the
// selection is not something this load can sequence around; MissingParents is
// the question to ask about those.
func (g *Graph) LoadOrder(selection []string) Plan {
	comps := g.components(selection)

	// Condense: which components does each component depend on?
	owner := map[string]int{}
	for i, c := range comps {
		for _, t := range c {
			owner[t] = i
		}
	}
	deps := make([]map[int]bool, len(comps))
	for i := range comps {
		deps[i] = map[int]bool{}
		for _, t := range comps[i] {
			for p := range g.parents[t] {
				j, inSelection := owner[p]
				if inSelection && j != i {
					deps[i][j] = true
				}
			}
		}
	}

	var plan Plan
	for _, c := range comps {
		if len(c) > 1 {
			plan.Cycles = append(plan.Cycles, Layer(c))
		}
	}

	done := make([]bool, len(comps))
	remaining := len(comps)
	for remaining > 0 {
		var ready []int
		for i := range comps {
			if done[i] {
				continue
			}
			satisfied := true
			for j := range deps[i] {
				if !done[j] {
					satisfied = false
					break
				}
			}
			if satisfied {
				ready = append(ready, i)
			}
		}
		if len(ready) == 0 {
			// Unreachable: the condensation of any graph is acyclic. Bail
			// rather than spin, and leave the tables in Cycles so a caller
			// that ignores this comment still sees them.
			var stuck []string
			for i := range comps {
				if !done[i] {
					stuck = append(stuck, comps[i]...)
				}
			}
			sort.Strings(stuck)
			plan.Cycles = append(plan.Cycles, Layer(stuck))
			break
		}
		var layer Layer
		for _, i := range ready {
			layer = append(layer, comps[i]...)
			done[i] = true
			remaining--
		}
		sort.Strings(layer)
		plan.Layers = append(plan.Layers, layer)
	}
	return plan
}

// components returns the strongly connected components of the selection's
// subgraph, each sorted, using Tarjan's algorithm on the child-to-parent
// edges. Self-references are not edges in this graph (NewGraph drops them), so
// a parent_id hierarchy is a component of one.
func (g *Graph) components(selection []string) [][]string {
	in := set(selection)
	nodes := sortedKeys(in)

	var (
		index   = map[string]int{}
		low     = map[string]int{}
		onStack = map[string]bool{}
		stack   []string
		next    int
		out     [][]string
	)

	// Iterative rather than recursive: a schema is allowed to be deeper than
	// the goroutine stack is willing to be.
	type frame struct {
		node    string
		parents []string
		at      int
	}
	for _, root := range nodes {
		if _, seen := index[root]; seen {
			continue
		}
		frames := []frame{{node: root, parents: g.selectedParents(root, in)}}
		index[root], low[root] = next, next
		next++
		stack = append(stack, root)
		onStack[root] = true

		for len(frames) > 0 {
			f := &frames[len(frames)-1]
			if f.at < len(f.parents) {
				p := f.parents[f.at]
				f.at++
				if _, seen := index[p]; !seen {
					index[p], low[p] = next, next
					next++
					stack = append(stack, p)
					onStack[p] = true
					frames = append(frames, frame{node: p, parents: g.selectedParents(p, in)})
				} else if onStack[p] {
					low[f.node] = min(low[f.node], index[p])
				}
				continue
			}

			if low[f.node] == index[f.node] {
				var comp []string
				for {
					t := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[t] = false
					comp = append(comp, t)
					if t == f.node {
						break
					}
				}
				sort.Strings(comp)
				out = append(out, comp)
			}
			node := f.node
			frames = frames[:len(frames)-1]
			if len(frames) > 0 {
				parent := frames[len(frames)-1].node
				low[parent] = min(low[parent], low[node])
			}
		}
	}

	// Deterministic component order, so a plan reads the same way twice.
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func (g *Graph) selectedParents(table string, in map[string]bool) []string {
	var out []string
	for p := range g.parents[table] {
		if in[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// TruncateOrder is the load order reversed: children are emptied before the
// parents they reference, so no statement ever violates a constraint that is
// still in place.
func (g *Graph) TruncateOrder(selection []string) Plan {
	plan := g.LoadOrder(selection)
	reversed := make([]Layer, 0, len(plan.Layers))
	for i := len(plan.Layers) - 1; i >= 0; i-- {
		reversed = append(reversed, plan.Layers[i])
	}
	plan.Layers = reversed
	return plan
}

// Flat returns the plan's layers as a single sequence, layers in order.
func (p Plan) Flat() []string {
	var out []string
	for _, l := range p.Layers {
		out = append(out, l...)
	}
	return out
}

// String renders a plan the way the TUI and the CLI both describe it.
func (p Plan) String() string {
	var b strings.Builder
	for i, l := range p.Layers {
		fmt.Fprintf(&b, "%d. %s\n", i+1, strings.Join(l, ", "))
	}
	for _, c := range p.Cycles {
		fmt.Fprintf(&b, "cycle: %s\n", strings.Join(c, ", "))
	}
	return b.String()
}

// byName sorts constraints so that a plan reads the same way twice.
func byName(fks []FK) []FK {
	sort.Slice(fks, func(i, j int) bool { return fks[i].Name < fks[j].Name })
	return fks
}

func set(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k, ok := range m {
		if ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
