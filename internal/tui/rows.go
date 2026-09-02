package tui

import (
	"fmt"
	"time"

	"github.com/richarddavenport/tuikit/comp"

	"github.com/richarddavenport/pgctl/internal/engine"
)

// panelRows renders one panel's list. Each row is a single line already styled
// for its content but not for selection, which the panel applies.
func (m *Model) panelRows(panel int) []string {
	switch panel {
	case panelConnections:
		return m.connectionRows()
	case panelDatabases:
		return m.databaseRows()
	case panelSnapshots:
		return m.snapshotRows()
	case panelSets:
		return m.setRows()
	case panelRuns:
		return m.runRows()
	}
	return nil
}

func (m *Model) connectionRows() []string {
	conns := m.connections()
	out := make([]string, 0, len(conns))
	for _, conn := range conns {
		// The marker answers "can I reach it" before the name answers "which
		// is it", because an unreachable environment changes what every panel
		// below is showing.
		mark := mutedStyle.Render("○")
		note := ""
		switch {
		case m.probing[conn.Name]:
			mark = mutedStyle.Render(spinner(m.now))
		case m.probes[conn.Name] == nil:
		case m.probes[conn.Name].Reachable:
			mark = okStyle.Render("●")
			note = mutedStyle.Render(" " + formatServerVersion(m.probes[conn.Name].ServerVersion))
		default:
			mark = dangerStyle.Render("✗")
		}

		switch {
		case conn.Protected:
			note = dangerStyle.Render(" protected")
		case conn.Guarded:
			note = warnStyle.Render(" guarded")
		}
		out = append(out, fmt.Sprintf("%s %-9s%s", mark, truncate(conn.Name, 9), note))
	}
	return out
}

func (m *Model) databaseRows() []string {
	dbs := m.databases()
	out := make([]string, 0, len(dbs))
	for _, db := range dbs {
		size := mutedStyle.Render("       -")
		if db.Bytes > 0 {
			size = fmt.Sprintf("%8s", engine.HumanBytes(db.Bytes))
		}
		// The name takes whatever the size column leaves, so a long database
		// name is only shortened when it genuinely does not fit.
		width := panelInner - 9
		out = append(out, fmt.Sprintf("%-*s %s", width, truncate(db.Name, width), size))
	}
	return out
}

func (m *Model) snapshotRows() []string {
	snaps := m.snapshots()
	out := make([]string, 0, len(snaps))
	for _, entry := range snaps {
		man := entry.Manifest
		stamp := man.StartedAt.Local().Format("01-02 15:04")

		where := mutedStyle.Render("l")
		switch {
		case entry.Local && entry.Remote:
			where = okStyle.Render("l+r")
		case entry.Remote:
			where = accentStyle.Render("r")
		}
		state := ""
		if !man.Complete() {
			state = dangerStyle.Render(" ✗")
		}
		out = append(out, fmt.Sprintf("%s %7s %s%s",
			stamp, engine.HumanBytes(man.Bytes), where, state))
	}
	return out
}

func (m *Model) setRows() []string {
	conn, _ := m.selectedConn()
	db, _ := m.selectedDatabase()

	sets := m.sets()
	out := make([]string, 0, len(sets))
	for _, set := range sets {
		note := mutedStyle.Render(" ?")
		if s := m.setInfo[setKey(conn.Name, db.Name, set.Name)]; s != nil {
			switch {
			case s.loading:
				note = mutedStyle.Render(" " + spinner(m.now))
			case s.err != nil:
				note = dangerStyle.Render(" ✗")
			case len(s.added) > 0:
				// The number that matters about a set is not how many tables it
				// names but how many it drags in.
				note = warnStyle.Render(fmt.Sprintf(" %d+%d", len(s.members), len(s.added)))
			default:
				note = okStyle.Render(fmt.Sprintf(" %d closed", len(s.members)))
			}
		}
		out = append(out, truncate(set.Name, panelInner-8)+note)
	}
	return out
}

func (m *Model) runRows() []string {
	runs := m.runList()
	out := make([]string, 0, len(runs))
	for _, r := range runs {
		mark := okStyle.Render("✓")
		switch {
		case r.running:
			mark = accentStyle.Render(spinner(m.now))
		case r.err != nil:
			mark = dangerStyle.Render("✗")
		}
		out = append(out, fmt.Sprintf("%s %-*s %s",
			mark, panelInner-9, truncate(r.kind, panelInner-9),
			mutedStyle.Render(elapsed(r.duration(m.now)))))
	}
	return out
}

// formatServerVersion renders 170004 as "17.4".
func formatServerVersion(v int) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprintf("%d.%d", v/10000, v%10000)
}

// elapsed formats a duration as m:ss, or h:mm:ss past an hour.
//
// Not time.Duration.String(): "1m0s" and "1h0m0s" are hard to read at a glance
// and change width as they tick, which makes a status line jitter.
func elapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second).Seconds())
	h, mins, sec := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, mins, sec)
	}
	return fmt.Sprintf("%d:%02d", mins, sec)
}

// age renders how long ago something happened, in the coarsest unit that still
// says something useful.
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// spinner picks its frame from the clock rather than from a counter, so every
// spinner on screen turns together and at a steady rate however often the view
// happens to be rebuilt.
//
// The frames and that rule are now comp.Spinner's — tuikit took them from this
// file and says so in its comment. Every is passed explicitly rather than left
// to default, because it has to agree with tickInterval or the spinner jumps
// several frames between redraws and reads as flicker; the two constants being
// equal by coincidence is exactly the arrangement that breaks quietly.
func spinner(now time.Time) string {
	return comp.Spinner{Every: tickInterval}.Frame(now)
}
