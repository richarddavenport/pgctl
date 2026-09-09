package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/richarddavenport/tuikit/comp"

	"github.com/richarddavenport/pgctl/internal/update"
)

// Version is the running binary's version, set from main. "dev" until it is.
var Version = "dev"

// updateEvery is how often a running session re-checks for a release.
//
// Five minutes is 12 requests an hour against an unauthenticated limit of 60,
// and 5000 with a token. The check is a courtesy and its failures are silent,
// so the budget only has to be small enough never to matter.
const updateEvery = 5 * time.Minute

// updateNotice is a newer release, once one is known.
type updateNotice struct {
	Version string

	// Fresh means it was published while this session was running, rather than
	// having already been out when it started.
	Fresh bool
}

// showable reports a notice worth drawing, against the version actually
// running. The running version is not an update, and neither is nothing.
func (u updateNotice) showable(running string) bool {
	return u.Version != "" && u.Version != running
}

type (
	updateTickMsg      struct{}
	updateAvailableMsg struct{ version string }
	updateAppliedMsg   struct{ err error }
)

func updateTick() tea.Cmd {
	return tea.Tick(updateEvery, func(time.Time) tea.Msg { return updateTickMsg{} })
}

// checkUpdate looks for a newer release in the background. Every failure is
// silent — offline, rate-limited, no such repo — because this is a courtesy
// notice and an error about it would be noise in front of the actual work.
func checkUpdate(running string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		rel, err := update.Latest(ctx, update.OptionalToken(ctx), "")
		if err != nil || !update.Newer(rel.Version, running) {
			return nil
		}
		// A local build is not out of date — it is usually NEWER than the last
		// release, and `pgctl update` refuses to replace it anyway. Offering an
		// update the tool would then decline is worse than saying nothing, and
		// with the version in the footer the two would sit side by side
		// contradicting each other.
		if !update.Released(running) {
			return nil
		}
		return updateAvailableMsg{version: rel.Version}
	}
}

// The update screen: installing pgctl from inside pgctl.
//
// It deliberately does NOT restart anything. Replacing the binary of a running
// process is safe on Unix — the kernel holds the old inode — but the running
// code is still the old code, and a screen that claimed otherwise would be
// lying. So it says what to do and the operator decides when.
type updateStage int

const (
	updateConfirm updateStage = iota
	updateWorking
	updateDone
)

type updateModel struct {
	// running is the version this process is, and target the one on offer.
	running string
	target  string
	stage   updateStage
	err     error

	// brewed is the Homebrew path this binary lives at, when it does.
	brewed string

	// local means a local build, which installing would replace with something
	// older.
	local bool

	started time.Time
	took    time.Duration
}

func newUpdate(running, target string) *updateModel {
	u := &updateModel{running: running, target: target, local: !update.Released(running)}
	if path, brewed := update.HomebrewManaged(); brewed {
		u.brewed = path
	}
	return u
}

// openUpdate puts the update screen in front, or says why there is nothing to
// install.
func (m *Model) openUpdate() {
	if !m.updateAvail.showable(m.version) {
		// Said in the status line rather than opening a screen that would only
		// say "nothing to do".
		m.status = "pgctl " + m.version + " is the latest release"
		if !m.updateChecked {
			m.status = "still checking for a newer release"
		}
		return
	}
	m.updater = newUpdate(m.version, m.updateAvail.Version)
}

// applyUpdate fetches and installs, through the same package `pgctl update`
// uses, so there is one implementation of replacing the binary and one set of
// refusals.
func applyUpdate() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		token := update.OptionalToken(ctx)
		rel, err := update.Latest(ctx, token, "")
		if err != nil {
			return updateAppliedMsg{err: err}
		}
		return updateAppliedMsg{err: update.Apply(ctx, token, rel)}
	}
}

// updateKey routes a keypress while the update screen is open.
func (m *Model) updateKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	u := m.updater
	switch msg.String() {
	case "esc":
		// Closable at every stage including working: the download is a
		// detached command and closing the screen does not cancel it, which is
		// the honest behaviour for something that ends in one atomic rename.
		m.updater = nil
		return nil, true

	case "enter":
		if u.stage != updateConfirm {
			return nil, true
		}
		u.stage, u.started = updateWorking, m.now
		return applyUpdate(), true
	}
	return nil, true
}

