package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

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
			Text: "no connections declared in " + m.cfg.Source,
		}))
	}
	probe := m.probes[conn.Name]

	switch tab {
	case 1: // Databases
		if probe == nil || !probe.Reachable {
			return facts(m.unreachable(conn.Name, probe))
		}
		rows := make([][]comp.Segment, 0, len(probe.Databases))
		for _, db := range probe.Databases {
			rows = append(rows, cells(text(db.Name), text(engine.HumanBytes(db.Bytes))))
		}
		return paneContent{lines: tableRows(width,
			[]string{"DATABASE", "SIZE"},
			[]comp.Column{{Fill: true}, {Width: 12, Right: true}}, rows)}

	case 2: // Config
		// A flag that is OFF is muted, so a column of them reads as "these two
		// are set" rather than as four equal facts. The style is the Fact's,
		// which is the only place it can be: a Value reaches the canvas as
		// characters, so a pre-styled one draws as nothing at all.
		off := func(b bool) *lipgloss.Style {
			if b {
				return nil
			}
			return &mutedStyle
		}
		cfg := []comp.Fact{
			{Label: "name", Value: conn.Name},
			{Label: "dsn", Value: orDash(conn.DSN), Style: off(conn.DSN != "")},
			{Label: "guarded", Value: yesNo(conn.Guarded), Style: off(conn.Guarded)},
			{Label: "protected", Value: yesNo(conn.Protected), Style: off(conn.Protected)},
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

func (m *Model) viewDatabaseTab(tab, width int) paneContent {
	conn, hasConn := m.selectedConn()
	db, hasDB := m.selectedDatabase()
	if !hasConn || !hasDB {
		return facts(m.detail(comp.Block{Text: "no database selected"}))
	}
	key := liveKey(conn.Name, db.Name)

	switch tab {
	case 1: // Rules
		return facts(m.viewRules(key))
	case 2: // Foreign keys
		return facts(m.detail(comp.Block{
			Text: "Foreign keys are read as part of a plan. Select a set and open " +
				"its Load order tab, or press a to plan an apply.",
		}))
	default: // Tables
		if err := m.liveErr[key]; err != nil {
			d := m.detail(comp.Block{
				Heading: heading("could not read " + db.Name),
				Text:    err.Error(),
			})
			d.HeadingStyle = &dangerStyle
			return facts(d)
		}
		tables := m.liveTable[key]
		if tables == nil {
			return facts(m.detail(comp.Block{Text: spinner(m.now) + " reading " + db.Name + "…"}))
		}
		if len(tables) == 0 {
			return facts(m.detail(comp.Block{Text: "no tables"}))
		}

		rows := make([][]comp.Segment, 0, len(tables))
		var total int64
		for _, t := range tables {
			total += t.Bytes
			rows = append(rows, cells(
				text(t.Name),
				text(engine.HumanBytes(t.Bytes)),
				text(compactCount(t.EstimatedRows)),
				dataMode(m.ruleFor(t.Name)),
			))
		}
		head := make([]comp.Row, 0, 2)
		head = append(head,
			headRow(span(comp.Pad("tables", 14), &mutedStyle),
				span(fmt.Sprint(len(tables)), nil),
				span("   "+comp.Pad("total", 8), &mutedStyle),
				span(engine.HumanBytes(total), nil)),
			blank())
		return paneContent{lines: append(head, tableRows(width,
			[]string{"TABLE", "SIZE", "ROWS", "SNAPSHOT"},
			[]comp.Column{{Fill: true}, {Width: 9, Right: true},
				{Width: 8, Right: true}, {Width: 9}}, rows)...)}
	}
}

// viewRules shows what each rule does and, crucially, whether it matches
// anything — a rule naming a renamed table silently stops filtering it.
// viewRules is the row filters, one block each.
//
// A rule that matches nothing is the interesting case and it is the one worth
// colouring: `hdb_catalog.*log*` once matched hdb_source_catalog_version —
// "cata·log" — and blanking that left Hasura unable to find its metadata. A
// rule silently matching nothing is the same mistake with the sign flipped.
func (m *Model) viewRules(key string) comp.Detail {
	if len(m.cfg.Rules) == 0 {
		return m.detail(comp.Block{
			Text: "No rules declared, so every table is carried whole. A rule says " +
				"how much of a table a snapshot contains: every row, the rows " +
				"matching a predicate, or none at all.",
		})
	}
	tables := m.liveTable[key]

	var blocks []comp.Block
	// The precedence, said once and only where it can matter. Two rules can
	// match one table and only the last of them applies, which is a fact about
	// the config that the list of rules below cannot show — it looks like four
	// independent statements.
	if len(m.cfg.Rules) > 1 {
		blocks = append(blocks, comp.Block{
			Text: "Where two rules match a table, the LAST one applies.",
		})
	}

	for i, rule := range m.cfg.Rules {
		matched, wins := 0, 0
		for _, t := range tables {
			if !config.MatchPattern(rule.Table, t.Name) {
				continue
			}
			matched++
			if m.winningRule(t.Name) == i {
				wins++
			}
		}

		match := comp.Fact{Label: "matches"}
		switch {
		case tables == nil:
			match.Value = "not read yet"
			match.Style = &mutedStyle
		case matched == 0:
			match.Value = "nothing"
			match.Style = &dangerStyle
		case wins == matched:
			match.Value = plural(matched, "table")
		default:
			// Some or all of its matches belong to a later rule. Worth its own
			// wording: "3 tables" beside a rule that decides nothing about two
			// of them is a true number and a misleading one.
			match.Value = fmt.Sprintf("%s, %d of them overridden below",
				plural(matched, "table"), matched-wins)
			match.Style = &warnStyle
			if wins == 0 {
				match.Value = fmt.Sprintf("%s, every one overridden below — "+
					"this rule decides nothing", plural(matched, "table"))
				match.Style = &dangerStyle
			}
		}

		// This rule's OWN data mode, not the effective one for its pattern. The
		// version this replaces asked RuleFor(rule.Table), which resolves a
		// TABLE NAME — handed a pattern it matched the pattern's own text
		// against the other patterns, so `audit.*` reported whatever a rule
		// literally named `audit.*` would have done.
		mode := dataMode(rule)
		facts := []comp.Fact{{Label: "data", Value: mode.Text, Style: mode.Style}, match}
		if rule.Where != "" {
			facts = append(facts, comp.Fact{Label: "where", Value: rule.Where})
		}
		if rule.Why != "" {
			facts = append(facts, comp.Fact{Label: "why", Value: rule.Why, Style: &mutedStyle})
		}
		blocks = append(blocks, comp.Block{Heading: rule.Table, Facts: facts})
	}
	d := m.detail(blocks...)
	// The pattern, not an upper-cased heading: it is a table name, and
	// upper-casing `quotes.quote` makes it look like something else.
	d.HeadingStyle = &accentStyle
	return d
}

// winningRule is the index of the rule that decides a table, or -1.
//
// The engine's rule is "the last match wins", and this is that rule read
// backwards so the interface can say which of several matching rules is the one
// doing anything. config.RuleFor returns the resolved rule and not its
// position, which is the right shape for the engine and not enough for a screen
// that lists all of them.
func (m *Model) winningRule(table string) int {
	winner := -1
	for i, r := range m.cfg.Rules {
		if config.MatchPattern(r.Table, table) {
			winner = i
		}
	}
	return winner
}

func (m *Model) viewSnapshotTab(tab, width int) paneContent {
	entry, ok := m.selectedSnapshot()
	if !ok {
		return facts(m.detail(comp.Block{Text: "no snapshot selected. Press n to take one."}))
	}
	man := entry.Manifest

	switch tab {
	case 1: // Tables
		rows := make([][]comp.Segment, 0, len(man.Tables))
		for _, t := range man.Tables {
			// What the snapshot actually CARRIES for this table, which is the
			// question this column exists to answer: everything, nothing, or the
			// rows a filter admitted. The amber is on the two that are not
			// everything, because a table restored with fewer rows than it had
			// is the surprise worth catching before the apply rather than after.
			carried := comp.Segment{Text: "all", Style: &mutedStyle}
			switch t.Data {
			case config.DataNone:
				carried = comp.Segment{Text: "none", Style: &warnStyle}
			case config.DataFiltered:
				carried = comp.Segment{
					Text:  fmt.Sprintf("%s rows", compactCount(t.Rows)),
					Style: &warnStyle,
				}
			}
			rows = append(rows, cells(
				text(t.Name),
				text(engine.HumanBytes(t.SourceBytes)),
				text(compactCount(t.SourceRows)),
				carried,
			))
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i][0].Text < rows[j][0].Text })
		return paneContent{lines: tableRows(width,
			[]string{"TABLE", "SOURCE SIZE", "ROWS", "CARRIED"},
			[]comp.Column{{Fill: true}, {Width: 12, Right: true},
				{Width: 8, Right: true}, {Width: 12}}, rows)}

	case 2: // Warnings
		if len(man.Warnings) == 0 {
			d := m.detail(comp.Block{Text: "no warnings"})
			d.LabelStyle = &okStyle
			return facts(d)
		}
		// One block of facts labelled "!", not a heading each: a heading names
		// what is UNDER it, and "!" names nothing — it marks the line it is on.
		// As a label it stays on that line, and the amber is the label's.
		warnings := make([]comp.Fact, 0, len(man.Warnings))
		for _, w := range man.Warnings {
			warnings = append(warnings, comp.Fact{Label: "!", Value: w})
		}
		d := m.detail(comp.Block{Facts: warnings})
		d.LabelStyle = &warnStyle
		return facts(d)

	case 3: // Drift
		return facts(m.viewDrift(man))

	default: // Manifest
		taken := comp.Fact{
			Label: "taken",
			Value: man.StartedAt.Local().Format("2006-01-02 15:04") +
				"  " + age(m.now.Sub(man.StartedAt)),
		}
		state := comp.Fact{Label: "took", Value: elapsed(man.FinishedAt.Sub(man.StartedAt))}
		if !man.Complete() {
			// The one fact on this tab that decides whether the snapshot is
			// usable at all, so it is the one that is red.
			state = comp.Fact{
				Label: "state",
				Value: "did not finish — cannot be applied",
				Style: &dangerStyle,
			}
		}

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

		contents := []comp.Fact{
			{Label: "whole", Value: fmt.Sprint(len(man.Tables) - filtered - empty)},
		}
		if filtered > 0 {
			contents = append(contents, comp.Fact{
				Label: "filtered", Value: fmt.Sprint(filtered), Style: &warnStyle,
			})
		}
		if empty > 0 {
			contents = append(contents, comp.Fact{
				Label: "no data", Value: fmt.Sprint(empty), Style: &warnStyle,
			})
		}
		contents = append(contents, comp.Fact{
			Label: "foreign keys", Value: fmt.Sprint(len(man.ForeignKeys)),
		})
		if len(man.Extensions) > 0 {
			names := make([]string, 0, len(man.Extensions))
			for _, e := range man.Extensions {
				names = append(names, e.Name)
			}
			// Not truncated here. comp.Detail wraps a value to the pane, which
			// is what this wanted: the old version cut the list at width-16 and
			// a target that cannot install an extension is exactly the failure
			// the list exists to warn about.
			contents = append(contents, comp.Fact{
				Label: "extensions", Value: strings.Join(names, ", "),
			})
		}

		d := m.detail(
			comp.Block{Facts: []comp.Fact{
				taken,
				state,
				{Label: "size", Value: engine.HumanBytes(man.Bytes)},
				{Label: "tables", Value: fmt.Sprint(len(man.Tables))},
				{Label: "where", Value: entry.Location()},
				{Label: "server", Value: "PostgreSQL " +
					formatServerVersion(man.ServerVersion) + "   pg_dump " + man.PgDumpVersion},
				{Label: "compression", Value: man.Compression +
					fmt.Sprintf("   %d jobs", man.Jobs)},
				{Label: "source size", Value: engine.HumanBytes(source)},
			}},
			comp.Block{Heading: heading("contents"), Facts: contents},
		)
		d.Title = man.ID
		if n := len(man.Warnings); n > 0 {
			d.Blocks = append(d.Blocks, comp.Block{Facts: []comp.Fact{{
				Label: "!",
				Value: fmt.Sprintf("%d warnings — see the Warnings tab", n),
				Style: &warnStyle,
			}}})
		}
		return facts(d)
	}
}

