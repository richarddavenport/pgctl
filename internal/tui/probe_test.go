package tui

import (
	"testing"

	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/tuikit/harness"
)

// Opening the interface reaches ONE server, not every declared one.
//
// Probing everything on startup meant opening the TUI opened a session to
// production — a real login against every declared server, audited, against a
// connection limit, on every launch. prd is protected at the engine level so
// that pgctl cannot touch it casually, and startup was doing it anyway.
//
// m.probing is what a probe in flight looks like, so counting it counts the
// connections pgctl has decided to open without running any of them.
func TestOpeningReachesOnlyTheSelectedEnvironment(t *testing.T) {
	m := fixtureModel(t)
	m.probes = map[string]*engine.Probe{} // the fixture pre-loads them
	m.probing = map[string]bool{}

	_ = m.Init()
	if len(m.probing) != 1 {
		t.Errorf("Init started %d probes (%v), want 1 — the selected environment",
			len(m.probing), keysOf(m.probing))
	}
	if conn, _ := m.selectedConn(); !m.probing[conn.Name] {
		t.Errorf("Init probed %v, not the selected %q", keysOf(m.probing), conn.Name)
	}
}

// Moving to an environment is what asks pgctl to reach it, and moving back does
// not ask twice.
func TestMovingToAnEnvironmentProbesItOnce(t *testing.T) {
	m := fixtureModel(t)
	m.probes = map[string]*engine.Probe{}
	m.probing = map[string]bool{}
	r := run(m, 132, 38)
	_ = m.Init()

	first, _ := m.selectedConn()
	harness.Press(r, "down")
	// A tick, because comp.List resolves a Move when it draws and the probe
	// starts on the frame after — see the tickMsg case in Update.
	m.Update(tickMsg(m.now))
	second, _ := m.selectedConn()
	if second.Name == first.Name {
		t.Fatal("down did not move the cursor")
	}
	if !m.probing[second.Name] {
		t.Errorf("moving to %q did not probe it", second.Name)
	}
	if len(m.probing) != 2 {
		t.Errorf("%d probes in flight (%v), want 2", len(m.probing), keysOf(m.probing))
	}

	// Back to the first. It is already in flight, so nothing new is asked for.
	harness.Press(r, "up")
	m.Update(tickMsg(m.now))
	if len(m.probing) != 2 {
		t.Errorf("moving back re-probed: %d in flight (%v)", len(m.probing), keysOf(m.probing))
	}
}

// r is the explicit "tell me about everything" — a reasonable thing to ask for
// and an unreasonable thing to do unasked.
func TestReloadProbesEverything(t *testing.T) {
	m := fixtureModel(t)
	m.probes = map[string]*engine.Probe{}
	m.probing = map[string]bool{}
	r := run(m, 132, 38)

	harness.Press(r, "r")
	if want := len(m.cfg.All()); len(m.probing) != want {
		t.Errorf("r started %d probes (%v), want all %d",
			len(m.probing), keysOf(m.probing), want)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
