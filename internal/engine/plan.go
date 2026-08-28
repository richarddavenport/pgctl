package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/pg"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// ApplyRequest is one restore to perform.
type ApplyRequest struct {
	// Snapshot is the id of the snapshot to apply.
	Snapshot string

	// Target is the environment to apply it to.
	Target string

	// Set names a table set, restoring only those tables. Empty means the
	// whole database, which is a different mechanism — see decisions #6.
	Set string

	// Tables restricts the apply to these tables instead of a named set. The
	// selection is still closed against the target's foreign keys.
	Tables []string

	// Widen accepts the referential closure of the selection rather than
	// refusing an unclosed one.
	Widen bool
}

// Plan is what an apply will do, computed before anything is touched so that
// the operator confirms a description rather than a hope.
type Plan struct {
	Snapshot *snapshot.Manifest
	Target   *Target

	// WholeDatabase distinguishes the two mechanisms: a drop-and-recreate of
	// the database, or a load of some tables into the one that is there.
	WholeDatabase bool

	// Selection is the tables to load, and Order the layers to load them in.
	Selection []string
	Order     pg.Plan

	// Added lists tables pulled in by closing the selection over foreign keys.
	Added []string

	// MissingFromSnapshot lists selected tables the snapshot does not contain.
	// A snapshot older than a migration is the usual cause.
	MissingFromSnapshot []string

	// DropFKs are the selection's own foreign keys, dropped before the load and
	// restored after it.
	DropFKs []pg.FK

	// BlockingFKs point into the selection from tables outside it. Their
	// children keep rows referencing data that is about to be replaced, so
	// they are dropped and revalidated too — and a validation failure
	// afterwards is a real finding: the new data does not support the old rows.
	BlockingFKs []pg.FK

	// DropIndexes are secondary indexes rebuilt after the load.
	DropIndexes []pg.Index

	// Bytes is the selection's size on the source, the best estimate of how
	// much work this is.
	Bytes int64

	// Drift describes foreign keys that differ between the snapshot's source
	// and this target.
	Drift []string

	Warnings []string
}

// RefusalError is a considered decision not to run: a selection that cannot
// be loaded safely, or a target that may never be written to. Distinguished
// from a failure so that a front end can present it as a decision rather than
// as a crash.
type RefusalError struct{ Reason string }

func (r *RefusalError) Error() string { return r.Reason }

// Plan computes what an apply would do. It connects to the target — the
// target's constraints are what a load has to satisfy, not the source's — and
// refuses rather than guesses.
func (e *Engine) Plan(ctx context.Context, req ApplyRequest, report Reporter) (*Plan, error) {
	man, _, err := e.openSnapshot(req.Snapshot)
	if err != nil {
		return nil, err
	}
	if !man.Complete() {
		return nil, &RefusalError{fmt.Sprintf("snapshot %s did not finish and cannot be applied", man.ID)}
	}

	target, err := Resolve(ctx, e.cfg, e.root, req.Target, man.Database)
	if err != nil {
		return nil, err
	}
	if target.Env.Protected {
		return nil, &RefusalError{fmt.Sprintf(
			"%s is a protected environment and can never be an apply target", target.Env.Name)}
	}
	if target.Env.Name == man.Environment {
		report.warn(fmt.Sprintf("applying %s back to %s, the environment it came from", man.ID, man.Environment))
	}

	plan := &Plan{Snapshot: man, Target: target}
	plan.WholeDatabase = req.Set == "" && len(req.Tables) == 0

	// A whole-database apply drops and recreates the database, so the target's
	// current constraints are about to stop existing: there is nothing to
	// order, drop or rebuild, and nothing to introspect. It must not connect to
	// the database either — the first refresh of a new environment is the case
	// where that database does not exist yet, and a planner that cannot plan
	// the first restore is a planner nobody can rely on.
	if plan.WholeDatabase {
		plan.Selection = man.TableNames()
		plan.Bytes = man.Bytes

		exists, err := e.databaseExists(ctx, target, man.Database)
		if err != nil {
			return nil, err
		}
		if !exists {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"%s does not exist on %s yet and will be created", man.Database, target.Env.Name))
		}
		return plan, nil
	}

	db, _ := e.database(man.Database)
	conn, err := target.Connect(ctx, man.Database)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx) //nolint:errcheck // nothing useful to do with a close failure

	cat, err := pg.Introspect(ctx, conn, db.ExcludeSchemas)
	if err != nil {
		return nil, err
	}

	selection, err := e.resolveSelection(req, cat)
	if err != nil {
		return nil, err
	}

	missing := cat.Graph.MissingParents(selection)
	if len(missing) > 0 {
		if !req.Widen {
			return nil, &RefusalError{fmt.Sprintf(
				"selection is not referentially closed: %s reference %s, which the selection does not include.\n"+
					"Re-run with --widen to include them, or choose a set that does.",
				summarize(selection), strings.Join(missing, ", "))}
		}
		selection = cat.Graph.Closure(selection)
		plan.Added = missing
	}
	sort.Strings(selection)

	plan.Selection = selection
	plan.Order = cat.Graph.LoadOrder(selection)
	plan.DropFKs = cat.Graph.FKsWithin(selection)
	plan.BlockingFKs = cat.Graph.FKsInbound(selection)

	indexes, err := pg.Indexes(ctx, conn, selection)
	if err != nil {
		return nil, err
	}
	plan.DropIndexes = indexes

	for _, name := range selection {
		entry, ok := man.Table(name)
		if !ok {
			plan.MissingFromSnapshot = append(plan.MissingFromSnapshot, name)
			continue
		}
		plan.Bytes += entry.SourceBytes
		if entry.Data == config.DataNone {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"%s carries no data in this snapshot, so applying it empties the table", name))
		}
	}
	if len(plan.MissingFromSnapshot) > 0 {
		return nil, &RefusalError{fmt.Sprintf(
			"snapshot %s does not contain %s — it predates a migration that created them",
			man.ID, strings.Join(plan.MissingFromSnapshot, ", "))}
	}

	plan.Drift = driftBetween(man.ForeignKeys, cat.FKs, selection)
	plan.Warnings = append(plan.Warnings, plan.Drift...)
	if len(plan.Order.Cycles) > 0 {
		for _, c := range plan.Order.Cycles {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"%s reference each other, so they load as one group with their foreign keys deferred",
				strings.Join(c, " and ")))
		}
	}
	return plan, nil
}