// viewDrift compares the snapshot's source structure with a live environment's,
// which is the question "will this restore cleanly" asked before it is tried.
// viewDrift compares a snapshot's tables against the target's.
//
// The asymmetry is the point, and it is why the two lists are separate blocks
// with separate warnings rather than one diff. A table in the snapshot and not
// on the target is a migration having dropped it. A table on the target and not
// in the snapshot is the dangerous one: a whole-database apply drops it, and a
// set-level apply leaves it holding rows that reference data about to be
// replaced.
func (m *Model) viewDrift(man *snapshot.Manifest) comp.Detail {
	conn, ok := m.selectedConn()
	if !ok {
		return m.detail(comp.Block{Text: "select a connection to compare against"})
	}
	key := liveKey(conn.Name, man.Database)
	tables := m.liveTable[key]
	if tables == nil {
		return m.detail(comp.Block{
			Facts: []comp.Fact{
				{Label: "snapshot", Value: man.ID},
				{Label: "compared to", Value: conn.Name},
			},
		}, comp.Block{
			Text: spinner(m.now) + " reading " + conn.Name + "… open the Databases " +
				"panel's Tables tab to load it.",
		})
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

	blocks := []comp.Block{{Facts: []comp.Fact{
		{Label: "snapshot", Value: man.ID},
		{Label: "compared to", Value: conn.Name},
	}}}

	if len(onlyLive) == 0 && len(onlySnapshot) == 0 {
		d := m.detail(append(blocks, comp.Block{
			Heading: heading("no drift"),
			Text:    "The same tables exist on both sides.",
		})...)
		d.HeadingStyle = &okStyle
		return d
	}

	named := func(names []string) []comp.Fact {
		out := make([]comp.Fact, 0, len(names))
		for _, n := range names {
			out = append(out, comp.Fact{Value: n})
		}
		return out
	}
	if len(onlySnapshot) > 0 {
		blocks = append(blocks, comp.Block{
			Heading: heading(fmt.Sprintf("in the snapshot, not on %s (%d)",
				conn.Name, len(onlySnapshot))),
			Text: "A migration dropped these, or the snapshot is newer.",
		}, comp.Block{Indent: 1, Facts: named(onlySnapshot)})
	}
	if len(onlyLive) > 0 {
		blocks = append(blocks, comp.Block{
			Heading: heading(fmt.Sprintf("on %s, not in the snapshot (%d)",
				conn.Name, len(onlyLive))),
			Text: "A whole-database apply drops these. A set-level apply leaves them, " +
				"holding rows that reference data about to be replaced.",
		}, comp.Block{Indent: 1, Facts: named(onlyLive)})
	}
	return m.detail(blocks...)
}

func (m *Model) viewSetTab(tab, width int) paneContent {
	set, ok := m.selectedSet()
	if !ok {
		return facts(m.detail(comp.Block{
			Text: "no sets declared for this database. A set is a named group of tables " +
				"that move together — declare one in " + m.cfg.Source + ".",
		}))
	}
	conn, _ := m.selectedConn()
	db, _ := m.selectedDatabase()
	info := m.setInfo[setKey(conn.Name, db.Name, set.Name)]

	// The heading every branch of this tab shares: the set's name and what it
	// is for. Rows rather than a builder, so the branches that end in a table
	// can put them above it and the ones that do not can hand them to
	// comp.Detail as a Title.
	head := []comp.Row{{Text: set.Name, Style: &titleStyle, Skip: true}}
	if set.Description != "" {
		head = append(head,
			comp.Row{Text: set.Description, Style: &mutedStyle, Skip: true})
	}
	head = append(head, blank())

	if info == nil || info.loading {
		d := m.detail(comp.Block{
			Text: spinner(m.now) + " resolving against " + conn.Name + "…",
		})
		d.Title, d.Subtitle = set.Name, set.Description
		return facts(d)
	}
	if info.err != nil {
		d := m.detail(comp.Block{
			Heading: heading("could not resolve"),
			Text:    info.err.Error(),
		})
		d.Title, d.Subtitle = set.Name, set.Description
		d.HeadingStyle = &dangerStyle
		return facts(d)
	}

	switch tab {
	case 1: // Closure
		if len(info.added) == 0 {
			d := m.detail(comp.Block{
				Heading: heading("referentially closed"),
				Text: "Every foreign key its tables have points at another table in the " +
					"set, so it can be applied on its own.",
			})
			d.HeadingStyle = &okStyle
			return facts(d)
		}
		added := make([]comp.Fact, 0, len(info.added))
		for _, n := range info.added {
			added = append(added, comp.Fact{Value: n})
		}
		d := m.detail(
			comp.Block{
				Heading: heading("not closed"),
				Text: fmt.Sprintf("%d tables reference %d others. An apply is refused "+
					"unless you widen it to include these.", len(info.members), len(info.added)),
			},
			comp.Block{Heading: heading("would be added by --widen"), Facts: added},
		)
		d.HeadingStyle = &warnStyle
		return facts(d)

	case 2: // Load order
		return facts(m.detail(comp.Block{
			Text: "The load order is computed against the target when you plan an apply, " +
				"because the target's foreign keys are the ones a load has to satisfy. " +
				"Press a to plan one.",
		}))

	default: // Members
		head = append(head,
			headRow(span(comp.Pad("patterns", 14), &mutedStyle),
				span(strings.Join(set.Include, ", "), nil)))
		if len(set.Exclude) > 0 {
			head = append(head,
				headRow(span(comp.Pad("excluding", 14), &mutedStyle),
					span(strings.Join(set.Exclude, ", "), nil)))
		}
		head = append(head,
			headRow(span(comp.Pad("matches", 14), &mutedStyle),
				span(fmt.Sprintf("%d tables on %s", len(info.members), conn.Name), nil)))
		if len(info.added) > 0 {
			head = append(head,
				headRow(span(comp.Pad("closure", 14), &mutedStyle),
					span(fmt.Sprintf("+%d more — see Closure", len(info.added)), &warnStyle)))
		}
		head = append(head, blank(),
			comp.Row{Text: heading("members"), Style: &headerStyle, Skip: true})

		sizes := map[string]int64{}
		for _, t := range m.liveTable[liveKey(conn.Name, db.Name)] {
			sizes[t.Name] = t.Bytes
		}
		rows := make([][]comp.Segment, 0, len(info.members))
		for _, n := range info.members {
			size := comp.Segment{Text: "-", Style: &mutedStyle}
			if b, ok := sizes[n]; ok {
				size = text(engine.HumanBytes(b))
			}
			rows = append(rows, cells(text(n), size))
		}
		return paneContent{lines: append(head, tableRows(width,
			[]string{"TABLE", "SIZE"},
			[]comp.Column{{Fill: true}, {Width: 10, Right: true}}, rows)...)}
	}
}

// orDash is a value or an em dash, in PLAIN text.
//
// Plain because these go into a comp.Fact, and a Fact's Value reaches the
// canvas as characters — a styled string is escape sequences, which the canvas
// draws as nothing and the goldens cannot see because they are colour
// stripped. Fact.Style is where a value's colour lives now.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// yesNo is a flag as a word. Plain, for the reason orDash gives.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
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
