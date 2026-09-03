package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/richarddavenport/tuikit/comp"
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
// A layout rather than a rendered string, and that is what fixes the complaint
// it was drawn to fix: "it's hard to tell what I'm supposed to do." The old
// version was a strings.Builder blitted into a box, and three things followed
// from that which no amount of wording could fix.
//
// The keys were in the SCREEN's footer, outside the modal, so the one place a
// reader looks when a box appears in front of them did not say how to leave it
// or how to commit. They are inside now, on the bottom row of the box.
//
// A field's help was an indented line UNDER the field, appearing and
// disappearing as the cursor moved, so the form changed height while being
// read and the hint was in a different place for every field. There is one help
// row now, always in the same place, describing whichever field has the cursor.
// The eye learns one location instead of tracking one.
//
// And the fields are comp.Form, which is pgctl's own viewForm extracted — so
// this is mostly giving code back and taking the parts swarmctl contributed:
// a placeholder, so an unanswered field looks unanswered rather than broken;
// Disabled that still shows its value, because a choice you cannot change is
// one you may still need to read; and Must, the type-the-name-to-confirm
// phrase, which pgctl had as a separate prompt after the plan.
func (m *Model) drawAction(c *comp.Canvas, r comp.Rect) {
	id := comp.Region(regModal)

	head := m.actionHead()
	help, hints := m.actionHelp(), m.actionHints()

	// The form is one of three stages. The other two — waiting for a plan, and
	// reading the plan that came back — are still prose, so they arrive as
	// rows and the fields are empty. They are what the ansi.Strip in this file
	// is still for, and TestOnlyTheOverlaysStillStripColour is what keeps that
	// from becoming permanent.
	var blocks []formBlock
	var body []comp.Row
	switch m.action.stage {
	case stagePlanning:
		body = strip(spinner(m.now) + " reading the target's foreign keys…\n\n" +
			"The plan is computed against the target, because the target's " +
			"constraints are the ones a load has to satisfy.")
	case stagePlan:
		body = strip(m.viewPlanPreview())
	default:
		blocks = m.actionForm()
	}

	// title and explain, a blank, the body, a blank, help, hints.
	rows := len(head) + 1 + len(body) + 2 + 1
	for _, b := range blocks {
		rows += b.height()
	}
	box := comp.Rect{W: min(m.actionWidth()+6, r.W), H: min(rows+2, r.H)}
	box.X, box.Y = r.X+(r.W-box.W)/2, r.Y+(r.H-box.H)/2

	// Narrow, not Inset. Pane.Draw already returns the rect INSIDE the border,
	// so insetting it again takes a row off the top and the bottom as well as
	// a column off each side — which is what starved the fields the first time
	// this ran: the box was sized for ten rows of content and handed eight.
	// Narrow pads the sides and leaves the height alone, which is what the
	// panels do for the same reason.
	inside := comp.Pane{Border: &panelFocusBorder, Focus: &panelFocusBorder}.
		Draw(c, box, id).Narrow(1)
	if inside.Empty() {
		return
	}
	c = c.Clip(inside)

	// The bottom two rows are RESERVED, and the content is drawn into what is
	// left. Computing an exact height instead put the help row on top of the
	// last field the first time this ran — the arithmetic was two out and
	// nothing said so, which is the whole argument for reserving rather than
	// counting. comp.Layout would do this if the content were not three
	// different shapes.
	content := comp.Rect{X: inside.X, Y: inside.Y, W: inside.W, H: max(0, inside.H-2)}

	y := content.Y
	draw := func(rows []comp.Row) {
		for _, line := range rows {
			if y > content.Bottom() {
				return
			}
			c.Text(content.X, y, comp.Truncate(line.Text, content.W), line.Style, id)
			y++
		}
	}

	draw(head)
	y++
	for _, b := range blocks {
		if b.rows != nil {
			draw(b.rows)
			continue
		}
		if h := len(b.form.Fields); h > 0 && y+h-1 <= content.Bottom() {
			b.form.Draw(c, comp.Rect{X: content.X, Y: y, W: content.W, H: h}, regModal)
			y += h
		}
	}
	draw(body)

	// The help for the focused field, then the keys. Pinned to the bottom
	// rather than following the content, so they do not move as the form does
	// — which is the half of the complaint that wording could not fix.
	c.Text(inside.X, inside.Bottom()-1, comp.Truncate(help, inside.W), &mutedStyle, id)
	c.Text(inside.X, inside.Bottom(), comp.Truncate(hints, inside.W), &footerStyle, id)
}

