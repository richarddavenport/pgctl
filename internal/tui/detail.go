package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/richarddavenport/tuikit/comp"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// viewConnectionTab is the detail beside the Connections panel.
//
// comp.Detail rather than a strings.Builder and a field() helper. What that
// buys, beyond the colour the canvas was stripping: the label column takes what
// its widest label needs, per block, instead of pgctl's hardcoded fourteen —
// which every label longer than fourteen quietly pushed out of line.
func (m *Model) viewConnectionTab(tab, width int) paneContent {
	conn, ok := m.selectedConn()
	if !ok {
		return facts(m.detail(comp.Block{
			Text: "no environments declared in " + m.cfg.Source,
		}))
	}
	probe := m.probes[conn.Name]

	switch tab {
	case 1: // Databases
		if probe == nil || !probe.Reachable {
			return facts(m.unreachable(conn.Name, probe))
		}
		rows := make([][]string, 0, len(probe.Databases))
		for _, db := range probe.Databases {
			rows = append(rows, []string{db.Name, engine.HumanBytes(db.Bytes)})
		}
		return m.lineContent(renderTable(width, []string{"DATABASE", "SIZE"}, []int{0, 12}, rows))

	case 2: // Config
		cfg := []comp.Fact{
			{Label: "name", Value: conn.Name},
			{Label: "dsn", Value: orDash(conn.DSN)},
			{Label: "guarded", Value: yesNo(conn.Guarded)},
			{Label: "protected", Value: yesNo(conn.Protected)},
		}
		if conn.Jobs > 0 {
			cfg = append(cfg, comp.Fact{Label: "jobs", Value: fmt.Sprint(conn.Jobs)})
		}
		cfg = append(cfg, comp.Fact{Label: "maintenance", Value: conn.MaintenanceDB})

		meaning := comp.Block{Heading: heading("meaning")}
		switch {
		case conn.Protected:
			meaning.Text = "This environment can never be an apply target. " +
				"There is no flag that changes that."
		case conn.Guarded:
			meaning.Text = "An apply here needs the environment's name typed in full."
		default:
			meaning.Text = "An apply here needs only a confirmation."
		}

		return facts(m.detail(
			comp.Block{Facts: cfg},
			comp.Block{
				Heading: heading("how this resolves"),
				Text: "The DSN is handed to libpq unchanged, so ~/.pg_service.conf, " +
					"~/.pgpass and the PG* variables apply exactly as they do to psql. " +
					"pgctl never stores a password.",
			},
			meaning,
		))

	default: // Overview
		d := m.detail()
		d.Title = conn.Name
		switch {
		case conn.Protected:
			d.Subtitle = "protected — never a target"
			d.SubtitleStyle = &dangerStyle
		case conn.Guarded:
			d.Subtitle = "guarded"
			d.SubtitleStyle = &warnStyle
		}

		if probe == nil || !probe.Reachable {
			u := m.unreachable(conn.Name, probe)
			u.Title, u.Subtitle, u.SubtitleStyle = d.Title, d.Subtitle, d.SubtitleStyle
			return facts(u)
		}

		var total int64
		for _, db := range probe.Databases {
			total += db.Bytes
		}
		d.Blocks = []comp.Block{{Facts: []comp.Fact{
			{Label: "host", Value: fmt.Sprintf("%s:%d", probe.Host, probe.Port)},
			{Label: "user", Value: probe.User},
			{Label: "server", Value: "PostgreSQL " + formatServerVersion(probe.ServerVersion)},
			{Label: "databases", Value: fmt.Sprint(len(probe.Databases))},
			{Label: "total size", Value: engine.HumanBytes(total)},
			{Label: "checked", Value: age(m.now.Sub(probe.ProbedAt))},
		}}}

		var newest *engine.Entry
		count := 0
		for _, entry := range m.entries {
			if entry.Manifest.Connection != conn.Name {
				continue
			}
			count++
			if newest == nil || entry.Manifest.StartedAt.After(newest.Manifest.StartedAt) {
				newest = entry
			}
		}
		snaps := comp.Block{Heading: heading("snapshots")}
		if newest == nil {
			snaps.Text = "none — press n to take one"
		} else {
			snaps.Facts = []comp.Fact{
				{Label: "count", Value: fmt.Sprint(count)},
				{Label: "newest", Value: age(m.now.Sub(newest.Manifest.StartedAt)) +
					"  " + newest.Manifest.ID},
			}
		}
		d.Blocks = append(d.Blocks, snaps)
		return facts(d)
	}
}

