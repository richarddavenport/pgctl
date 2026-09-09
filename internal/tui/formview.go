package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/richarddavenport/tuikit/comp"

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
		d.Blocks = append(d.Blocks, comp.Block{Text: a.err.Error()})
		rows += 1 + len(comp.Wrap(a.err.Error(), width))
		d.ValueStyle = &dangerStyle
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

	// One row per field, and that is the whole of it now that no field draws
	// options of its own underneath. The multi-select was the reason this
	// function had to know anything about a component's layout.
	return len(a.fields)
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

// drawFields is the form.
//
// One comp.Form for every field, which it was not until the databases
// multi-select was deleted: that field had to draw its own options directly
// underneath itself, so the field list was split into runs and drawn as several
// forms, each carrying the cursor only if the cursor was inside it — and the
// label column had to be measured across all of them, because left to itself
// each run measured only its own labels and "Upload to storage" started seven
// columns right of "Databases".
func (m *Model) drawFields(c *comp.Canvas, r comp.Rect) {
	a := m.action

	labelWidth := 0
	for _, f := range a.fields {
		labelWidth = max(labelWidth, comp.Width(f.label))
	}

	fields := make([]comp.Field, 0, len(a.fields))
	for _, f := range a.fields {
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
		}
		fields = append(fields, field)
	}

	form := comp.Form{
		Fields:     fields,
		Cursor:     a.cursor,
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
	if a.cursor < len(a.fields) {
		form.Caret = a.fields[a.cursor].caret
	}
	form.Draw(c, r, regModal)
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
	return f.help
}

// actionHintList is the keys on the modal's bottom row, for the stage it is in.
func (m *Model) actionHintList() []comp.Hint {
	a := m.action
	switch a.stage {
	case stagePlanning:
		return []comp.Hint{{Key: "esc", Label: "cancel"}}
	case stagePlan:
		hints := []comp.Hint{{Key: "enter", Label: "apply it"}}
		if a.plan != nil && len(a.plan.Added) == 0 && !a.plan.WholeDatabase {
			hints = append(hints, comp.Hint{Key: "w", Label: "widen to closure"})
		}
		return append(hints,
			comp.Hint{Key: "↑↓", Label: "read"},
			comp.Hint{Key: "esc", Label: "back"},
		)
	}

	hints := []comp.Hint{{Key: "↑↓", Label: "field"}}
	if a.cursor < len(a.fields) {
		switch a.fields[a.cursor].kind {
		case fieldChoice:
			hints = append(hints, comp.Hint{Key: "←→", Label: "change"})
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
// Built from the Plan's fields rather than from Plan.Describe(): the engine's
// description is written for a terminal that has already scrolled — the CLI
// prints it once and it stays on screen — while this is read in a box, so the
// heaviest facts go first and the load order last. Both come from the same
// struct, so neither can claim something the other does not.
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

	// What is destroyed, first. This is the last thing between an operator and
	// a destructive act.
	if p.WholeDatabase {
		wrapped(fmt.Sprintf("DROP AND RECREATE %s on %s",
			p.Snapshot.Database, p.Target.Conn.Name), &dangerStyle)
	} else {
		wrapped(fmt.Sprintf("REPLACE %s in %s on %s",
			plural(len(p.Selection), "table"), p.Snapshot.Database,
			p.Target.Conn.Name), &dangerStyle)
	}
	wrapped(fmt.Sprintf("from %s taken %s, %s of source data",
		p.Snapshot.ID, p.Snapshot.StartedAt.Local().Format("2006-01-02 15:04"),
		engine.HumanBytes(p.Bytes)), &mutedStyle)

	if len(p.Added) > 0 {
		line("", nil)
		wrapped(fmt.Sprintf("widened to include %d more: %s",
			len(p.Added), strings.Join(p.Added, ", ")), &warnStyle)
	}

	if !p.WholeDatabase {
		line("", nil)
		wrapped(fmt.Sprintf("%s dropped and rebuilt, %s rebuilt",
			plural(len(p.DropFKs)+len(p.BlockingFKs), "foreign key"),
			plural(len(p.DropIndexes), "index")), nil)
		if n := len(p.TriggerTables); n > 0 {
			triggers := 0
			for _, t := range p.TriggerTables {
				triggers += len(t.Triggers)
			}
			wrapped(fmt.Sprintf("%s disabled for the load on %s",
				plural(triggers, "user trigger"), plural(n, "table")), nil)
		}

		line("", nil)
		line("LOAD ORDER", &headerStyle)
		for i, layer := range p.Order.Layers {
			wrapped(fmt.Sprintf("%d. %s", i+1, strings.Join(layer, ", ")), nil)
		}
		for _, cycle := range p.Order.Cycles {
			wrapped("ring: "+strings.Join(cycle, ", "), &warnStyle)
		}
	}

	if len(p.MissingFromSnapshot) > 0 {
		line("", nil)
		wrapped("missing from the snapshot: "+
			strings.Join(p.MissingFromSnapshot, ", "), &dangerStyle)
	}
	for _, d := range p.Drift {
		line("", nil)
		wrapped("drift: "+d, &warnStyle)
	}
	for _, w := range p.Warnings {
		line("", nil)
		wrapped("! "+w, &warnStyle)
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
