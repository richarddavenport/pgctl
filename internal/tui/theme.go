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
// The braille spinner in rows.go needs no entry: theme.SpinnerRange covers
// U+2800-U+28FF, on the reasoning that braille is in every terminal font, which
// is why a spinner reaches for it instead of the block elements.
var Glyphs = theme.DefaultGlyphs.With(
	'←', "leftwards arrow — the key hint for changing a choice, and → is in the default set; they are a pair or neither reads",
	'○', "white circle — an environment pgctl has not reached yet, and an unselected database. ● is the filled half of the same question",
	'▏', "left one-eighth block — the text cursor in the filter and the action form. The same character azctl chose, for the same reason: an interface that cannot show where typing lands is one you type into blind",
	'╭', "rounded box drawing", '╮', "rounded box drawing",
	'╰', "rounded box drawing", '╯', "rounded box drawing",
)

// Chrome is tuikit's, with pgctl's rounded panels.
//
// One field, and the four corners are in the glyph set above so guard.Chrome
// can check them. pgctl's panels have been rounded since the first TUI commit;
// this is where that stops being a lipgloss call in three places and becomes a
// decision written down once.
var Chrome = theme.DefaultChrome.With(theme.RoundedBox)

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

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(Palette.Border).Padding(0, 1)
	focusedPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).BorderForeground(Palette.Accent).Padding(0, 1)

	tabStyle       = lipgloss.NewStyle().Foreground(Palette.Muted).Padding(0, 1)
	activeTabStyle = lipgloss.NewStyle().Foreground(Palette.Accent).Bold(true).Underline(true).Padding(0, 1)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(Palette.Accent).Padding(1, 2)
)
