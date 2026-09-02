package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/tuikit/app"
	"github.com/richarddavenport/tuikit/comp"
)

// onMouse routes a mouse event against the last frame.
//
// pgctl has never had a mouse. It gets one here for nothing, because every row
// drawn under a region records WHICH connection, database, snapshot or set drew
// it — an index into the list rather than a line on the screen — so a click
// after a scroll lands on the thing you pointed at instead of whatever moved
// into that row.
//
// app.Mouse holds the one piece of state that matters: a drag owns the mouse
// until release, whatever it is over by then. The pointer outruns the divider
// on every real drag, and without that rule the divider is dropped mid-gesture.
func (m *Model) onMouse(msg tea.MouseMsg) tea.Cmd {
	return m.mouse.Route(msg, m.canvas, app.Handler{
		// A modal takes the mouse as well as the keyboard. Clicking the panel
		// behind a confirmation is not an answer to it — and the panel is
		// exactly what the confirmation is about, so the click is tempting.
		Blocked: func() bool { return m.action != nil || m.showHelp },

		Press: func(id comp.ID, _ tea.MouseMsg) tea.Cmd {
			// Clicking a panel's row selects it AND moves focus there, because
			// the panels are a hierarchy: choosing an environment changes what
			// every panel below it is about, and doing that without taking
			// focus would leave the keys acting somewhere else.
			for panel := range panelRegions {
				if id.Name != panelRegions[panel] && id.Name != panelRowRegions[panel] {
					continue
				}
				m.focus, m.paneFocus = panel, false
				if id.Index != comp.NoIndex {
					m.lists[panel].Select(id.Index)
				}
				return m.onSelectionChanged()
			}
			switch id.Name {
			case regTabs:
				// A click on a tab selects that tab, not the next one. Index is
				// the tab's own, which is why comp.Tabs gives each one a region
				// rather than the strip owning them all.
				if id.Index != comp.NoIndex {
					m.tabs[m.focus] = id.Index
					m.paneFocus = true
					m.paneList.Reset()
					return m.paneLoad()
				}
			case regPane, regBody:
				m.paneFocus = true
				if id.Name == regBody && id.Index != comp.NoIndex {
					m.paneList.Select(id.Index)
				}
			}
			return nil
		},

		// The wheel moves the VIEWPORT of whatever the pointer is over, never
		// the selection. Wiring it to the cursor makes scrolling appear to pick
		// things at random, and in a tool whose next keystroke may replace a
		// database that is not a cosmetic problem.
		Wheel: func(id comp.ID, by int) tea.Cmd {
			for panel := range panelRegions {
				if id.Name == panelRegions[panel] || id.Name == panelRowRegions[panel] {
					m.lists[panel].Scroll(by)
					return nil
				}
			}
			if id.Name == regBody || id.Name == regPane {
				m.paneList.Scroll(by)
			}
			return nil
		},

		Drags: func(id comp.ID) bool { return id.Name == regSplit },
		Drag: func(_ comp.ID, msg tea.MouseMsg) tea.Cmd {
			m.split.MoveTo(msg.X, m.body())
			return nil
		},
	})
}
