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

// viewAction renders the open form, the plan it produced, or the wait between
// them.
func (m *Model) viewAction() string {
	a := m.action
	width := m.actionWidth()

	var b strings.Builder
	b.WriteString(titleStyle.Render(truncate(a.title, width)) + "\n")
	if a.explain != "" {
		b.WriteString(mutedStyle.Render(wrap(a.explain, width)) + "\n")
	}
	b.WriteString("\n")

	switch a.stage {
	case stagePlanning:
		b.WriteString(accentStyle.Render(spinner(m.now)) + " reading the target's foreign keys…\n")
		b.WriteString(mutedStyle.Render("\nThe plan is computed against the target, because the target's\n" +
			"constraints are the ones a load has to satisfy."))
	case stagePlan:
		b.WriteString(m.viewPlanPreview())
	default:
		b.WriteString(m.viewForm())
	}

	if a.err != nil {
		b.WriteString("\n\n" + dangerStyle.Render(wrap(a.err.Error(), width)))
	}
	return b.String()
}

// drawAction centres the open form over the frame.
//
// A comp.Pane rather than a lipgloss box over a hand-clipped screen: the canvas
// blanks a pane's interior, so a modal covers what is behind it without pgctl
// cutting the rows underneath itself. That cutting was 30 lines — overlay(),
// clip() and padTo() between them — and it is where the "…" against the modal's
// left edge came from.
func (m *Model) drawAction(c *comp.Canvas, r comp.Rect) {
	lines := strings.Split(m.viewAction(), "\n")
	m.drawOverlay(c, r, comp.Region(regModal), m.actionWidth(), lines, &panelFocusBorder)
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
func (m *Model) actionFooter() string {
	a := m.action
	switch a.stage {
	case stagePlanning:
		return "esc cancel"
	case stagePlan:
		if a.plan != nil && a.plan.needsName {
			return "type the environment's name  ·  enter confirm  ·  esc back"
		}
		hints := "enter apply  ·  esc back to the form"
		if f := a.field("widen"); f != nil && !f.disabled && !f.on {
			hints = "enter apply  ·  w widen the selection  ·  esc back"
		}
		return hints
	default:
		if a.cursor < len(a.fields) {
			switch a.fields[a.cursor].kind {
			case fieldMulti:
				return "space toggle  ·  a all  ·  n none  ·  ↑↓ fields  ·  enter run  ·  esc cancel"
			case fieldChoice:
				return "← → change  ·  ↑↓ fields  ·  enter run  ·  esc cancel"
			case fieldToggle:
				return "space toggle  ·  ↑↓ fields  ·  enter run  ·  esc cancel"
			}
		}
		return "↑↓ fields  ·  enter run  ·  esc cancel"
	}
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
