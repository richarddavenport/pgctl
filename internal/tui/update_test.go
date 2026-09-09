package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/richarddavenport/tuikit/comp"
	"github.com/richarddavenport/tuikit/harness"
)

// The version is in the corner of every screen, and a newer release is an arrow
// beside it.
//
// The corner is where somebody already looks to answer "what am I running", so
// the answer "not the latest" belongs in the same place. A notice anywhere else
// gets read once and then stops being seen.
func TestTheFooterSaysWhatIsRunningAndWhatIsNewer(t *testing.T) {
	m := loadedModel(t)

	// The footer line, not the whole frame: "newest" appears in the detail
	// pane, and asserting on the frame made a test of the word "new" pass for
	// the wrong reason.
	if got := footerOf(t, m); !strings.Contains(got, m.version) {
		t.Errorf("the footer does not say the running version %q: %q", m.version, got)
	}
	if got := footerOf(t, m); strings.Contains(got, "→") {
		t.Errorf("an arrow is drawn with no newer release known: %q", got)
	}

	offered(t, m, false)
	if got := footerOf(t, m); !strings.Contains(got, "v0.3.0 → v0.4.0") {
		t.Errorf("the footer does not offer v0.4.0: %q", got)
	}
	if got := footerOf(t, m); strings.Contains(got, "new") {
		t.Errorf(`"new" is claimed for a release that was already out: %q`, got)
	}

	// Published while this session was running, which is a different statement.
	m.updateAvail.Fresh = true
	if got := footerOf(t, m); !strings.Contains(got, "v0.4.0 new") {
		t.Errorf("a release found mid-session is not marked new: %q", got)
	}
}

// "new" means it happened while you were here, and the first answer of a
// session never does.
//
// Otherwise a release that had been out for a week would be announced as though
// it had just landed, every time pgctl started.
func TestFreshIsOnlyForAReleaseFoundMidSession(t *testing.T) {
	m := loadedModel(t)

	m.Update(updateAvailableMsg{version: "v0.4.0"})
	if m.updateAvail.Fresh {
		t.Error("the first check of a session reported its answer as new")
	}
	if !m.updateChecked {
		t.Error("the check did not record that it had answered")
	}

	// A second, different answer: this one really did appear while sitting here.
	m.Update(updateAvailableMsg{version: "v0.5.0"})
	if !m.updateAvail.Fresh {
		t.Error("a release published mid-session was not marked new")
	}

	// The same answer again is not news.
	m.Update(updateAvailableMsg{version: "v0.5.0"})
	if m.updateAvail.Fresh {
		t.Error("re-reporting the same release marked it new")
	}
}

// U opens the screen, and only when there is something to install.
func TestUOpensTheUpdateScreenOnlyWhenThereIsOne(t *testing.T) {
	m := loadedModel(t)

	// Before the check has answered, the status says so rather than opening a
	// screen that would have to say "nothing to do".
	press(t, m, "U")
	if m.updater != nil {
		t.Error("the update screen opened with no release known")
	}
	if !strings.Contains(m.status, "checking") {
		t.Errorf("status = %q, want it to say the check has not answered", m.status)
	}

	m.updateChecked = true
	press(t, m, "U")
	if m.updater != nil {
		t.Error("the update screen opened when the running version is the latest")
	}
	if !strings.Contains(m.status, "latest release") {
		t.Errorf("status = %q, want it to say this is the latest", m.status)
	}

	offered(t, m, false)
	press(t, m, "U")
	if m.updater == nil {
		t.Fatal("U did not open the update screen")
	}
	if m.updater.target != "v0.4.0" {
		t.Errorf("the screen offers %q, want v0.4.0", m.updater.target)
	}
}

// A local build is warned about rather than quietly downgraded.
//
// `make install` stamps a git describe, so a developer pressing U is always in
// this case: the last release is OLDER than what they are running, and
// installing it throws away the build they were working on.
func TestTheScreenWarnsBeforeReplacingALocalBuild(t *testing.T) {
	m := loadedModel(t)
	offered(t, m, true)
	press(t, m, "U")

	frame := harness.Strip(run(m, 132, 38).View())
	for _, want := range []string{"local build", "older binary", localBuildVersion} {
		if !strings.Contains(frame, want) {
			t.Errorf("the screen does not say %q:\n%s", want, frame)
		}
	}
}

