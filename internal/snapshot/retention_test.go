package snapshot

import (
	"testing"
	"time"
)

func at(day int) time.Time { return time.Date(2026, 8, day, 3, 0, 0, 0, time.UTC) }

func man(id string, t time.Time, complete bool) *Manifest {
	m := &Manifest{ID: id, StartedAt: t}
	if complete {
		m.FinishedAt = t.Add(10 * time.Minute)
	}
	return m
}

func ids(ms []*Manifest) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func TestUnsetPolicyDeletesNothing(t *testing.T) {
	var all []*Manifest
	for d := 1; d <= 30; d++ {
		all = append(all, man(string(rune('a'+d)), at(d), true))
	}
	keep, remove := Keep(all, Policy{})
	if len(remove) != 0 {
		t.Errorf("an unset policy deleted %v", ids(remove))
	}
	if len(keep) != len(all) {
		t.Errorf("kept %d of %d", len(keep), len(all))
	}
}

func TestDailyKeepsTheLastNDays(t *testing.T) {
	var all []*Manifest
	for d := 1; d <= 20; d++ {
		all = append(all, man(at(d).Format("02"), at(d), true))
	}
	keep, remove := Keep(all, Policy{Daily: 3})

	if got := ids(keep); len(got) != 3 {
		t.Fatalf("keep = %v, want the three newest days", got)
	}
	for _, want := range []string{"20", "19", "18"} {
		found := false
		for _, id := range ids(keep) {
			if id == want {
				found = true
			}
		}
		if !found {
			t.Errorf("day %s was not kept: keep = %v", want, ids(keep))
		}
	}
	if len(remove) != 17 {
		t.Errorf("remove = %d snapshots, want 17", len(remove))
	}
}

func TestWeeklyAndMonthlyExtendHistory(t *testing.T) {
	// One snapshot a day through August 2026. August 1 is a Saturday, so the
	// month spans six ISO weeks.
	var all []*Manifest
	for d := 1; d <= 31; d++ {
		all = append(all, man(at(d).Format("2006-01-02"), at(d), true))
	}

	daily, _ := Keep(all, Policy{Daily: 7})
	both, _ := Keep(all, Policy{Daily: 7, Weekly: 4})
	if len(both) <= len(daily) {
		t.Errorf("adding a weekly rule kept %d, no more than daily alone (%d)", len(both), len(daily))
	}

	withMonthly, _ := Keep(all, Policy{Daily: 7, Weekly: 4, Monthly: 3})
	if len(withMonthly) < len(both) {
		t.Errorf("adding a monthly rule kept fewer: %d < %d", len(withMonthly), len(both))
	}
}

func TestAnIncompleteSnapshotIsNeverAKeeper(t *testing.T) {
	// The newest snapshot of the newest day did not finish. The day's keeper
	// must be the complete one behind it, not the broken one — otherwise a
	// policy of `daily: 1` retains a snapshot that cannot be restored.
	all := []*Manifest{
		man("broken", at(10).Add(2*time.Hour), false),
		man("good", at(10), true),
		man("older", at(9), true),
	}
	keep, remove := Keep(all, Policy{Daily: 1})

	kept := map[string]bool{}
	for _, id := range ids(keep) {
		kept[id] = true
	}
	if !kept["good"] {
		t.Errorf("the complete snapshot was not kept: keep = %v", ids(keep))
	}
	// The very newest is kept regardless, so that a prune can never empty an
	// environment — but it is kept as the newest, not as the day's backup.
	if !kept["broken"] {
		t.Errorf("the newest snapshot was deleted: remove = %v", ids(remove))
	}
	if kept["older"] {
		t.Errorf("`daily: 1` kept a second day: keep = %v", ids(keep))
	}
}

func TestTheNewestSnapshotSurvivesAnyPolicy(t *testing.T) {
	all := []*Manifest{man("only", at(1), true)}
	keep, remove := Keep(all, Policy{Daily: 0, Weekly: 0, Monthly: 1})
	if len(keep) != 1 || len(remove) != 0 {
		t.Errorf("keep = %v, remove = %v; a prune must never empty an environment", ids(keep), ids(remove))
	}
}