// strip turns a still-styled string into rows.
//
// The last of the scaffolding, and the only reason ansi.Strip is still imported
// here: the canvas draws clusters into cells, so an escape sequence handed to
// it is text. The plan preview and the planning wait are the two screens left
// that build one, and they go the way the pane's fourteen tabs went.
func strip(text string) []comp.Row {
	lines := strings.Split(text, "\n")
	out := make([]comp.Row, 0, len(lines))
	for _, line := range lines {
		out = append(out, comp.Row{Text: ansi.Strip(line)})
	}
	return out
}

// actionHead is the modal's title and what it is about to do.
func (m *Model) actionHead() []comp.Row {
	a := m.action
	head := []comp.Row{{Text: a.title, Style: &titleStyle}}
	if a.explain != "" {
		for _, line := range comp.Wrap(a.explain, m.actionWidth()) {
			head = append(head, comp.Row{Text: line, Style: &mutedStyle})
		}
	}
	if a.err != nil {
		for _, line := range comp.Wrap(a.err.Error(), m.actionWidth()) {
			head = append(head, comp.Row{Text: line, Style: &dangerStyle})
		}
	}
	return head
}

// formBlock is either a run of comp.Form fields or rows pgctl draws itself.
//
// It exists for the multi-select, which is the gap: comp.Field is text, choice
// or toggle, and pgctl's snapshot form picks ANY NUMBER of databases. Reported
// to tuikit with this code.
//
// Blocks rather than "the form, then the extra rows", because the options have
// to sit UNDER the field they belong to. Appended after the form they landed
// below "Upload to storage", three rows away from the "1 of 3" they explain,
// which reads as a list belonging to the wrong question.
type formBlock struct {
	form comp.Form
	rows []comp.Row
}

func (b formBlock) height() int {
	if b.rows != nil {
		return len(b.rows)
	}
	return len(b.form.Fields)
}

// actionForm maps pgctl's fields onto comp.Form, splitting the run wherever a
// multi-select needs its options drawn beneath it.
func (m *Model) actionForm() []formBlock {
	a := m.action
	form := comp.Form{
		Cursor:     a.cursor,
		Focused:    true,
		Marker:     "▸ ",
		Blank:      "  ",
		Label:      &mutedStyle,
		FocusLabel: &accentStyle,
		Muted:      &mutedStyle,
		Danger:     &dangerStyle,
	}
	// One label column across every run, measured over ALL the fields.
	//
	// Left to itself each run measures only its own labels, so splitting the
	// form for a multi-select made "Upload to storage" start seven columns
	// right of "Databases" — a form that looks like two forms.
	for _, f := range a.fields {
		form.LabelWidth = max(form.LabelWidth, comp.Width(f.label))
	}

	// base is the styles, marker and label width every run shares; each run
	// carries the cursor only if the cursor is inside it.
	base := form
	var blocks []formBlock
	first := 0
	flush := func(upto int, rows []comp.Row) {
		run := base
		run.Fields = form.Fields[first:upto]
		run.Cursor = a.cursor - first
		run.Focused = run.Cursor >= 0 && run.Cursor < len(run.Fields)
		if len(run.Fields) > 0 {
			blocks = append(blocks, formBlock{form: run})
		}
		if rows != nil {
			blocks = append(blocks, formBlock{rows: rows})
		}
		first = upto
	}

	for _, f := range a.fields {
		field := comp.Field{Label: f.label, Disabled: f.disabled}
		switch f.kind {
		case fieldChoice:
			// One choice, pre-composed, rather than the whole list.
			//
			// comp.FieldChoice draws EVERY choice inline and mutes the ones
			// not selected — a segmented control, which is right for
			// swarmctl's short options and wrong here: pgctl's choices are
			// sentences ("whole database — drop and recreate it", "set claims
			// — claims and everything a claim points at"), and three of them
			// side by side is two hundred columns. Reported to tuikit with
			// this code.
			//
			// The chevrons say ← → cycles it, which is a fact about the keymap
			// that no styling can state, and the position says how many there
			// are to cycle through.
			field.Kind, field.Choice = comp.FieldChoice, 0
			field.Choices = []string{m.choiceLabel(f)}
		case fieldToggle:
			field.Kind, field.On = comp.FieldToggle, f.on
		case fieldText:
			field.Kind, field.Text = comp.FieldText, f.text
			field.Placeholder = "nothing yet"
		case fieldConfirm:
			// Must is swarmctl's type-the-name-to-confirm, and the form knows
			// whether it has been satisfied — so this stops being a prompt
			// after the plan and becomes a field you can see before it.
			field.Kind, field.Text = comp.FieldText, f.text
			field.Must, field.Placeholder = f.key, "type it to confirm"
		case fieldMulti:
			field.Kind, field.Choice = comp.FieldChoice, 0
			field.Choices = []string{m.multiSummary(f)}
			form.Fields = append(form.Fields, field)
			// Its options go directly underneath, so the run ends here.
			flush(len(form.Fields), m.multiRows(f))
			continue
		}
		form.Fields = append(form.Fields, field)
	}
	flush(len(form.Fields), nil)
	return blocks
}

