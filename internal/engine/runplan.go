package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// RunApplyRequest is one restore of a whole run, or of some of its databases.
type RunApplyRequest struct {
	// Run is a run id, a `<connection>/latest`, or a single-database snapshot
	// id — see Engine.OpenRun.
	Run string

	// Target is the connection to apply it to.
	Target string

	// Databases restricts the apply to some of the run's members. Empty means
	// every one of them, which is what a run means.
	Databases []string

	// Set and Tables narrow to part of ONE database, so they require the
	// selection to be one database — see PlanRun.
	Set    string
	Tables []string

	Widen bool
}

// Refusal is one database the engine will not restore, and why.
//
// A value rather than an error, because a run-level apply carries several at
// once and they are a finding rather than a failure: five databases that will
// restore and one that will not is information, and the run should say so
// before it starts rather than stopping at the one.
type Refusal struct {
	Database string
	Reason   string
}

// RunPlan is what applying a run would do: a plan per database it will restore,
// and a refusal per database it will not.
type RunPlan struct {
	Run    *Run
	Target string

	// Plans are in the order the databases will be restored, which is the order
	// they are listed in the run — alphabetical, and arbitrary. Databases are
	// independent: nothing in one database's foreign keys can reach another.
	Plans    []*Plan
	Refusals []Refusal
}

// Bytes is the source size of everything that will be restored.
func (p *RunPlan) Bytes() int64 {
	var total int64
	for _, plan := range p.Plans {
		total += plan.Bytes
	}
	return total
}

// Tables is how many tables will be loaded across every database.
func (p *RunPlan) Tables() int {
	n := 0
	for _, plan := range p.Plans {
		n += len(plan.Selection)
	}
	return n
}

// Warnings is every plan's warnings, each naming its database, because a
// warning about `quotes.quote` means nothing without the database it is in.
func (p *RunPlan) Warnings() []string {
	var out []string
	for _, plan := range p.Plans {
		for _, w := range plan.Warnings {
			out = append(out, plan.Snapshot.Database+": "+w)
		}
	}
	return out
}

// CanWiden reports a plan some database of which could be widened: a
// whole-database apply has nothing to widen, and a selection already closed has
// nothing left to add.
func (p *RunPlan) CanWiden() bool {
	for _, one := range p.Plans {
		if !one.WholeDatabase && len(one.Added) == 0 {
			return true
		}
	}
	return false
}

// PlanRun computes a plan per database of a run.
//
// A refusal for one database does not refuse the others, which is the decision
// worth reading: databases are independent — no foreign key crosses them — so a
// table missing from one database's snapshot says nothing about the other five,
// and refusing all six would make one stale member block a refresh of the
// estate. The refusals are returned rather than raised, and the confirmation
// screen lists them.
//
// What DOES refuse everything is a request that cannot mean anything: a
// protected target, or a set named for a run of more than one database.
func (e *Engine) PlanRun(ctx context.Context, req RunApplyRequest, report Reporter) (*RunPlan, error) {
	run, err := e.OpenRun(ctx, req.Run, report)
	if err != nil {
		return nil, err
	}

	members, err := selectMembers(run, req.Databases)
	if err != nil {
		return nil, err
	}

	// A set or a table list names tables in ONE database — config.Set carries a
	// database, and a table pattern is resolved against one catalog — so it
	// cannot narrow a run of several. Refused rather than applied to whichever
	// member happened to match, which is how a "claims set" apply could have
	// silently restored the whole of five other databases.
	if (req.Set != "" || len(req.Tables) > 0) && len(members) > 1 {
		return nil, &RefusalError{fmt.Sprintf(
			"a set or table selection applies to one database, and %s covers %s. "+
				"Name one with --db", run.ID, strings.Join(run.Databases(), ", "))}
	}

	plan := &RunPlan{Run: run, Target: req.Target}
	for _, member := range members {
		one, err := e.Plan(ctx, ApplyRequest{
			Snapshot: member.Manifest.ID,
			Target:   req.Target,
			Set:      req.Set,
			Tables:   req.Tables,
			Widen:    req.Widen,
		}, report)
		if err == nil {
			plan.Plans = append(plan.Plans, one)
			continue
		}

		// A refusal is per database and carried; anything else — an unreachable
		// target, credentials that do not work — is about the whole request and
		// stops it. Telling those apart is what RefusalError is for.
		var refusal *RefusalError
		if errors.As(err, &refusal) {
			plan.Refusals = append(plan.Refusals,
				Refusal{Database: member.Manifest.Database, Reason: refusal.Reason})
			continue
		}
		return nil, err
	}

	if len(plan.Plans) == 0 {
		return plan, &RefusalError{fmt.Sprintf(
			"nothing in %s can be applied to %s: %s", run.ID, req.Target,
			describeRefusals(plan.Refusals))}
	}
	return plan, nil
}

// ExecuteRun restores every plan, in order, stopping at the first FAILURE.
//
// Stopping, not continuing: a refusal was already dealt with at plan time, so a
// failure here is the environment misbehaving — a dropped connection, a full
// disk, a lock that never came — and the next database would meet the same
// thing. What it costs is a partial restore, which is why the report says which
// databases finished.
func (e *Engine) ExecuteRun(ctx context.Context, plan *RunPlan, report Reporter) error {
	var done []string
	for _, one := range plan.Plans {
		database := one.Snapshot.Database
		if err := e.Execute(ctx, one, report.about(database)); err != nil {
			if len(done) > 0 {
				return fmt.Errorf("%s restored (%s), then %s failed: %w",
					plural(len(done), "database"), strings.Join(done, ", "), database, err)
			}
			return err
		}
		done = append(done, database)
	}
	report.send(Event{Kind: EventDone, Step: "apply",
		Message: fmt.Sprintf("restored %s to %s from %s",
			strings.Join(done, ", "), plan.Target, plan.Run.ID)})
	return nil
}

// selectMembers is the run's members the request names, in the run's order.
func selectMembers(run *Run, databases []string) ([]*Entry, error) {
	if len(databases) == 0 {
		return run.Members, nil
	}
	var out []*Entry
	for _, name := range databases {
		member, ok := run.Member(name)
		if !ok {
			return nil, fmt.Errorf("%s has no snapshot of %q — it covers %s",
				run.ID, name, strings.Join(run.Databases(), ", "))
		}
		out = append(out, member)
	}
	// The run's own order, not the order they were asked for: a caller listing
	// databases is naming a set, not a sequence.
	sort.Slice(out, func(i, j int) bool {
		return out[i].Manifest.Database < out[j].Manifest.Database
	})
	return out, nil
}

// describeRefusals is the reasons, one per line, for an error that has to carry
// several.
func describeRefusals(refusals []Refusal) string {
	out := make([]string, 0, len(refusals))
	for _, r := range refusals {
		out = append(out, r.Database+": "+r.Reason)
	}
	return strings.Join(out, "; ")
}
