package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/richarddavenport/tuikit/comp"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
)

// actionWidth is how wide a modal may be: enough for a load order to read on
// one line where it can, never wider than the terminal.
//
// The plan's description contains lines as long as a list of every table a
// widened selection adds, and an unbounded box drew itself off the side of the
// screen — which the screenshots caught before anyone else did.
func (m *Model) actionWidth() int {
	width := m.screenWidth() - 12
	if width > 104 {
		width = 104
	}
	if width < 32 {
		width = 32
	}
	return width
}

// drawAction centres the open form over the frame.
//
// The keys are INSIDE the box, on its own bottom row. The first version of this
// screen kept them in the frame's footer, outside the modal, so the one place a
// reader looks when a box appears in front of them did not say how to leave it
// or how to commit — "it's hard to tell what I'm supposed to do", which was the
// correct reaction.
//
// There is one help row, always in the same place, describing whichever field
// has the cursor. It used to be an indented line under each field, appearing
// and disappearing as the cursor moved, so the form changed height while being
// read and the hint was somewhere different for every field. The eye learns one
// location instead of tracking one.
func (m *Model) drawAction(c *comp.Canvas, r comp.Rect) {
	a := m.action
	id := comp.Region(regModal)

	// Two columns of border and two of padding, and the height is the content
	// plus the border plus the two reserved rows at the bottom.
	inner := min(m.actionWidth(), r.W-6)
	head := m.actionHead(inner)
	// The head, a blank, the body, then the two reserved rows.
	rows := head.rows + 1 + m.bodyRows(inner) + 2

	box := comp.Rect{W: min(inner+6, r.W), H: min(rows+2, r.H)}
	box.X, box.Y = r.X+(r.W-box.W)/2, r.Y+(r.H-box.H)/2

	// Narrow, not Inset. Pane.Draw already returns the rect INSIDE the border,
	// so insetting it again takes a row off the top and the bottom as well as a
	// column off each side — which is what starved the fields the first time
	// this ran: the box was sized for ten rows of content and handed eight.
	inside := comp.Pane{Border: &panelFocusBorder, Focus: &panelFocusBorder}.
		Draw(c, box, id).Narrow(1)
	if inside.Empty() {
		return
	}

	// The bottom two rows are RESERVED, and the content is drawn into what is
	// left. Computing an exact height instead put the help row on top of the
	// last field the first time this ran — the arithmetic was two out and
	// nothing said so, which is the whole argument for reserving rather than
	// counting.
	// The gap is a BAND, not comp.Layout's Gap: Gap goes between every pair, so
	// asking for one blank after the head silently took two more rows off the
	// body — and the arithmetic above had allowed for one. The symptom was a
	// multi-select two rows short, showing two of three databases with nothing
	// to say the third was there.
	bands := comp.Layout{Constraints: []comp.Constraint{
		comp.Length(head.rows),
		comp.Length(1), // a blank, so the head reads as the head
		comp.Fill(1),
		comp.Length(1), // what the focused field is for
		comp.Length(1), // the keys
	}}.Rows(inside)

	head.detail.Draw(c, bands[0], id)
	m.drawActionBody(c, bands[2], id)

	comp.Bar{Left: []comp.Segment{{Text: m.actionHelp(), Style: &mutedStyle}}}.
		Draw(c, bands[3], id)
	comp.KeyHints(c, bands[4], id, &footerStyle, m.actionHintList()...)

	// A form takes the keyboard, so nothing behind it can be reached and the
	// cursor belongs to whichever field has it.
	_ = a
}

// actionHead is the modal's title, what it is about to do, and what went wrong.
type actionHead struct {
	detail comp.Detail
	rows   int
}

func (m *Model) actionHead(width int) actionHead {
	a := m.action
	d := m.detail()
	d.Title = a.title
	rows := 1

	if a.explain != "" {
		d.Blocks = append(d.Blocks, comp.Block{Text: a.explain})
		rows += 1 + len(comp.Wrap(a.explain, width))
	}
	if a.err != nil {
		// A refusal, in the box that caused it. Its own block, so the blank
		// line before it separates the reason from the description of the thing
		// that was refused.
		//
		// A FACT rather than prose, because comp.Detail has one ValueStyle for
		// every block: setting it to danger painted the explain line red as
		// well, so a form that had refused something described itself as if the
		// description were the problem. Fact.Style paints one value.
		d.Blocks = append(d.Blocks, comp.Block{
			Facts: []comp.Fact{{Value: a.err.Error(), Style: &dangerStyle}},
		})
		rows += 1 + len(comp.Wrap(a.err.Error(), width))
	}
	return actionHead{detail: d, rows: rows}
}

