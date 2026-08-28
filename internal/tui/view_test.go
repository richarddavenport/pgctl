package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

const uiConfig = `
protect: [prd]

environments:
  - name: qat
    guarded: true
  - name: prd
  - name: scratch

postgres:
  qat: { host: qat.example }
  prd: { host: prd.example }
  scratch: { host: 127.0.0.1 }

databases:
  - name: product-development

sets:
  - name: claims
    database: product-development
    description: claims and everything a claim points at
    include: ["claims.*"]
`

func model(t *testing.T) *Model {
	t.Helper()
	cfg, err := config.Parse([]byte(uiConfig), t.TempDir())
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.Storage.Dir = t.TempDir()
	m := New(engine.New(cfg))
	m.width, m.height = 120, 40
	return m
}

func press(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.Update(msg)
	}
}

func TestEmptyStateTellsYouWhatToDo(t *testing.T) {
	m := model(t)
	view := m.View()
	if !strings.Contains(view, "No snapshots yet") {
		t.Errorf("empty state does not explain itself:\n%s", view)
	}
	// Pressing apply with nothing to apply is a mistake the UI should absorb.
	press(t, m, "enter")
	if m.err == nil {
		t.Error("applying with no snapshots did not report anything")
	}
}

func TestProtectedEnvironmentIsRefusedAsATarget(t *testing.T) {
	m := withSnapshot(t)
	press(t, m, "enter") // choose the snapshot
	if m.stage != stageTargetEnv {
		t.Fatalf("stage = %v, want the target picker", m.stage)
	}

	view := m.View()
	if !strings.Contains(view, "protected") {
		t.Errorf("the target list does not mark prd protected:\n%s", view)
	}

	// prd is second in the list.
	press(t, m, "down", "enter")
	if m.stage != stageTargetEnv {
		t.Error("a protected environment was accepted as a target")
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "protected") {
		t.Errorf("err = %v, want a refusal naming protection", m.err)
	}
}

func TestGuardedEnvironmentDemandsItsNameTyped(t *testing.T) {
	m := withSnapshot(t)
	m.chosen = m.snapshots[0]
	m.target = "qat"
	m.plan = &engine.Plan{
		Snapshot:      m.snapshots[0],
		Target:        &engine.Target{Env: mustEnv(t, m, "qat"), Database: "product-development"},
		WholeDatabase: true,
		Selection:     []string{"claims.policy_claim"},
	}
	m.stage = stagePlan

	press(t, m, "enter")
	if !m.confirming {
		t.Fatal("a guarded target did not ask for confirmation")
	}
	if !strings.Contains(m.View(), `Type "qat" to confirm`) {
		t.Errorf("the prompt does not say what to type:\n%s", m.View())
	}

	// The wrong name is rejected, and the typing does not trigger shortcuts:
	// `q` would otherwise quit.
	press(t, m, "q", "a", "t", "x", "enter")
	if m.err == nil {
		t.Error("a mistyped name was accepted")
	}
	if m.confirmation != "" {
		t.Errorf("confirmation = %q, want it cleared after a failure", m.confirmation)
	}

	press(t, m, "esc")
	if m.confirming {
		t.Error("esc did not cancel the confirmation")
	}
}

func TestScopeOffersWholeDatabaseAndEachSet(t *testing.T) {
	m := withSnapshot(t)
	press(t, m, "enter")        // snapshot
	press(t, m, "down", "down") // scratch
	press(t, m, "enter")        // target
	if m.stage != stageScope {
		t.Fatalf("stage = %v, want the scope picker", m.stage)
	}

	view := m.View()
	for _, want := range []string{"the whole database", "claims", "claims and everything a claim points at"} {
		if !strings.Contains(view, want) {
			t.Errorf("scope view missing %q:\n%s", want, view)
		}
	}
}

func TestRunViewShowsTheLastEventsAndCancelHint(t *testing.T) {
	m := model(t)
	m.stage = stageRunning
	m.startedAt = time.Now()
	m.run = &run{kind: "snapshot prd"}
	for i := 0; i < 30; i++ {
		m.events = append(m.events, engine.Event{Kind: engine.EventStep, Step: "dump",
			Message: "step " + string(rune('a'+i%26))})
	}
	m.events = append(m.events, engine.Event{Kind: engine.EventWarning, Message: "rule matches no table"})

	view := m.View()
	if !strings.Contains(view, "rule matches no table") {
		t.Errorf("the newest event is not shown:\n%s", view)
	}
	if strings.Count(view, "→ dump:") > 12 {
		t.Error("the run view is not bounded to the last few events")
	}
	if !strings.Contains(view, "failure hooks still run") {
		t.Error("the footer does not explain what cancelling does")
	}
}