// heading is how pgctl writes a heading inside a pane: upper case.
//
// A function rather than upper-casing at each call site, because the twelve
// tabs still to convert go through section(), which does the same thing — and
// the two have to agree until the last of them is done, or the pane changes
// style depending on which tab you are looking at.
func heading(s string) string { return strings.ToUpper(s) }

// detail is a comp.Detail wearing pgctl's styles, so no call site restates them.
func (m *Model) detail(blocks ...comp.Block) comp.Detail {
	return comp.Detail{
		Blocks:        blocks,
		TitleStyle:    &titleStyle,
		SubtitleStyle: &mutedStyle,
		HeadingStyle:  &headerStyle,
		LabelStyle:    &mutedStyle,
	}
}

// unreachable explains itself rather than rendering an empty form.
//
// A secrets file that will not decrypt, a firewall rule that does not list this
// address, and a stopped server all look the same from here, so the error text
// is the whole of what pgctl can offer and it is shown in full.
func (m *Model) unreachable(name string, probe *engine.Probe) comp.Detail {
	if probe == nil {
		return m.detail(comp.Block{Text: spinner(m.now) + " probing…"})
	}
	why := comp.Block{Heading: heading("cannot reach " + name)}
	if probe.Err != nil {
		why.Text = probe.Err.Error()
	}
	d := m.detail(why, comp.Block{
		Text: "r retries. A secrets file that will not decrypt, a firewall rule that " +
			"does not list this address, and a stopped server all look the same from here.",
	})
	d.HeadingStyle = &dangerStyle
	return d
}

func (m *Model) viewDatabaseTab(tab, width int) string {
	conn, hasEnv := m.selectedConn()
	db, hasDB := m.selectedDatabase()
	if !hasEnv || !hasDB {
		return mutedStyle.Render("no database selected")
	}
	key := liveKey(conn.Name, db.Name)

	switch tab {
	case 1: // Rules
		return m.viewRules(key)
	case 2: // Foreign keys
		return mutedStyle.Render("Foreign keys are read as part of a plan.\n" +
			"Select a set and open its Load order tab, or press a to plan an apply.")
	default: // Tables
		if err := m.liveErr[key]; err != nil {
			return dangerStyle.Render("could not read "+db.Name) + "\n\n" + wrap(err.Error(), 70)
		}
		tables := m.liveTable[key]
		if tables == nil {
			return mutedStyle.Render(spinner(m.now) + " reading " + db.Name + "…")
		}
		if len(tables) == 0 {
			return mutedStyle.Render("no tables")
		}

		rows := make([][]string, 0, len(tables))
		var total int64
		for _, t := range tables {
			total += t.Bytes
			rows = append(rows, []string{
				t.Name,
				engine.HumanBytes(t.Bytes),
				compactCount(t.EstimatedRows),
				dataMode(m.ruleFor(t.Name)),
			})
		}
		header := field("tables", fmt.Sprint(len(tables))) + "   " +
			field("total", engine.HumanBytes(total)) + "\n\n"
		return header + renderTable(width, []string{"TABLE", "SIZE", "ROWS", "SNAPSHOT"},
			[]int{0, 9, 8, 9}, rows)
	}
}