// bodyRows is how many rows the middle of the modal wants.
func (m *Model) bodyRows(width int) int {
	a := m.action
	switch a.stage {
	case stagePlanning:
		return 2
	case stagePlan:
		// The plan, capped: past this it scrolls, because a widened load order
		// is longer than any terminal.
		return min(len(m.planLines(width)), 18) + 1
	}

	rows := 0
	for _, f := range a.fields {
		rows++
		if f.kind == fieldMulti {
			// Its options, drawn underneath. No extra row: the list is
			// NoStatus, because the field's own row carries the count.
			rows += len(f.options)
		}
	}
	return rows
}

// drawActionBody is the middle of the modal: the fields, the wait, or the plan.
func (m *Model) drawActionBody(c *comp.Canvas, r comp.Rect, id comp.ID) {
	switch m.action.stage {
	case stagePlanning:
		// A wait drawn INSIDE the interface rather than instead of it, and it
		// says what it is waiting for: a plan is a read of the target's whole
		// foreign key catalog, and on a large database that is seconds. The
		// elapsed counter appears once the wait is long enough to wonder about,
		// which is the difference between "working" and "hung".
		comp.Waiting{
			Label:  "reading the target's foreign keys",
			Detail: "the plan is computed against the target, because the target's constraints are the ones a load has to satisfy",
			Spinner: comp.Spinner{
				Every: tickInterval,
				Style: &accentStyle,
			},
			Since: m.action.plannedAt,
			// No Style on the label: ordinary text is what the terminal draws
			// without being told, and there is no "foreground" role to name.
			DetailStyle: &mutedStyle,
		}.Draw(c, r, m.now, id)

	case stagePlan:
		lines := m.planLines(r.W)
		m.planView.DrawFunc(c, r, len(lines), func(i int) comp.Line { return lines[i] })

	default:
		m.drawFields(c, r)
	}
}

// drawFields is the form, split wherever a multi-select needs its options drawn
// beneath it.
//
// One label column across every run, measured over ALL the fields: left to
// itself each run measures only its own labels, so a form split for a
// multi-select had its later labels start seven columns right of its earlier
// ones and read as two forms.
func (m *Model) drawFields(c *comp.Canvas, r comp.Rect) {
	a := m.action

	labelWidth := 0
	for _, f := range a.fields {
		labelWidth = max(labelWidth, comp.Width(f.label))
	}
	base := comp.Form{
		Focused:    true,
		Marker:     "▸ ",
		Blank:      "  ",
		LabelWidth: labelWidth,
		Label:      &mutedStyle,
		FocusLabel: &accentStyle,
		Muted:      &mutedStyle,
		Danger:     &dangerStyle,
		CursorFG:   &cursorFG,
		CursorBG:   &cursorBG,
	}

	y := r.Y
	var run []comp.Field
	runFrom := 0
	flush := func(upto int) {
		if len(run) == 0 {
			run, runFrom = nil, upto
			return
		}
		form := base
		form.Fields = run
		form.Cursor = a.cursor - runFrom
		form.Focused = form.Cursor >= 0 && form.Cursor < len(run)
		if form.Focused && a.cursor < len(a.fields) {
			// A multi-select's row is NOT drawn as focused, even when it is: the
			// marker and the accent belong to the option row below, which is
			// where the keys act. Two markers on screen and a reader has to work
			// out which one their arrows are moving — the first version of this
			// had both, and that ambiguity is what made the databases version
			// hard to use for the same reason in reverse.
			if a.fields[a.cursor].kind == fieldMulti {
				form.Focused = false
			}
			form.Caret = a.fields[a.cursor].caret
		}
		if h := min(len(run), r.Bottom()-y+1); h > 0 {
			form.Draw(c, comp.Rect{X: r.X, Y: y, W: r.W, H: h}, regModal)
			y += h
		}
		run, runFrom = nil, upto
	}

	for i, f := range a.fields {
		field := comp.Field{Label: f.label, Disabled: f.disabled}
		switch f.kind {
		case fieldChoice:
			// One choice, pre-composed, rather than the whole list.
			//
			// comp.FieldChoice draws EVERY choice inline and mutes the ones not
			// selected — a segmented control, which is right for short options
			// and wrong here: pgctl's choices are sentences ("whole database —
			// drop and recreate it", "set claims — claims and everything a
			// claim points at"), and three of those side by side is two hundred
			// columns. tuikit issue 49.
			field.Kind = comp.FieldChoice
			field.Choices, field.Choice = []string{m.choiceLabel(f)}, 0
		case fieldToggle:
			field.Kind, field.On = comp.FieldToggle, f.on
		case fieldText:
			field.Kind, field.Text = comp.FieldText, f.text
			field.Placeholder = "nothing yet"
		case fieldConfirm:
			// Must is the type-the-name-to-confirm phrase, and the form knows
			// whether it has been satisfied — so this is a field you can see
			// while choosing rather than a prompt sprung after the plan.
			field.Kind, field.Text = comp.FieldText, f.text
			field.Placeholder = "type it to confirm"
			if target := a.value("target"); target != "" {
				field.Must = target
			}
		case fieldMulti:
			// The field's row is the label and the count; its options are drawn
			// underneath it as a list, because that is what a set of choices
			// looks like. The row carries NO cursor marker even when focused —
			// the marker is on the option, which is where the keys act, and two
			// markers is how a reader loses track of which one they are moving.
			field.Kind = comp.FieldChoice
			field.Choices, field.Choice = []string{m.multiSummary(f)}, 0
			run = append(run, field)
			flush(i + 1)

			rows := m.multiRows(f)
			if h := min(len(rows), r.Bottom()-y+1); h >= 1 {
				focused := i == a.cursor
				m.multiList.Select(f.choice)
				m.multiList.Focused = focused
				// The marker only where the keys act. comp.List draws it
				// against its cursor row whether or not the list is focused, so
				// a form with two multi-selects showed two ▸ and a reader had
				// to guess which one their arrows were moving — the same
				// ambiguity as the field-row marker, one level along.
				m.multiList.Marker = "  "
				if focused {
					m.multiList.Marker = "▸ "
				}
				m.multiList.Draw(c, comp.Rect{X: r.X, Y: y, W: r.W, H: h}, rows)
				y += h
			}
			continue
		}
		run = append(run, field)
	}
	flush(len(a.fields))
}