// actionHints is the keys, on the bottom row of the modal.
//
// It is actionFooter's list, moved inside the box. The screen's footer used to
// carry these, which put the answer to "how do I get out of this" outside the
// thing a reader was looking at.
func (m *Model) actionHints() string {
	return comp.Hints(m.actionHintList()...)
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
		return "none of " + fmt.Sprint(len(f.options))
	case on == len(f.options):
		return "all " + fmt.Sprint(on)
	default:
		return fmt.Sprintf("%d of %d", on, len(f.options))
	}
}

// multiRows are the options under a multi-select, indented under its label.
func (m *Model) multiRows(f formField) []comp.Row {
	out := make([]comp.Row, 0, len(f.options))
	for i, opt := range f.options {
		// The same filled/hollow pair the Connections panel marks reachability
		// with. One question shape — is this one in or out — should not have
		// two glyphs.
		mark, style := "○", &mutedStyle
		if f.selected[i] {
			mark, style = "●", &okStyle
		}
		out = append(out, comp.Row{Text: "      " + mark + " " + opt, Style: style})
	}
	return out
}

// actionHelp is what the focused field is for, or why a disabled one is not.
func (m *Model) actionHelp() string {
	a := m.action
	if a.cursor >= len(a.fields) {
		return ""
	}
	f := a.fields[a.cursor]
	if f.disabled && f.reason != "" {
		return f.reason
	}
	return f.help
}

// drawOverlay centres a bordered box of lines over r.
//
// Shared by the modal and the help, because they differ only in what is in them
// and what colour the edge is — and because the arithmetic for "centre a box
// and do not let it leave the screen" is the kind that gets written twice and
// then diverges.
func (m *Model) drawOverlay(
	c *comp.Canvas, r comp.Rect, id comp.ID, width int, lines []string, edge *lipgloss.Style,
) {
	// Two columns of border and two of padding on each side.
	box := comp.Rect{W: min(width+6, r.W), H: min(len(lines)+4, r.H)}
	box.X = r.X + (r.W-box.W)/2
	box.Y = r.Y + (r.H-box.H)/2

	inside := comp.Pane{Border: edge, Focus: edge}.Draw(c, box, id).Inset(1)

	// Clipped to the box, so a line longer than the modal is cut by the canvas
	// rather than drawn over the border and out the other side. The form's
	// field rows used to be wrapped by lipgloss's Width; nothing wraps them
	// now, and at eighty columns two of them wrote straight through the right
	// edge. Truncating as well as clipping, so the reader is told the line was
	// cut instead of finding out by its ending mid-word.
	c = c.Clip(inside)
	for i, line := range lines {
		if i >= inside.H {
			break
		}
		line = comp.Truncate(line, inside.W)
		// ansi.Strip for the same reason the detail pane does it, and it goes
		// the same way: the form and the key list still build styled strings,
		// and the canvas draws cells rather than replaying escape sequences.
		// See drawPane.
		c.Text(inside.X, inside.Y+i, ansi.Strip(line), nil, id)
	}
}

// viewForm renders every field at once, so the operator can see what they have
// chosen rather than remembering it.
func (m *Model) viewForm() string {
	a := m.action
	var b strings.Builder

	for i, f := range a.fields {
		focused := i == a.cursor
		marker := "  "
		if focused {
			marker = accentStyle.Render("▸ ")
		}

		label := fmt.Sprintf("%-22s", f.label)
		if f.disabled {
			label = mutedStyle.Render(label)
		} else if focused {
			label = accentStyle.Render(label)
		}
		b.WriteString(marker + label)

		switch f.kind {
		case fieldChoice:
			b.WriteString(m.renderChoice(f, focused))
		case fieldMulti:
			b.WriteString(m.renderMulti(f, focused))
		case fieldToggle:
			b.WriteString(renderToggle(f))
		case fieldText:
			text := f.text
			if text == "" {
				text = mutedStyle.Render("(none)")
			}
			if focused && !f.disabled {
				text += "▏"
			}
			b.WriteString(text)
		}
		b.WriteString("\n")

		switch {
		case f.disabled && f.reason != "":
			b.WriteString("    " + mutedStyle.Render(f.reason) + "\n")
		case focused && f.help != "":
			b.WriteString("    " + mutedStyle.Render(f.help) + "\n")
		}
	}
	return b.String()
}

