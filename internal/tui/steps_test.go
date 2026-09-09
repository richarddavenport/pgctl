package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/richarddavenport/tuikit/comp"

	"github.com/richarddavenport/pgctl/internal/engine"
)

// The steps come from the engine's own event stream, not from a list this
// package keeps.
//
// That is the property worth a test: apply.go names a phase on everything it
// reports, and a phase added there has to appear here without an edit. A step
// list maintained beside the engine is a step list that eventually describes a
// run that no longer happens.
func TestTheStepsAreTheEnginesOwnPhases(t *testing.T) {
	m := fixtureModel(t)
	rec := fixtureRun(m)

	steps := rec.steps(epoch)
	var labels []string
	for _, s := range steps {
		labels = append(labels, s.Label)
	}
	want := []string{"drop constraints", "disable triggers", "truncate", "load"}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Errorf("steps = %v, want %v", labels, want)
	}

	// Everything but the last has finished, and each says what it cost.
	for _, s := range steps[:len(steps)-1] {
		if s.State != comp.StepDone {
			t.Errorf("%q is %v, want done", s.Label, s.State)
		}
		if s.Took == "" {
			t.Errorf("%q does not say how long it took", s.Label)
		}
	}
	last := steps[len(steps)-1]
	if last.State != comp.StepRunning {
		t.Errorf("%q is %v, want running — the operation has not finished",
			last.Label, last.State)
	}
	// The newest progress replaces the last one rather than appending, so a
	// byte counter counts instead of scrolling.
	if last.Detail != "504 MB of 1.9 GB" {
		t.Errorf("the running step says %q, want the newest progress", last.Detail)
	}
}

// A warning does not stop a step, and a failure does.
func TestAWarningLeavesTheStepRunningAndAFailureDoesNot(t *testing.T) {
	at := epoch
	rec := &runRecord{
		id:        "apply-0",
		kind:      "apply → qat",
		startedAt: at,
		endedAt:   at.Add(time.Minute),
		err:       errUnreachable,
		events: []engine.Event{
			{Kind: engine.EventStep, Step: "load", Message: "42 entries", At: at},
			{Kind: engine.EventWarning, Step: "load", Message: "quotes.quote will load empty", At: at.Add(time.Second)},
			{Kind: engine.EventFailed, Step: "load", Message: "could not connect", At: at.Add(2 * time.Second)},
		},
	}

	steps := rec.steps(at.Add(time.Minute))
	if len(steps) != 1 {
		t.Fatalf("%d steps, want 1", len(steps))
	}
	if steps[0].State != comp.StepFailed {
		t.Errorf("state = %v, want failed", steps[0].State)
	}
	// The failure's own message is the note. The warning was overwritten by it,
	// which is right: a step gets one line beneath it and the failure is the
	// one that explains why the run stopped.
	if !strings.Contains(steps[0].Note, "could not connect") {
		t.Errorf("note = %q, want the failure's message", steps[0].Note)
	}
}

// A cancelled run leaves its phase where it stopped rather than claiming it
// finished.
//
// q cancels the operation on the way out, so this is the state a run is left in
// by the most common way of ending one — and a step list that marked it done
// would say the restore completed.
func TestACancelledRunDoesNotClaimItsLastStepFinished(t *testing.T) {
	at := epoch
	rec := &runRecord{
		id:        "apply-0",
		startedAt: at,
		endedAt:   at.Add(30 * time.Second),
		err:       errUnreachable,
		events: []engine.Event{
			{Kind: engine.EventStep, Step: "load", Message: "42 entries", At: at},
		},
	}
	steps := rec.steps(at.Add(30 * time.Second))
	if steps[0].State != comp.StepFailed {
		t.Errorf("state = %v, want failed — the run did not finish", steps[0].State)
	}
}

// The meter counts DISTINCT tables, because the engine reports a table more
// than once: a load and then a reindex.
//
// A counter incremented per event runs past the denominator and clamps there
// for the second half of the run, which reads as a bar that stopped moving.
func TestTheMeterCountsTablesNotEvents(t *testing.T) {
	rec := &runRecord{
		total: 3,
		events: []engine.Event{
			{Kind: engine.EventTable, Step: "load", Table: "claims.policy_claim"},
			{Kind: engine.EventTable, Step: "load", Table: "claims.claim_detail"},
			{Kind: engine.EventTable, Step: "reindex", Table: "claims.policy_claim"},
			{Kind: engine.EventTable, Step: "validate", Table: "claims.policy_claim"},
		},
	}
	if got := rec.tablesSeen(); got != 2 {
		t.Errorf("tablesSeen = %d, want 2 distinct tables of 4 events", got)
	}
}