// viewRules shows what each rule does and, crucially, whether it matches
// anything — a rule naming a renamed table silently stops filtering it.
func (m *Model) viewRules(key string) string {
	if len(m.cfg.Rules) == 0 {
		return mutedStyle.Render("no rules declared.\n\nEvery table is dumped whole.")
	}
	tables := m.liveTable[key]

	var b strings.Builder
	for _, rule := range m.cfg.Rules {
		matched := 0
		for _, t := range tables {
			if config.MatchPattern(rule.Table, t.Name) {
				matched++
			}
		}

		b.WriteString(accentStyle.Render(rule.Table) + "  " + dataMode(m.cfg.RuleFor(rule.Table)))
		switch {
		case tables == nil:
			b.WriteString(mutedStyle.Render("  ?"))
		case matched == 0:
			b.WriteString(dangerStyle.Render("  matches nothing"))
		default:
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  %d tables", matched)))
		}
		b.WriteString("\n")
		if rule.Where != "" {
			b.WriteString("  " + mutedStyle.Render("where ") + rule.Where + "\n")
		}
		if rule.Why != "" {
			b.WriteString("  " + mutedStyle.Render(rule.Why) + "\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) viewSnapshotTab(tab, width int) string {
	entry, ok := m.selectedSnapshot()
	if !ok {
		return mutedStyle.Render("no snapshot selected.\n\nPress n to take one.")
	}
	man := entry.Manifest

	switch tab {
	case 1: // Tables
		rows := make([][]string, 0, len(man.Tables))
		for _, t := range man.Tables {
			detail := mutedStyle.Render("all")
			switch t.Data {
			case config.DataNone:
				detail = warnStyle.Render("none")
			case config.DataFiltered:
				detail = warnStyle.Render(fmt.Sprintf("%s rows", compactCount(t.Rows)))
			}
			rows = append(rows, []string{
				t.Name, engine.HumanBytes(t.SourceBytes), compactCount(t.SourceRows), detail,
			})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
		return renderTable(width, []string{"TABLE", "SOURCE SIZE", "ROWS", "CARRIED"},
			[]int{0, 12, 8, 12}, rows)

	case 2: // Warnings
		if len(man.Warnings) == 0 {
			return okStyle.Render("no warnings")
		}
		var b strings.Builder
		for _, w := range man.Warnings {
			b.WriteString(warnStyle.Render("! ") + wrap(w, 72) + "\n\n")
		}
		return strings.TrimRight(b.String(), "\n")

	case 3: // Drift
		return m.viewDrift(man)

	default: // Manifest
		var b strings.Builder
		b.WriteString(titleStyle.Render(man.ID) + "\n\n")
		b.WriteString(field("taken", man.StartedAt.Local().Format("2006-01-02 15:04")+
			mutedStyle.Render("  "+age(m.now.Sub(man.StartedAt)))) + "\n")
		if man.Complete() {
			b.WriteString(field("took", elapsed(man.FinishedAt.Sub(man.StartedAt))) + "\n")
		} else {
			b.WriteString(field("state", dangerStyle.Render("did not finish — cannot be applied")) + "\n")
		}
		b.WriteString(field("size", engine.HumanBytes(man.Bytes)) + "\n")
		b.WriteString(field("tables", fmt.Sprint(len(man.Tables))) + "\n")
		b.WriteString(field("where", entry.Location()) + "\n")
		b.WriteString(field("server", "PostgreSQL "+formatServerVersion(man.ServerVersion)+
			mutedStyle.Render("   pg_dump "+man.PgDumpVersion)) + "\n")
		b.WriteString(field("compression", man.Compression+mutedStyle.Render(fmt.Sprintf("   %d jobs", man.Jobs))) + "\n")

		var source int64
		filtered, empty := 0, 0
		for _, t := range man.Tables {
			source += t.SourceBytes
			switch t.Data {
			case config.DataFiltered:
				filtered++
			case config.DataNone:
				empty++
			}
		}
		b.WriteString(field("source size", engine.HumanBytes(source)) + "\n")

		b.WriteString(section("contents"))
		b.WriteString(field("whole", fmt.Sprint(len(man.Tables)-filtered-empty)) + "\n")
		if filtered > 0 {
			b.WriteString(field("filtered", warnStyle.Render(fmt.Sprint(filtered))) + "\n")
		}
		if empty > 0 {
			b.WriteString(field("no data", warnStyle.Render(fmt.Sprint(empty))) + "\n")
		}
		b.WriteString(field("foreign keys", fmt.Sprint(len(man.ForeignKeys))) + "\n")
		if len(man.Extensions) > 0 {
			names := make([]string, 0, len(man.Extensions))
			for _, e := range man.Extensions {
				names = append(names, e.Name)
			}
			b.WriteString(field("extensions", truncate(strings.Join(names, ", "), width-16)) + "\n")
		}
		if len(man.Warnings) > 0 {
			b.WriteString("\n" + warnStyle.Render(fmt.Sprintf("! %d warnings — see the Warnings tab", len(man.Warnings))))
		}
		return b.String()
	}
}

// viewDrift compares the snapshot's source structure with a live environment's,
// which is the question "will this restore cleanly" asked before it is tried.
func (m *Model) viewDrift(man *snapshot.Manifest) string {
	conn, ok := m.selectedConn()
	if !ok {
		return mutedStyle.Render("select an environment to compare against")
	}
	key := liveKey(conn.Name, man.Database)
	tables := m.liveTable[key]
	if tables == nil {
		return mutedStyle.Render(fmt.Sprintf(
			"Comparing %s against %s.\n\n%s reading %s…\n\n%s",
			man.ID, conn.Name, spinner(m.now), conn.Name,
			mutedStyle.Render("Open the Databases panel's Tables tab to load it.")))
	}

	inSnapshot := map[string]bool{}
	for _, t := range man.Tables {
		inSnapshot[t.Name] = true
	}
	live := map[string]bool{}
	for _, t := range tables {
		live[t.Name] = true
	}

	var onlyLive, onlySnapshot []string
	for name := range live {
		if !inSnapshot[name] {
			onlyLive = append(onlyLive, name)
		}
	}
	for name := range inSnapshot {
		if !live[name] {
			onlySnapshot = append(onlySnapshot, name)
		}
	}
	sort.Strings(onlyLive)
	sort.Strings(onlySnapshot)

	var b strings.Builder
	b.WriteString(field("snapshot", man.ID) + "\n")
	b.WriteString(field("compared to", conn.Name) + "\n")
	if len(onlyLive) == 0 && len(onlySnapshot) == 0 {
		b.WriteString("\n" + okStyle.Render("The same tables exist on both sides."))
		return b.String()
	}
	if len(onlySnapshot) > 0 {
		b.WriteString(section(fmt.Sprintf("in the snapshot, not on %s (%d)", conn.Name, len(onlySnapshot))))
		b.WriteString(mutedStyle.Render("A migration dropped these, or the snapshot is newer.\n"))
		for _, n := range onlySnapshot {
			b.WriteString("  " + n + "\n")
		}
	}
	if len(onlyLive) > 0 {
		b.WriteString(section(fmt.Sprintf("on %s, not in the snapshot (%d)", conn.Name, len(onlyLive))))
		b.WriteString(warnStyle.Render("A whole-database apply drops these. A set-level apply leaves them,\n" +
			"holding rows that reference data about to be replaced.\n"))
		for _, n := range onlyLive {
			b.WriteString("  " + n + "\n")
		}
	}
	return b.String()
}

func (m *Model) viewSetTab(tab, width int) string {
	set, ok := m.selectedSet()
	if !ok {
		return mutedStyle.Render("no sets declared for this database.\n\n" +
			"A set is a named group of tables that move together — declare one in " + m.cfg.Source + ".")
	}
	conn, _ := m.selectedConn()
	db, _ := m.selectedDatabase()
	info := m.setInfo[setKey(conn.Name, db.Name, set.Name)]

	var b strings.Builder
	b.WriteString(titleStyle.Render(set.Name) + "\n")
	if set.Description != "" {
		b.WriteString(mutedStyle.Render(set.Description) + "\n")
	}
	b.WriteString("\n")

	if info == nil || info.loading {
		return b.String() + mutedStyle.Render(spinner(m.now)+" resolving against "+conn.Name+"…")
	}
	if info.err != nil {
		return b.String() + dangerStyle.Render("could not resolve") + "\n\n" + wrap(info.err.Error(), 70)
	}

	switch tab {
	case 1: // Closure
		if len(info.added) == 0 {
			b.WriteString(okStyle.Render("This set is referentially closed.") + "\n\n")
			b.WriteString(mutedStyle.Render("Every foreign key its tables have points at another table in\n" +
				"the set, so it can be applied on its own."))
			return b.String()
		}
		b.WriteString(warnStyle.Render(fmt.Sprintf(
			"This set is not closed: %d tables reference %d others.", len(info.members), len(info.added))) + "\n")
		b.WriteString(mutedStyle.Render("An apply is refused unless you widen it to include these.") + "\n")
		b.WriteString(section("would be added by --widen"))
		for _, n := range info.added {
			b.WriteString("  " + n + "\n")
		}
		return b.String()

	case 2: // Load order
		return b.String() + mutedStyle.Render(
			"The load order is computed against the target when you plan an apply,\n"+
				"because the target's foreign keys are the ones a load has to satisfy.\n\n") +
			mutedStyle.Render("Press a to plan one.")

	default: // Members
		b.WriteString(field("patterns", strings.Join(set.Include, ", ")) + "\n")
		if len(set.Exclude) > 0 {
			b.WriteString(field("excluding", strings.Join(set.Exclude, ", ")) + "\n")
		}
		b.WriteString(field("matches", fmt.Sprintf("%d tables on %s", len(info.members), conn.Name)) + "\n")
		if len(info.added) > 0 {
			b.WriteString(field("closure", warnStyle.Render(fmt.Sprintf("+%d more — see Closure", len(info.added)))) + "\n")
		}
		b.WriteString(section("members"))

		sizes := map[string]int64{}
		for _, t := range m.liveTable[liveKey(conn.Name, db.Name)] {
			sizes[t.Name] = t.Bytes
		}
		rows := make([][]string, 0, len(info.members))
		for _, n := range info.members {
			size := mutedStyle.Render("-")
			if b, ok := sizes[n]; ok {
				size = engine.HumanBytes(b)
			}
			rows = append(rows, []string{n, size})
		}
		return b.String() + renderTable(width, []string{"TABLE", "SIZE"}, []int{0, 10}, rows)
	}
}

func (m *Model) viewRunTab(width int) string {
	r, ok := m.selectedRun()
	if !ok {
		return mutedStyle.Render("Nothing has run yet.\n\n" +
			"n takes a snapshot, a applies one, m moves between environments.")
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(r.kind) + "  " + mutedStyle.Render(elapsed(r.duration(m.now))))
	switch {
	case r.running:
		b.WriteString("  " + accentStyle.Render(spinner(m.now)+" running"))
	case r.err != nil:
		b.WriteString("  " + dangerStyle.Render("failed"))
	default:
		b.WriteString("  " + okStyle.Render("ok"))
	}
	b.WriteString("\n")
	if r.explain != "" {
		b.WriteString(mutedStyle.Render(wrap(r.explain, width-2)) + "\n")
	}
	b.WriteString("\n")

	for _, ev := range r.log() {
		switch ev.Kind {
		case engine.EventStep:
			b.WriteString(mutedStyle.Render("· ") + ev.Message + "\n")
		case engine.EventTable:
			b.WriteString("  " + ev.Table + " " + mutedStyle.Render(ev.Message) + "\n")
		case engine.EventWarning:
			b.WriteString(warnStyle.Render("! "+wrap(ev.Message, width-2)) + "\n")
		case engine.EventDone:
			b.WriteString(okStyle.Render("✓ "+ev.Message) + "\n")
		case engine.EventFailed:
			b.WriteString(dangerStyle.Render("✗ "+wrap(ev.Message, width-2)) + "\n")
		}
	}
	if p := r.latestProgress(); r.running && p.Message != "" {
		b.WriteString("\n" + accentStyle.Render(spinner(m.now)) + " " + p.Message + "\n")
	}
	if r.err != nil {
		b.WriteString("\n" + dangerStyle.Render(wrap(r.err.Error(), width-2)) + "\n")
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return mutedStyle.Render("—")
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return mutedStyle.Render("no")
}

// compactCount renders 1479891 as "1.5M": exact counts of millions are noise in
// a list whose purpose is comparison.
func compactCount(n int64) string {
	switch {
	case n <= 0:
		return "-"
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1_000_000:
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

// wrap breaks text at word boundaries so a long error is readable in a pane.
func wrap(s string, width int) string {
	// The floor is this package's, not comp's: a pane squeezed to fifteen
	// columns is better read as overflowing than as one word per line.
	if width < 20 {
		width = 20
	}
	// comp.Wrap, because the version this replaces compared BYTES against the
	// width — len(line)+1+len(word) — so every description containing an em
	// dash or an arrow wrapped two or three columns early. This file is full of
	// them.
	return strings.Join(comp.Wrap(s, width), "\n")
}
