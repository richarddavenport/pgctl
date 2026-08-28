package snapshot

import (
	"sort"
	"time"
)

// Policy is how many snapshots of each cadence survive a prune. Zero means keep
// every snapshot of that cadence, so an unconfigured pgctl never deletes
// anything.
type Policy struct {
	Daily   int
	Weekly  int
	Monthly int
}

// Unset reports a policy that keeps everything.
func (p Policy) Unset() bool { return p.Daily == 0 && p.Weekly == 0 && p.Monthly == 0 }

// Keep decides which snapshots a policy retains.
//
// Grandfather-father-son, per calendar period rather than per count: the newest
// snapshot of each day, week and month is a candidate, and the policy says how
// many of each kind of period to go back. A snapshot kept by any one of the
// three is kept, so `daily: 7, weekly: 4` means "the last week in detail, and
// one a week for a month".
//
// Two properties matter more than the arithmetic. An incomplete snapshot is
// never a keeper — it cannot be restored, so retaining it in place of a
// complete one would silently reduce the real depth of history. And the single
// newest snapshot is always kept whatever the policy says, because a prune that
// can leave an environment with no snapshot at all is a prune nobody can run
// unattended.
func Keep(snapshots []*Manifest, p Policy) (keep, remove []*Manifest) {
	if len(snapshots) == 0 {
		return nil, nil
	}
	if p.Unset() {
		return snapshots, nil
	}

	// Newest first, so the first snapshot seen in any period is that period's
	// keeper.
	ordered := append([]*Manifest{}, snapshots...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].StartedAt.After(ordered[j].StartedAt)
	})

	keepers := map[string]bool{}
	keepers[ordered[0].ID] = true

	mark := func(limit int, key func(time.Time) string) {
		if limit <= 0 {
			return
		}
		seen := map[string]bool{}
		for _, m := range ordered {
			if !m.Complete() {
				continue
			}
			k := key(m.StartedAt)
			if seen[k] {
				continue
			}
			seen[k] = true
			keepers[m.ID] = true
			if len(seen) == limit {
				return
			}
		}
	}

	mark(p.Daily, func(t time.Time) string { return t.UTC().Format("2006-01-02") })
	mark(p.Weekly, func(t time.Time) string {
		year, week := t.UTC().ISOWeek()
		return isoWeekKey(year, week)
	})
	mark(p.Monthly, func(t time.Time) string { return t.UTC().Format("2006-01") })

	for _, m := range ordered {
		if keepers[m.ID] {
			keep = append(keep, m)
		} else {
			remove = append(remove, m)
		}
	}
	return keep, remove
}

func isoWeekKey(year, week int) string {
	return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006") + "-W" + twoDigits(week)
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