func (m *Model) renderChoice(f formField, focused bool) string {
	label := ""
	if f.choice < len(f.labels) {
		label = f.labels[f.choice]
	} else if f.choice < len(f.options) {
		label = f.options[f.choice]
	}

	// The position is shown because a list of choices you can only step through
	// is one you cannot tell the length of.
	position := mutedStyle.Render(fmt.Sprintf("  (%d/%d)", f.choice+1, len(f.options)))
	if focused && !f.disabled {
		return accentStyle.Render("‹ ") + label + accentStyle.Render(" ›") + position
	}
	return "  " + label + position
}

func (m *Model) renderMulti(f formField, focused bool) string {
	parts := make([]string, 0, len(f.options))
	for i, opt := range f.options {
		// The same filled/hollow pair the Connections panel marks reachability
		// with, rather than a ballot box. One question shape — is this one in or
		// out — should not have two glyphs, and ☐/☑ are the characters a
		// terminal font is most likely to draw as a replacement box or, worse,
		// as double-width emoji that shift the column.
		box := "○"
		if f.selected[i] {
			box = okStyle.Render("●")
		}
		item := box + " " + opt
		if focused && i == f.choice {
			item = selectedStyle.Render(" " + box + " " + opt + " ")
		}
		parts = append(parts, item)
	}
	chosen := 0
	for _, on := range f.selected {
		if on {
			chosen++
		}
	}
	return strings.Join(parts, "  ") + mutedStyle.Render(fmt.Sprintf("   %d of %d", chosen, len(f.options)))
}

func renderToggle(f formField) string {
	if f.disabled {
		return mutedStyle.Render("—")
	}
	if f.on {
		return okStyle.Render("on")
	}
	return mutedStyle.Render("off")
}

// viewPlanPreview renders the plan. This is the last thing between an operator
// and a destructive act, so it leads with what is destroyed and never with what
// is convenient.
func (m *Model) viewPlanPreview() string {
	p := m.action.plan
	var b strings.Builder
	b.WriteString(wrapIndented(p.plan.Describe(), m.actionWidth()))

	if p.needsName {
		b.WriteString("\n" + dangerStyle.Render(
			fmt.Sprintf("%s is guarded. Type its name to continue: ", p.plan.Target.Conn.Name)) +
			p.typed + "▏")
	}
	return b.String()
}

// actionFooter is the key hint for whatever the form is showing.
// actionHintList is what acts on the modal right now, as hints.
//
// A []comp.Hint rather than a joined string, because the separator lives in the
// chrome once and because a Hint is the type a context menu is built from —
// tuikit's rule that the keyboard path and the pointer path are ONE list.
func (m *Model) actionHintList() []comp.Hint {
	a := m.action
	switch a.stage {
	case stagePlanning:
		return []comp.Hint{{Key: "esc", Label: "cancel"}}
	case stagePlan:
		if a.plan != nil && a.plan.needsName {
			return []comp.Hint{
				{Label: "type the environment's name"},
				{Key: "enter", Label: "confirm"},
				{Key: "esc", Label: "back"},
			}
		}
		keys := []comp.Hint{{Key: "enter", Label: "apply"}}
		if f := a.field("widen"); f != nil && !f.disabled && !f.on {
			keys = append(keys, comp.Hint{Key: "w", Label: "widen the selection"})
		}
		return append(keys, comp.Hint{Key: "esc", Label: "back"})
	}

	keys := make([]comp.Hint, 0, 3)
	if a.cursor < len(a.fields) {
		switch a.fields[a.cursor].kind {
		case fieldMulti:
			keys = []comp.Hint{
				{Key: "space", Label: "toggle"}, {Key: "a", Label: "all"}, {Key: "n", Label: "none"},
			}
		case fieldChoice:
			keys = []comp.Hint{{Key: "← →", Label: "change"}}
		case fieldToggle:
			keys = []comp.Hint{{Key: "space", Label: "toggle"}}
		}
	}
	return append(keys,
		comp.Hint{Key: "↑↓", Label: "fields"},
		comp.Hint{Key: "enter", Label: "run"},
		comp.Hint{Key: "esc", Label: "cancel"},
	)
}