// databaseExists asks the maintenance database whether the target database is
// there, which is also the cheapest proof that the credentials work before a
// long restore starts.
func (e *Engine) databaseExists(ctx context.Context, target *Target, database string) (bool, error) {
	conn, err := target.Connect(ctx, target.MaintenanceDB())
	if err != nil {
		return false, err
	}
	defer conn.Close(ctx) //nolint:errcheck // nothing useful to do with a close failure

	var exists bool
	const q = `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`
	if err := conn.QueryRow(ctx, q, database).Scan(&exists); err != nil {
		return false, fmt.Errorf("check for database %s: %w", database, err)
	}
	return exists, nil
}

// resolveSelection turns a set name or an explicit table list into concrete
// tables that exist on the target.
func (e *Engine) resolveSelection(req ApplyRequest, cat *pg.Catalog) ([]string, error) {
	if req.Set != "" {
		set, ok := e.cfg.LookupSet(req.Set)
		if !ok {
			return nil, fmt.Errorf("unknown set %q", req.Set)
		}
		var out []string
		for _, t := range cat.Tables {
			if set.Matches(t.Name) {
				out = append(out, t.Name)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("set %q matches no table on the target", req.Set)
		}
		return out, nil
	}

	var out []string
	for _, pattern := range req.Tables {
		matched := false
		for _, t := range cat.Tables {
			if config.MatchPattern(pattern, t.Name) {
				out = append(out, t.Name)
				matched = true
			}
		}
		if !matched {
			return nil, fmt.Errorf("%q matches no table on the target", pattern)
		}
	}
	return dedupe(out), nil
}

// driftBetween reports foreign keys that differ between the snapshot's source
// and the target, restricted to the selection. A constraint added on the source
// after this snapshot was taken, or one the target has that the source did not,
// both mean the load is not the like-for-like swap it appears to be.
func driftBetween(source, target []pg.FK, selection []string) []string {
	relevant := func(fk pg.FK) bool {
		for _, t := range selection {
			if fk.Child == t || fk.Parent == t {
				return true
			}
		}
		return false
	}
	index := func(fks []pg.FK) map[string]pg.FK {
		m := map[string]pg.FK{}
		for _, fk := range fks {
			if relevant(fk) {
				m[fk.Child+"."+fk.Name] = fk
			}
		}
		return m
	}
	src, dst := index(source), index(target)

	var out []string
	for key, fk := range dst {
		if _, ok := src[key]; !ok {
			out = append(out, fmt.Sprintf("target has foreign key %s on %s that the snapshot's source did not",
				fk.Name, fk.Child))
		}
	}
	for key, fk := range src {
		if _, ok := dst[key]; !ok {
			out = append(out, fmt.Sprintf("snapshot's source had foreign key %s on %s that the target does not",
				fk.Name, fk.Child))
		}
	}
	sort.Strings(out)
	return out
}

// Describe renders a plan for confirmation. This text is the last thing between
// an operator and a destructive act, so it leads with what is destroyed.
func (p *Plan) Describe() string {
	var b strings.Builder

	if p.WholeDatabase {
		fmt.Fprintf(&b, "DROP AND RECREATE database %s on %s\n", p.Snapshot.Database, p.Target.Env.Name)
		fmt.Fprintf(&b, "  from snapshot %s taken %s\n", p.Snapshot.ID,
			p.Snapshot.StartedAt.Format("2006-01-02 15:04 MST"))
		fmt.Fprintf(&b, "  %d tables, %s\n", len(p.Selection), humanBytes(p.Bytes))
	} else {
		fmt.Fprintf(&b, "REPLACE %d tables in %s on %s\n",
			len(p.Selection), p.Snapshot.Database, p.Target.Env.Name)
		fmt.Fprintf(&b, "  from snapshot %s taken %s\n", p.Snapshot.ID,
			p.Snapshot.StartedAt.Format("2006-01-02 15:04 MST"))
		fmt.Fprintf(&b, "  %s of source data\n", humanBytes(p.Bytes))
		if len(p.Added) > 0 {
			fmt.Fprintf(&b, "  widened to include %s\n", strings.Join(p.Added, ", "))
		}
		fmt.Fprintf(&b, "  %d foreign keys dropped and rebuilt, %d indexes rebuilt\n",
			len(p.DropFKs)+len(p.BlockingFKs), len(p.DropIndexes))
		b.WriteString("\nload order:\n")
		b.WriteString(indent(p.Order.String()))
	}

	for _, w := range p.Warnings {
		fmt.Fprintf(&b, "\nwarning: %s", w)
	}
	if len(p.Warnings) > 0 {
		b.WriteString("\n")
	}
	return b.String()
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n") + "\n"
}

func summarize(tables []string) string {
	if len(tables) <= 3 {
		return strings.Join(tables, ", ")
	}
	return fmt.Sprintf("%s and %d others", strings.Join(tables[:3], ", "), len(tables)-3)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