// multiSummary is a multi-select as one line, for the field's own row.
func (m *Model) multiSummary(f formField) string {
	on := 0
	for i := range f.options {
		if f.selected[i] {
			on++
		}
	}
	switch {
	case on == 0:
		return "nothing chosen yet"
	case on == len(f.options):
		return fmt.Sprintf("all %d", on)
	default:
		return fmt.Sprintf("%d of %d", on, len(f.options))
	}
}

// multiRows are the options under a multi-select.
//
// ● in, ○ out, as the row's STATUS — a fixed column whose colour survives the
// selection highlight, because whether an option is chosen and where the cursor
// is are two different facts and the highlight must not eat one of them. The
// same in/out pair the Connections panel marks reachability with: one question
// shape should not have two glyphs.
//
// The hint travels with its option rather than only appearing in the help row,
// because the whole point of a list is that you can compare the choices without
// visiting each one.
func (m *Model) multiRows(f formField) []comp.Row {
	out := make([]comp.Row, 0, len(f.options))
	for i, opt := range f.options {
		mark, style := "○", &mutedStyle
		if f.selected[i] {
			mark, style = "●", &okStyle
		}
		row := comp.Row{
			Key:         opt,
			Status:      mark,
			StatusStyle: style,
			Depth:       1,
			Text:        opt,
		}
		if i < len(f.labels) && f.labels[i] != "" {
			row.Spans = []comp.Segment{
				{Text: comp.Pad(opt, 14) + "  "},
				{Text: f.labels[i], Style: &mutedStyle},
			}
			row.Text = comp.Pad(opt, 14) + "  " + f.labels[i]
		}
		out = append(out, row)
	}
	return out
}

// choiceLabel is the selected choice, with the chevrons that say it cycles and
// the position that says how far through it is.
func (m *Model) choiceLabel(f formField) string {
	label := ""
	if f.choice < len(f.labels) {
		label = f.labels[f.choice]
	}
	return fmt.Sprintf("‹ %s ›  (%d/%d)", label, f.choice+1, len(f.labels))
}

// actionHelp is what the focused field is for, or why a disabled one is not.
func (m *Model) actionHelp() string {
	a := m.action
	switch a.stage {
	case stagePlanning:
		return "esc leaves; the plan lands in the void"
	case stagePlan:
		return "read it, then confirm. Nothing has been touched yet."
	}
	if a.cursor >= len(a.fields) {
		return ""
	}
	f := a.fields[a.cursor]
	if f.disabled && f.reason != "" {
		return f.reason
	}
	if f.kind == fieldMulti {
		// What the current selection MEANS, per field, in the row that would
		// otherwise repeat the keys. The keys are on the hint line already.
		if f.key == "destinations" {
			return m.destinationConsequence()
		}
		return databaseConsequence(m.chosenDatabases(), len(f.options))
	}
	return f.help
}