// viewHelp is the ? overlay: every key, grouped by what it acts on.
func (m *Model) viewHelp() string {
	groups := []struct {
		title string
		keys  [][2]string
	}{
		{"Moving", [][2]string{
			{"1-5", "jump to a panel"},
			{"↑ ↓ / j k", "move within a panel"},
			{"J K", "next / previous panel"},
			{"g G", "first / last row"},
			{"tab", "focus the detail pane, then cycle its tabs"},
			{"shift+tab", "previous tab"},
			{"← / h", "leave the detail pane"},
			{"/", "filter the focused panel"},
			{"esc", "clear the filter, or leave the pane"},
		}},
		{"Doing", [][2]string{
			{"n", "take a snapshot — choose environment and databases"},
			{"a", "apply the selected snapshot — choose target and scope"},
			{"m", "move one environment's data into another"},
			{"p", "prune snapshots by the retention policy"},
			{"x", "delete the selected snapshot"},
			{"r", "reload snapshots and re-probe environments"},
		}},
		{"In a form", [][2]string{
			{"↑ ↓", "move between fields"},
			{"← →", "change a choice"},
			{"space", "toggle"},
			{"a / n", "select all / none in a multi-select"},
			{"enter", "run it"},
			{"esc", "cancel"},
		}},
		{"While something runs", [][2]string{
			{"q", "cancel it — the engine's failure hooks still run"},
		}},
	}

	// Built as lines rather than written straight into a builder, so the
	// overlay can show a window of them. The full list is 35 lines; a 24-line
	// terminal showed the first 22 and offered no way to see the rest, which
	// is the least helpful possible state for a help screen.
	var lines []string
	lines = append(lines, titleStyle.Render("pgctl keys"), "")
	for _, g := range groups {
		lines = append(lines, headerStyle.Render(strings.ToUpper(g.title)))
		for _, k := range g.keys {
			// comp.Pad, not %-12s: Sprintf counts BYTES, so a hint containing
			// ↑ or ← comes out three columns short and the descriptions stop
			// lining up. Every arrow row in this list has that problem.
			lines = append(lines, "  "+accentStyle.Render(comp.Pad(k[0], 12))+" "+k[1])
		}
		lines = append(lines, "")
	}

	// boxStyle is a rounded border plus Padding(1, 2): two rows of border and
	// two of padding, so the content gets four fewer rows than the terminal
	// has, and one more goes to the hint at the bottom.
	height := m.height
	if height <= 0 {
		height = 24
	}
	visible := height - 5
	if visible < 1 {
		visible = 1
	}

	offset := m.helpOffset
	if offset > len(lines)-visible {
		offset = len(lines) - visible
	}
	if offset < 0 {
		offset = 0
	}
	m.helpOffset = offset

	end := min(offset+visible, len(lines))
	shown := append([]string{}, lines[offset:end]...)

	hint := "any key closes this"
	switch below := len(lines) - end; {
	case below > 0:
		hint = fmt.Sprintf("%d more — ↑↓ to scroll, any other key closes", below)
	case offset > 0:
		hint = "the end — ↑↓ to scroll, any other key closes"
	}
	shown = append(shown, mutedStyle.Render(hint))
	return strings.Join(shown, "\n")
}

// drawHelp centres the key list over the frame.
func (m *Model) drawHelp(c *comp.Canvas, r comp.Rect) {
	lines := strings.Split(m.viewHelp(), "\n")
	width := 0
	for _, l := range lines {
		width = max(width, comp.Width(l))
	}
	m.drawOverlay(c, r, comp.Region(regHelp), width, lines, &panelFocusBorder)
}

// wrapIndented wraps text that is already laid out with leading indentation,
// keeping a wrapped line under the one it continues rather than back at the
// margin. Used for the plan description, whose lines list tables and can be
// arbitrarily long.
func wrapIndented(s string, width int) string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		body := strings.TrimLeft(line, " ")
		if body == "" {
			out = append(out, "")
			continue
		}
		avail := width - len(indent) - 2
		if avail < 20 {
			avail = 20
		}
		for i, wrapped := range strings.Split(wrap(body, avail), "\n") {
			if i == 0 {
				out = append(out, indent+wrapped)
				continue
			}
			// Continuations sit two columns in from their line, so a wrapped
			// list still reads as one item.
			out = append(out, indent+"  "+wrapped)
		}
	}
	return strings.Join(out, "\n")
}