// The screen does not claim the running session was upgraded.
//
// Replacing the binary of a running process is safe — the kernel holds the old
// inode — but the running code is still the old code, and a screen that said
// "done" and nothing else would leave somebody wondering why the footer had not
// changed.
func TestTheScreenSaysTheSessionIsStillTheOldVersion(t *testing.T) {
	m := loadedModel(t)
	offered(t, m, false)
	press(t, m, "U")
	m.Update(updateAppliedMsg{})

	if m.updater.stage != updateDone {
		t.Fatalf("stage = %v, want done", m.updater.stage)
	}
	frame := harness.Strip(run(m, 132, 38).View())
	for _, want := range []string{"installed v0.4.0", "still v0.3.0", "quit and start pgctl again"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the screen does not say %q:\n%s", want, frame)
		}
	}
}

// A failure is shown where the offer was, not swallowed.
func TestAFailedUpdateIsSaidOnTheScreen(t *testing.T) {
	m := loadedModel(t)
	offered(t, m, false)
	press(t, m, "U")
	m.Update(updateAppliedMsg{err: errors.New("does not match the release checksum")})

	frame := harness.Strip(run(m, 132, 38).View())
	if !strings.Contains(frame, "does not match the release checksum") {
		t.Errorf("the failure is not on screen:\n%s", frame)
	}
}

// esc closes the screen while the download is running, and the outcome still
// arrives.
//
// The download is a detached command; closing the screen does not cancel it,
// and pretending it did would be a lie about something that ends in an atomic
// rename. So the answer goes to the status line, where a reload or a refusal
// also lands.
func TestClosingTheScreenMidDownloadStillReportsTheOutcome(t *testing.T) {
	m := loadedModel(t)
	offered(t, m, false)
	press(t, m, "U", "enter")
	if m.updater.stage != updateWorking {
		t.Fatalf("enter did not start the download: stage = %v", m.updater.stage)
	}

	press(t, m, "esc")
	if m.updater != nil {
		t.Fatal("esc did not close the screen")
	}

	m.Update(updateAppliedMsg{})
	if !strings.Contains(m.status, "restart") {
		t.Errorf("status = %q, want the outcome of a download nobody was watching", m.status)
	}
}

// The palette carries the update, in its own group, and refuses with the reason
// when there is nothing to install.
func TestThePaletteOffersTheUpdateAndRefusesWithTheReason(t *testing.T) {
	m := loadedModel(t)
	m.updateChecked = true

	item := m.toolItems()[0]
	if !item.Refused {
		t.Error("the palette offers an update when this is the latest release")
	}
	if !strings.Contains(item.Hint, "latest release") {
		t.Errorf("hint = %q, want the reason", item.Hint)
	}

	offered(t, m, false)
	item = m.toolItems()[0]
	if item.Refused {
		t.Errorf("the palette refuses an available update: %q", item.Hint)
	}
	if !strings.Contains(item.Hint, "v0.4.0") || !strings.Contains(item.Hint, "no database") {
		t.Errorf("hint = %q, want the version and what it does not touch", item.Hint)
	}
	// And its key is the key, so the directory cannot drift from the keyboard.
	if item.Key != "U" {
		t.Errorf("key = %q, want U", item.Key)
	}
}

// The version survives a terminal much wider or narrower than the goldens.
//
// The goldens are 132 and 80 columns. A real terminal is whatever somebody has
// dragged their window to — 250 columns in the report that prompted this — and
// at that size the hint line and the version are nowhere near each other, which
// is the arrangement no golden covers.
func TestTheVersionSurvivesAnyTerminalWidth(t *testing.T) {
	m := loadedModel(t)
	offered(t, m, false)

	for _, w := range []int{250, 400, 100, 60, 40} {
		lines := harness.Lines(harness.Strip(run(m, w, 50).View()))
		got := strings.TrimRight(lines[len(lines)-1], " ")
		// comp.Width, not len: `→` is three bytes and `·` is two, so a byte
		// count reports every one of these lines as nine columns too wide.
		if comp.Width(got) > w {
			t.Errorf("at %d columns the footer is %d wide: %q", w, comp.Width(got), got)
		}
		if !strings.Contains(got, "v0.4.0") {
			t.Errorf("at %d columns the footer does not offer the release: %q", w, got)
		}
	}
}

// footerOf is the bottom line of the frame, which is where the version lives.
//
// Stripped first. Whether a frame comes out coloured depends on a global in
// the terminal library, which TestColorDoesNotChangeTheShape turns on — so this
// passed on its own and failed in the suite, with the version wrapped in escape
// codes that no Contains would match.
func footerOf(t *testing.T, m *Model) string {
	t.Helper()
	lines := harness.Lines(harness.Strip(run(m, 132, 38).View()))
	if len(lines) == 0 {
		t.Fatal("an empty frame")
	}
	return strings.TrimRight(lines[len(lines)-1], " ")
}
