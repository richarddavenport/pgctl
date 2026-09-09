package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/richarddavenport/tuikit/theme"
)

// Palette is tuikit's, unchanged.
//
// pgctl arrived with seven adaptive light/dark hex pairs, on the reasoning that
// a role named once is a theme change in one edit. The naming was right and the
// values were not: only ANSI indices 0-15 follow the reader's terminal theme, so
// naming a hex overrides what they already chose, in every terminal, forever.
// tuikit decision 28 argues this out; azctl reached the same place by migrating.
//
// The old palette's own goal survives intact. It used AdaptiveColor so the
// interface read on a pale terminal as well as a dark one; the default's
// selection is SelectionFG 0 on SelectionBG 7 — the background on the
// foreground — which is reverse video, and inverts correctly on a light theme
// BY CONSTRUCTION rather than by detecting one. Same goal, no table to keep in
// step.
//
// Nothing is overridden. pgctl disagrees with none of the nine.
var Palette = theme.Default

// Glyphs is tuikit's set plus the four pgctl needs.
//
// comp.Spinner needs no entry: theme.SpinnerRange covers U+2800-U+28FF, and the
// component draws the frames rather than pgctl, so its characters never appear
// in a literal here for guard.Glyphs to see.
var Glyphs = theme.DefaultGlyphs.With(
	'←', "leftwards arrow — the key hint for changing a choice, and → is in the default set; they are a pair or neither reads",
	'○', "white circle — an environment pgctl has not reached yet, and an unselected database. ● is the filled half of the same question",
	'▏', "left one-eighth block — the divider comp.Split draws between the panel column and the detail pane",
	'╭', "rounded box drawing", '╮', "rounded box drawing",
	'╰', "rounded box drawing", '╯', "rounded box drawing",
)

// Chrome is tuikit's, with pgctl's rounded panels and a drawn divider.
//
// The corners and the divider are in the glyph set above, so guard.Chrome can
// check them. Two overrides:
//
// Rounded panels, because pgctl's have been rounded since the first TUI commit;
// this is where that stops being a lipgloss call in three places and becomes a
// decision written down once.
//
// A drawn divider, because pgctl's is DRAGGABLE. The default is a blank, on the
// reasoning that a gap between panes should read as space rather than as a third
// thing — right for a layout you cannot change, and it hides the one affordance
// here that is worth finding. A seam you can grab should look like a seam.
var Chrome = drawnDivider(theme.DefaultChrome.With(theme.RoundedBox))

// drawnDivider is a function because theme.Chrome.With takes a box set and
// nothing else. Assigning a field needs a copy, and a copy needs somewhere to
// live; this is that, rather than a var block that mutates a package-level value
// in an init.
func drawnDivider(c theme.Chrome) theme.Chrome {
	c.Divider, c.VDivider = "▏", "▏"
	return c
}

// Colours are named for their role rather than their hue, so a theme change is
// one edit here.
//
// Derived at package level rather than built into a styles struct the model
// carries, which is where the scaffold puts them. The struct earns its keep when
// a tool has more than one palette to render — a design sheet, a second profile.
// pgctl has one, resolved by the terminal at paint time, so a struct would buy
// nothing and cost an edit at all seventy-one call sites.
//
// warning is Pending, the amber role. It is the closest of the nine and not an
// exact fit: Pending is documented as a queued change not yet applied, and
// pgctl's amber is a caveat about what an apply will do — a guarded target, a
// table restored with no rows, warnings on a manifest. Both mean "read this
// before you commit to it", which is near enough that a tenth role would be a
// shade rather than a meaning. A refusal is Danger; amber never stopped
// anything.
var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(Palette.Accent)
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(Palette.Muted)
	mutedStyle    = lipgloss.NewStyle().Foreground(Palette.Muted)
	accentStyle   = lipgloss.NewStyle().Foreground(Palette.Accent)
	dangerStyle   = lipgloss.NewStyle().Foreground(Palette.Danger).Bold(true)
	warnStyle     = lipgloss.NewStyle().Foreground(Palette.Pending)
	okStyle       = lipgloss.NewStyle().Foreground(Palette.Success)
	footerStyle   = lipgloss.NewStyle().Foreground(Palette.Muted)
	selectedStyle = lipgloss.NewStyle().
			Foreground(Palette.SelectionFG).Background(Palette.SelectionBG).Bold(true)

	// currentStyle marks the selected row of a panel that does not have the
	// keys, so the hierarchy stays legible: you can see which environment the
	// databases belong to while your cursor is somewhere else entirely.
	currentStyle = lipgloss.NewStyle().Bold(true).Foreground(Palette.Accent)

	// The border styles are plain foregrounds now, not lipgloss borders: the
	// box is drawn by comp.Pane out of the chrome's own characters, so a style
	// here says only what colour the edge is. The rounded corners moved to
	// Chrome above, where they are a decision rather than three lipgloss calls.
	panelBorder      = lipgloss.NewStyle().Foreground(Palette.Border)
	panelFocusBorder = lipgloss.NewStyle().Foreground(Palette.Accent)

	tabStyle       = lipgloss.NewStyle().Foreground(Palette.Muted)
	activeTabStyle = lipgloss.NewStyle().Foreground(Palette.Accent).Bold(true).Underline(true)

	// The caret's two cells, which is where the palette's selection roles are
	// doing their second job: SelectionFG is the terminal's background and
	// SelectionBG its foreground, so a caret painted with them is reverse
	// video — and it inverts correctly on a pale terminal BY CONSTRUCTION
	// rather than by detecting one. There is no "foreground" role to reach
	// for, deliberately: nine roles is the whole vocabulary, and ordinary text
	// is what the terminal already draws.
	cursorFG = lipgloss.NewStyle().Foreground(Palette.SelectionFG)
	cursorBG = lipgloss.NewStyle().Background(Palette.SelectionBG)
)