// drawUpdate is the screen: what will happen, then what did.
func (m *Model) drawUpdate(c *comp.Canvas, r comp.Rect) {
	u := m.updater
	id := comp.Region(regModal)

	inner := min(58, r.W-6)
	lines := u.lines()
	inside, ok := m.modalInside(c, r, inner, u.rows(inner))
	if !ok {
		return
	}

	bands := comp.Layout{Constraints: []comp.Constraint{
		comp.Length(1),
		comp.Length(1), // a blank, so the head reads as the head
		comp.Fill(1),
		comp.Length(1), // the keys
	}}.Rows(inside)

	head := m.detail()
	head.Title = "update"
	head.Draw(c, bands[0], id)

	comp.Bar{Left: []comp.Segment{
		{Text: u.running + " → " + u.target, Style: &mutedStyle},
	}}.Draw(c, bands[1], id)

	if u.stage == updateWorking {
		comp.Waiting{
			Label:  "downloading " + u.target,
			Detail: "checked against the release's checksums before anything is replaced",
			Spinner: comp.Spinner{
				Every: tickInterval,
				Style: &accentStyle,
			},
			Since:       u.started,
			DetailStyle: &mutedStyle,
		}.Draw(c, bands[2], m.now, id)
	} else {
		body := comp.Detail{
			LabelStyle: &headerStyle,
			ValueStyle: &mutedStyle,
			Blocks:     lines,
		}
		body.Draw(c, bands[2], id)
	}

	comp.KeyHints(c, bands[3], id, &footerStyle, u.hints()...)
}

// lines is what the screen says, which is different at each stage and in the
// two cases where installing is not simply an upgrade.
func (u *updateModel) lines() []comp.Block {
	switch u.stage {
	case updateWorking:
		return nil

	case updateDone:
		if u.err != nil {
			return []comp.Block{{Facts: []comp.Fact{
				{Value: u.err.Error(), Style: &dangerStyle},
			}}}
		}
		return []comp.Block{
			{Facts: []comp.Fact{{
				Value: fmt.Sprintf("installed %s in %s", u.target, u.took.Round(time.Second)),
				Style: &okStyle,
			}}},
			// The running process is still the old binary. Saying "done"
			// without this leaves somebody wondering why the footer has not
			// changed.
			{Facts: []comp.Fact{{
				Value: "this session is still " + u.running + " — quit and start pgctl again to use " + u.target,
				Style: &warnStyle,
			}}},
		}
	}

	// comp.Block wraps its own Text; width is only needed to count the rows the
	// box has to be tall enough for.
	blocks := []comp.Block{
		{Text: "Replace this binary with " + u.target + ", downloaded from the releases " +
			"and checked against their checksums."},
		{Text: "No database is touched: this updates the tool, not anything it has snapshotted."},
	}
	if u.local {
		blocks = append(blocks, comp.Block{Facts: []comp.Fact{
			{Value: "you are running a local build (" + u.running + ")", Style: &dangerStyle},
			{Value: "installing " + u.target + " would replace it with an older binary", Style: &dangerStyle},
		}})
	}
	if u.brewed != "" {
		blocks = append(blocks, comp.Block{Facts: []comp.Fact{
			{Value: "this binary is Homebrew-managed", Style: &warnStyle},
			{Value: "`brew upgrade pgctl` gets the same build and keeps brew's records in step", Style: &mutedStyle},
		}})
	}
	return blocks
}

func (u *updateModel) hints() []comp.Hint {
	switch u.stage {
	case updateWorking:
		return []comp.Hint{{Key: "esc", Label: "close — the download continues"}}
	case updateDone:
		if u.err != nil {
			return []comp.Hint{{Key: "esc", Label: "back"}}
		}
		return []comp.Hint{{Key: "esc", Label: "back"}, {Key: "q", Label: "quit and restart"}}
	}
	return []comp.Hint{{Key: "enter", Label: "install"}, {Key: "esc", Label: "cancel"}}
}

// rows is how tall the box has to be: the head, a blank, the body, and the key
// row — with every block's own wrapped height counted, because a block that
// wraps to three lines in a narrow terminal needs three rows and comp.Detail
// will not shrink to fit.
func (u *updateModel) rows(width int) int {
	if u.stage == updateWorking {
		return 2 + 2 + 1 // head, blank, the wait's two lines, keys
	}
	rows := 3 // head, blank, keys
	for i, b := range u.lines() {
		if i > 0 {
			rows++ // comp.Detail separates blocks with a blank
		}
		if b.Text != "" {
			rows += len(comp.Wrap(b.Text, width))
		}
		for _, f := range b.Facts {
			rows += len(comp.Wrap(f.Value, width))
		}
	}
	return rows
}
