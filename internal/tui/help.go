package tui

import (
	"github.com/richarddavenport/tuikit/comp"
)

// KeySections is every binding pgctl has, by what you are doing when it applies.
//
// Exported because internal/cli checks it against the command tree: guard.Keys
// fails when a command declares a Key the help screen does not document, which
// is the drift that makes a help screen worse than none. internal/tui imports
// nothing from internal/cli — the interface does not know how it was launched —
// so the check lives on that side and reads this.
//
// The footer says what acts on THIS; this says what exists. Both are built from
// comp.Hint, which is what keeps them from disagreeing about a key's name — and
// the same type the command directory offers.
func KeySections() []comp.KeySection {
	return []comp.KeySection{
		{Name: "Moving", Keys: []comp.Hint{
			{Key: "h l / ← →", Label: "previous / next panel"},
			{Key: "j k / ↑ ↓", Label: "move within a panel"},
			{Key: "1-5", Label: "jump straight to a panel"},
			{Key: "g G", Label: "first / last row"},
			{Key: "[ ]", Label: "previous / next tab of the detail pane"},
			{Key: "tab", Label: "focus the detail pane, and back"},
			{Key: "/", Label: "filter the focused panel"},
			{Key: "esc", Label: "clear the filter, or leave the pane"},
		}},
		{Name: "Doing", Keys: []comp.Hint{
			{Key: "ctrl+p", Label: "every command, with the key that runs it"},
			{Key: "n", Label: "take a snapshot — choose the databases"},
			{Key: "a", Label: "apply the selected snapshot — choose target and scope"},
			{Key: "m", Label: "move one connection's data into another"},
			{Key: "p", Label: "prune snapshots by the retention policy"},
			{Key: "x", Label: "delete the selected snapshot"},
			{Key: "r", Label: "reload snapshots and re-probe connections"},
		}},
		{Name: "In a form", Keys: []comp.Hint{
			{Key: "↑ ↓", Label: "move between fields"},
			{Key: "← →", Label: "change a choice"},
			{Key: "space", Label: "toggle"},
			{Key: "enter", Label: "run it, or plan it"},
			{Key: "esc", Label: "cancel"},
		}},
		{Name: "Reading a plan", Keys: []comp.Hint{
			{Key: "↑ ↓", Label: "scroll it — nothing has been touched yet"},
			{Key: "w", Label: "widen to the referential closure and re-plan"},
			{Key: "enter", Label: "apply it"},
			{Key: "esc", Label: "back to the form"},
		}},
		// The markers, in the screen a reader opens when they do not know
		// something. `?` answered "what can I press" and not "what does that
		// mean", so the first person to see a snapshot marked `r` had to ask —
		// and comp.Hint is a key and a label, which is exactly the shape a
		// legend is.
		{Name: "What a snapshot row means", Keys: []comp.Hint{
			{Key: "l", Label: "on this machine only"},
			{Key: "r", Label: "in a remote only — applying downloads it first"},
			{Key: "l+r", Label: "both: here and uploaded"},
			{Key: "✗", Label: "it did not finish; it cannot be applied"},
		}},
		{Name: "What a connection row means", Keys: []comp.Hint{
			{Key: "●", Label: "reachable"},
			{Key: "○", Label: "not reached yet — pgctl has not tried"},
			{Key: "⠋", Label: "being reached now"},
			{Key: "✗", Label: "unreachable — Overview has the error"},
			{Key: "protected", Label: "never an apply target, no override"},
			{Key: "guarded", Label: "an apply needs the name typed in full"},
		}},
		{Name: "What a set row means", Keys: []comp.Hint{
			{Key: "4 closed", Label: "closed: it can be applied on its own"},
			{Key: "4+2", Label: "4 named, 2 dragged in by foreign keys"},
			{Key: "?", Label: "not resolved yet — select it to read the catalog"},
		}},

		{Name: "While something runs", Keys: []comp.Hint{
			{Key: "tab", Label: "the steps, then the log it came from"},
			{Key: "q", Label: "leave — it is cancelled, so the failure hooks run"},
			{Key: "ctrl+c", Label: "the same, without the question"},
		}},
	}
}

// drawHelp is the key list, scrolled.
//
// comp.Keys does the sections, the key column and the scrolling, and it reports
// its own total so the offset can be clamped against it. The version this
// replaces built 35 styled lines, measured them, windowed them by hand and
// wrote the window into a box — and on a 24-line terminal it showed the first
// twenty-two with no way to reach the rest, which is the least helpful state a
// help screen has.
func (m *Model) drawHelp(c *comp.Canvas, r comp.Rect) {
	keys := comp.Keys{
		Sections:     KeySections(),
		Overlay:      true,
		Title:        "pgctl keys",
		TitleStyle:   &titleStyle,
		SectionStyle: &headerStyle,
		KeyStyle:     &accentStyle,
		LabelStyle:   &mutedStyle,
		Border:       &panelFocusBorder,
	}

	// The offset is clamped against the component's own count rather than
	// against an estimate of it, so scrolling stops at the end of the list
	// instead of past it.
	if visible := r.H - 4; visible > 0 {
		m.helpOffset = clamp(m.helpOffset, max(0, keys.Rows()-visible))
	}
	keys.Offset = m.helpOffset
	keys.Draw(c, r, comp.Region(regHelp))
}