// databaseConsequence is what the chosen databases add up to.
//
// It says the SIZE of the thing, not the count, because that is the fact a
// reader is deciding on: `product-development` is 71 GB on the real server and
// the other five together are under 4. "3 of 6" says nothing about the twenty
// minutes.
func databaseConsequence(chosen []string, total int) string {
	switch {
	case len(chosen) == 0:
		return "nothing chosen yet — enter will refuse"
	case len(chosen) == total:
		return "every database on this connection, in one snapshot set"
	case len(chosen) == 1:
		return chosen[0] + " alone"
	default:
		return strings.Join(chosen, ", ") + " — one snapshot set, one timestamp"
	}
}

// destinationConsequence is what the current selection MEANS, in the row that
// would otherwise repeat the keys.
//
// The keys are on the hint line already; what a reader cannot get from the rows
// is that unchoosing local does not merely skip an upload, it deletes the copy
// on this machine once the upload succeeds. That is the one consequence in this
// form worth a sentence, and it changes as the cursor never moves — so it
// belongs here rather than beside an option.
func (m *Model) destinationConsequence() string {
	chosen := m.chosenDestinations()
	local := false
	remotes := 0
	for _, name := range chosen {
		if name == config.LocalStorage {
			local = true
			continue
		}
		remotes++
	}

	switch {
	case len(chosen) == 0:
		return "nothing chosen yet — enter will refuse"
	case local && remotes > 0:
		return "kept on this machine and uploaded"
	case local:
		return "stays on this machine; nothing is uploaded"
	default:
		return "uploaded, and then the copy here is deleted"
	}
}

// actionHintList is the keys on the modal's bottom row, for the stage it is in.
func (m *Model) actionHintList() []comp.Hint {
	a := m.action
	switch a.stage {
	case stagePlanning:
		return []comp.Hint{{Key: "esc", Label: "cancel"}}
	case stagePlan:
		hints := []comp.Hint{{Key: "enter", Label: "apply it"}}
		// Widening is offered when some plan could use it: a whole-database
		// apply has nothing to widen, and one already widened has nothing left.
		if a.plan != nil && a.plan.CanWiden() {
			hints = append(hints, comp.Hint{Key: "w", Label: "widen to closure"})
		}
		return append(hints,
			comp.Hint{Key: "↑↓", Label: "read"},
			comp.Hint{Key: "esc", Label: "back"},
		)
	}

	hints := []comp.Hint{{Key: "↑↓", Label: "field"}}
	if len(a.fields) == 1 {
		hints = nil
	}
	if a.cursor < len(a.fields) {
		switch a.fields[a.cursor].kind {
		case fieldChoice:
			hints = append(hints, comp.Hint{Key: "←→", Label: "change"})
		case fieldMulti:
			hints = append([]comp.Hint{{Key: "↑↓", Label: "move"}},
				comp.Hint{Key: "space", Label: "choose"},
				comp.Hint{Key: "a", Label: "all"})
			if len(a.fields) > 1 {
				hints = append(hints, comp.Hint{Key: "tab", Label: "field"})
			}
		case fieldToggle:
			hints = append(hints, comp.Hint{Key: "space", Label: "toggle"})
		case fieldText, fieldConfirm:
			hints = append(hints, comp.Hint{Label: "type"})
		}
	}
	return append(hints,
		comp.Hint{Key: "enter", Label: m.submitLabel()},
		comp.Hint{Key: "esc", Label: "cancel"},
	)
}

// submitLabel says what enter will do, in the words of the thing it does.
//
// "confirm" says nothing about consequence, and this is the key that starts a
// restore.
func (m *Model) submitLabel() string {
	switch m.action.kind {
	case actionSnapshot:
		return "take it"
	case actionApply:
		return "plan it"
	case actionMove:
		return "move it"
	case actionPrune:
		return "prune"
	case actionDelete:
		return "delete it"
	}
	return "go"
}

