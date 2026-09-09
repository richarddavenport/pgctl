package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/tuikit/app"
	"github.com/richarddavenport/tuikit/comp"
)

// The command directory: one key to everything pgctl can do right now.
//
// A directory of the keyboard, not a replacement for it. Every row names the
// key that does the same thing, so using it teaches the key — and a row that
// CANNOT run is listed with the reason instead of being hidden, which is the
// part that earns its place here: "apply — no snapshot selected" and "move —
// every other connection is protected" are the two questions a reader of this
// tool asks most, and the answer used to arrive as a refusal after pressing
// the key.

// newCommandPalette is the styles, which do not change. The groups do, and are
// rebuilt each time it opens.
func newCommandPalette() comp.Palette {
	return comp.Palette{
		Title:       "run",
		Name:        regCommands,
		Item:        regCommandItem,
		Border:      &panelFocusBorder,
		TitleStyle:  &titleStyle,
		PromptStyle: &mutedStyle,
		QueryStyle:  &accentStyle,
		GroupStyle:  &headerStyle,
		KeyStyle:    &accentStyle,
		HintStyle:   &mutedStyle,
		NoteStyle:   &mutedStyle,
		MatchStyle:  &accentStyle,
		WarnStyle:   &warnStyle,
		DangerStyle: &dangerStyle,
		SelectedFG:  &selectedStyle,
		Cursor:      &cursorBG,
		Hints: []comp.Hint{
			{Key: "enter", Label: "run"},
			{Key: "↑↓", Label: "move"},
			{Key: "esc", Label: "close"},
		},
	}
}

// commandGroups is what can be done right now, and what cannot.
func (m *Model) commandGroups() []comp.PaletteGroup {
	return []comp.PaletteGroup{
		{Name: "Operations", Note: "act on what is selected", Items: m.operationItems()},
		{Name: "Reading", Note: "nothing is written", Items: []comp.PaletteItem{
			{Key: "r", Label: "reload",
				Hint: "re-list snapshots and re-probe every connection pgctl has reached"},
			{Key: "/", Label: "filter", Hint: "narrow the focused panel"},
			{Key: "tab", Label: "detail pane", Hint: "focus it, then cycle its tabs"},
			{Key: "?", Label: "keys", Hint: "every binding, by screen"},
		}},
	}
}

// operationItems is the five operations, each either runnable or refused with
// the reason.
//
// The reasons are the same ones the forms refuse with, asked here BEFORE the
// key is pressed. They are deliberately not the engine's refusals — those need
// a target and a plan, and this list is drawn from what is already loaded.
func (m *Model) operationItems() []comp.PaletteItem {
	snapshot := comp.PaletteItem{Key: "n", Label: "snapshot", Hint: "read the chosen databases and write a compressed copy"}
	if _, ok := m.selectedConn(); !ok {
		snapshot.Refused, snapshot.Hint = true, "no connections declared in "+m.cfg.Source
	}

	apply := comp.PaletteItem{Key: "a", Label: "apply", Warn: true,
		Hint: "replace data on a target — the plan is shown first"}
	switch entry, ok := m.selectedSnapshot(); {
	case !ok:
		apply.Refused, apply.Hint = true, "no snapshot selected — take one with n"
	case !entry.Manifest.Complete():
		apply.Refused, apply.Hint = true, entry.Manifest.ID+" did not finish and cannot be applied"
	default:
		apply.Hint = fmt.Sprintf("replace data with %s — the plan is shown first", entry.Manifest.ID)
	}

	move := comp.PaletteItem{Key: "m", Label: "move", Warn: true,
		Hint: "snapshot one connection into another and delete the copy"}
	if from, ok := m.selectedConn(); !ok {
		move.Refused, move.Hint = true, "no connection selected"
	} else if targets, _ := m.applyTargets(from.Name); len(targets) == 0 {
		move.Refused = true
		move.Hint = "nowhere to move " + from.Name + " to: every other connection is protected"
	}

	prune := comp.PaletteItem{Key: "p", Label: "prune", Hint: "report what the retention policy would remove"}
	if r := m.cfg.Storage.Retention; r.Daily == 0 && r.Weekly == 0 && r.Monthly == 0 {
		prune.Refused, prune.Hint = true, "no storage.retention configured in "+m.cfg.Source
	}

	del := comp.PaletteItem{Key: "x", Label: "delete the snapshot", Warn: true,
		Hint: "remove it from disk and from storage — this cannot be undone"}
	if _, ok := m.selectedSnapshot(); !ok {
		del.Refused, del.Hint = true, "no snapshot selected"
	}

	return []comp.PaletteItem{snapshot, apply, move, prune, del}
}

// commandKey routes a keypress while the directory is open.
func (m *Model) commandKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc", "ctrl+p":
		m.showCommand = false
		m.commands.Query, m.commands.Caret = "", 0
		return nil, true

	case "up", "ctrl+k":
		m.commands.Move(-1)
		return nil, true

	case "down", "ctrl+j":
		m.commands.Move(1)
		return nil, true

	case "enter":
		item, ok := m.commands.Selected()
		if !ok || !item.Runnable() {
			// A refused row is still selectable, because its hint is the
			// answer. Pressing enter on it does nothing rather than closing
			// the directory, so the reason stays on screen.
			return nil, true
		}
		m.showCommand = false
		m.commands.Query, m.commands.Caret = "", 0
		// The item IS its key: running one is pressing the key it names, which
		// is what makes this a directory of the keyboard rather than a second
		// way to do things that could drift from the first.
		return m.runKey(item.Key)
	}

	// Anything else is the query. j and k are the letters j and k in here,
	// which is the whole reason the palette is a Capture rather than a screen.
	if q, caret, ok := app.EditAt(m.commands.Query, m.commands.Caret, msg); ok {
		m.commands.Query, m.commands.Caret = q, caret
		m.commands.Select(0)
	}
	return nil, true
}

// runKey presses a key on the reader's behalf.
//
// It goes through the screen's own handler, so a command cannot mean something
// different from the key beside it in the list — and the globals are reachable
// too, because ? and r are in the directory and one of them is a global.
func (m *Model) runKey(key string) (tea.Cmd, bool) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	switch key {
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	}
	if cmd, ok := m.screenKey(msg); ok {
		return cmd, true
	}
	return m.globalKey(msg)
}

// drawCommands centres the directory over the frame.
//
// Bounded rather than filling the screen. comp.Palette draws into the rect it is
// given and a full-frame one is a directory of nine commands with twenty blank
// rows under it — which reads as a screen pgctl switched to rather than a thing
// in front of what you were doing.
func (m *Model) drawCommands(c *comp.Canvas, r comp.Rect) {
	m.commands.Note = "every key that acts right now; a greyed row says why it cannot"
	m.commands.Right = fmt.Sprintf("%d of %d", m.commands.Matches(), m.commands.Total())

	// The query, its note, a rule, the rows, a rule and the hints — plus the
	// two rows of border, and a heading per group. A query flattens the groups
	// into one column and the box gets shorter, which is what should happen:
	// the box is the size of what is in it.
	rows := m.commands.Matches() + 7
	if m.commands.Query == "" {
		rows += len(m.commands.Groups)
	}
	box := comp.Rect{W: min(96, r.W-4), H: min(rows+2, r.H-2)}
	box.X, box.Y = r.X+(r.W-box.W)/2, r.Y+(r.H-box.H)/2
	m.commands.Draw(c, box)
}