// withSnapshot gives the model one complete snapshot to work with.
func withSnapshot(t *testing.T) *Model {
	t.Helper()
	m := model(t)
	dir := snapshot.Path(m.engine.Config().Storage.Dir, "prd/product-development/20260828T030000Z")
	mkdir(t, dir)
	man := &snapshot.Manifest{
		ID:          "prd/product-development/20260828T030000Z",
		Environment: "prd",
		Database:    "product-development",
		StartedAt:   time.Date(2026, 8, 28, 3, 0, 0, 0, time.UTC),
		FinishedAt:  time.Date(2026, 8, 28, 3, 12, 0, 0, time.UTC),
		Bytes:       2_040_893_635,
		Tables:      []snapshot.TableEntry{{Name: "claims.policy_claim", Data: config.DataAll}},
	}
	if err := snapshot.Write(dir, man); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	m.reload()
	if len(m.snapshots) != 1 {
		t.Fatalf("reload found %d snapshots, want 1", len(m.snapshots))
	}
	return m
}

func mustEnv(t *testing.T, m *Model, name string) config.Environment {
	t.Helper()
	env, ok := m.engine.Config().LookupEnv(name)
	if !ok {
		t.Fatalf("no environment %q", name)
	}
	return env
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

func TestElapsedFormatting(t *testing.T) {
	// The old implementation trimmed a trailing "0s", so ten seconds rendered
	// as "1" and twenty as "2".
	now := time.Now()
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{
		{1 * time.Second, "0:01"},
		{10 * time.Second, "0:10"},
		{20 * time.Second, "0:20"},
		{90 * time.Second, "1:30"},
		{60 * time.Minute, "1:00:00"},
		{3*time.Hour + 4*time.Minute + 5*time.Second, "3:04:05"},
	} {
		if got := elapsed(now.Add(-c.ago)); got != c.want {
			t.Errorf("elapsed(%s ago) = %q, want %q", c.ago, got, c.want)
		}
	}
}

func TestRunningViewSaysWhatIsHappening(t *testing.T) {
	m := model(t)
	m.stage = stageRunning
	m.startedAt = time.Now().Add(-95 * time.Second)
	m.run = &run{
		kind:    "snapshot qat",
		explain: "Copying product-development from qat into .pgctl/snapshots. Nothing is written to qat.",
	}
	m.events = []engine.Event{{Kind: engine.EventStep, Step: "dump", Message: "writing to .pgctl/snapshots/qat"}}
	m.progress = engine.Event{Kind: engine.EventProgress, Step: "dump", Message: "505.0 MB written, 4.1 MB/s"}

	view := m.View()
	for _, want := range []string{
		// What it is doing, in words rather than in jargon.
		"Nothing is written to qat",
		// How long it has been doing it, correctly formatted.
		"1:35",
		// That it is still moving, and how fast.
		"505.0 MB written, 4.1 MB/s",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the running view does not show %q:\n%s", want, view)
		}
	}
}

func TestProgressRedrawsInPlace(t *testing.T) {
	// A byte counter must count, not scroll: a hundred near-identical progress
	// lines would push the steps that give them meaning off the screen.
	m := model(t)
	m.stage = stageRunning
	m.startedAt = time.Now()
	m.run = &run{kind: "snapshot qat"}

	for _, msg := range []string{"1.0 MB written", "2.0 MB written", "3.0 MB written"} {
		m.Update(eventMsg(engine.Event{Kind: engine.EventProgress, Step: "dump", Message: msg}))
	}
	if len(m.events) != 0 {
		t.Errorf("progress events were appended to the log: %v", m.events)
	}
	if m.progress.Message != "3.0 MB written" {
		t.Errorf("progress = %q, want the newest", m.progress.Message)
	}
	view := m.View()
	if strings.Contains(view, "1.0 MB written") {
		t.Error("a superseded progress line is still on screen")
	}
	if !strings.Contains(view, "3.0 MB written") {
		t.Error("the newest progress line is not on screen")
	}
}

func TestTickKeepsRedrawingOnlyWhileRunning(t *testing.T) {
	m := model(t)
	m.stage = stageRunning
	m.startedAt = time.Now()
	m.run = &run{kind: "snapshot qat"}

	// While running, a tick schedules the next one — this is what stops the
	// screen freezing at the last event for the length of a multi-minute dump.
	if _, cmd := m.Update(tickMsg(time.Now())); cmd == nil {
		t.Error("a tick during a run did not schedule the next one")
	}
	// Once it is over, the ticking stops rather than waking the process every
	// second forever.
	m.stage = stageSnapshots
	if _, cmd := m.Update(tickMsg(time.Now())); cmd != nil {
		t.Error("ticking continued after the run finished")
	}
}

func TestSpinnerAdvancesOneFramePerTick(t *testing.T) {
	// The spinner reads as flicker rather than rotation whenever the redraw
	// interval and the frame interval disagree: at one redraw a second and one
	// frame per 100ms it jumped ten frames between draws, landing back near
	// where it started. One tick must move it exactly one frame.
	now := time.Now()
	for i := range spinnerFrames {
		since := now.Add(-time.Duration(i) * tickInterval)
		if got, want := spinner(since), spinnerFrames[i]; got != want {
			t.Errorf("after %d ticks the spinner showed %q, want %q", i, got, want)
		}
	}
	// And it wraps rather than running off the end.
	if got := spinner(now.Add(-time.Duration(len(spinnerFrames)) * tickInterval)); got != spinnerFrames[0] {
		t.Errorf("the spinner did not wrap: got %q", got)
	}
}