// planLines is the plan as a document.
//
// Built from the RunPlan's fields rather than from Plan.Describe(): the engine's
// description is written for a terminal that has already scrolled — the CLI
// prints it once and it stays on screen — while this is read in a box, so the
// heaviest facts go first and the load orders last. Both come from the same
// structs, so neither can claim something the other does not.
func (m *Model) planLines(width int) []comp.Line {
	p := m.action.plan
	if p == nil {
		return nil
	}

	var out []comp.Line
	line := func(text string, style *lipgloss.Style) {
		out = append(out, comp.Line{Text: text, Style: style})
	}
	wrapped := func(text string, style *lipgloss.Style) {
		for _, l := range comp.Wrap(text, width) {
			line(l, style)
		}
	}

	// The refusals FIRST, before what will happen, because they are the part
	// that changes what a reader is about to confirm: five databases restoring
	// and one refused is a decision, and finding the refusal below two hundred
	// lines of load order is not one.
	if len(p.Refusals) > 0 {
		wrapped(fmt.Sprintf("REFUSED, and skipped: %s",
			plural(len(p.Refusals), "database")), &dangerStyle)
		for _, r := range p.Refusals {
			wrapped("  "+r.Database+" — "+r.Reason, &warnStyle)
		}
		line("", nil)
	}

	// What is destroyed, and how much of it. This is the last thing between an
	// operator and a destructive act.
	wrapped(fmt.Sprintf("REPLACE %s in %s on %s",
		plural(p.Tables(), "table"), plural(len(p.Plans), "database"), p.Target),
		&dangerStyle)
	wrapped(fmt.Sprintf("from %s taken %s, %s of source data",
		p.Run.ID, p.Run.At.Local().Format("2006-01-02 15:04"),
		engine.HumanBytes(p.Bytes())), &mutedStyle)

	for _, one := range p.Plans {
		line("", nil)
		what := plural(len(one.Selection), "table")
		if one.WholeDatabase {
			what = "the whole database"
		}
		wrapped(fmt.Sprintf("%s — %s, %s", one.Snapshot.Database, what,
			engine.HumanBytes(one.Bytes)), &headerStyle)

		if len(one.Added) > 0 {
			wrapped(fmt.Sprintf("  widened to include %d more: %s",
				len(one.Added), strings.Join(one.Added, ", ")), &warnStyle)
		}
		if !one.WholeDatabase {
			wrapped(fmt.Sprintf("  %s dropped and rebuilt, %s rebuilt",
				plural(len(one.DropFKs)+len(one.BlockingFKs), "foreign key"),
				plural(len(one.DropIndexes), "index")), nil)
			if n := len(one.TriggerTables); n > 0 {
				triggers := 0
				for _, t := range one.TriggerTables {
					triggers += len(t.Triggers)
				}
				wrapped(fmt.Sprintf("  %s disabled for the load on %s",
					plural(triggers, "user trigger"), plural(n, "table")), nil)
			}
			for i, layer := range one.Order.Layers {
				wrapped(fmt.Sprintf("  %d. %s", i+1, strings.Join(layer, ", ")), nil)
			}
			for _, cycle := range one.Order.Cycles {
				wrapped("  ring: "+strings.Join(cycle, ", "), &warnStyle)
			}
		}
		if len(one.MissingFromSnapshot) > 0 {
			wrapped("  missing from the snapshot: "+
				strings.Join(one.MissingFromSnapshot, ", "), &dangerStyle)
		}
		for _, d := range one.Drift {
			wrapped("  drift: "+d, &warnStyle)
		}
		for _, w := range one.Warnings {
			wrapped("  ! "+w, &warnStyle)
		}
	}
	return out
}

// drawLeaving is the question in front of q while an operation is in flight.
//
// comp.Confirm bounds itself to the frame by construction, which is what a
// question about abandoning a restore should do: there is no arithmetic here to
// get wrong, and no version of it that draws off the side of the screen.
func (m *Model) drawLeaving(c *comp.Canvas) {
	kind := "the operation"
	if m.active != nil {
		kind = m.active.kind
	}
	comp.Confirm{
		Title:  "Leave while " + kind + " is running?",
		Danger: true,
		Body: "It is cancelled rather than abandoned, so the engine's onFailure " +
			"and postApply hooks still run — they are what bring an environment " +
			"back up. Anything already written stays written.",
		Hints: []comp.Hint{
			{Key: "y", Label: "cancel it and leave"},
			{Key: "esc", Label: "stay"},
		},
		Margin:      2,
		Border:      &panelFocusBorder,
		TitleStyle:  &titleStyle,
		DangerStyle: &dangerStyle,
		HintStyle:   &footerStyle,
	}.Draw(c, comp.Region(regConfirmBox))
}

// plural is a count and its noun, agreeing.
//
// "1 tables" is the tell that a number came from len() and nobody read the
// sentence afterwards, and this sentence is the one an operator reads before
// replacing a database. Indexes rather than indices, since it is a Postgres
// index and that is what Postgres calls them.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	if noun == "index" {
		return fmt.Sprintf("%d indexes", n)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
