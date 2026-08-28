package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/richarddavenport/pgctl/internal/engine"
)

// View renders the current stage.
func (m *Model) View() string {
	var b strings.Builder

	b.WriteString(m.header())
	b.WriteString("\n\n")

	switch m.stage {
	case stageSnapshots:
		b.WriteString(m.viewSnapshots())
	case stageSourceEnv:
		b.WriteString(m.viewEnvs("Take a snapshot of which environment?"))
	case stageTargetEnv:
		b.WriteString(m.viewEnvs("Apply " + m.chosen.ID + " to which environment?"))
	case stageScope:
		b.WriteString(m.viewScope())
	case stagePlan:
		b.WriteString(m.viewPlan())
	case stageRunning:
		b.WriteString(m.viewRunning())
	}

	if m.err != nil {
		b.WriteString("\n" + dangerStyle.Render("✗ "+m.err.Error()) + "\n")
	} else if m.status != "" {
		b.WriteString("\n" + okStyle.Render("✓ "+m.status) + "\n")
	}

	b.WriteString("\n" + footerStyle.Render(m.footer()))
	return b.String()
}

func (m *Model) header() string {
	source := m.engine.Config().Source
	if source == "" {
		source = "no config"
	}
	return titleStyle.Render("pgctl") + mutedStyle.Render("  "+source)
}

func (m *Model) viewSnapshots() string {
	if len(m.snapshots) == 0 {
		return mutedStyle.Render("No snapshots yet. Press n to take one.")
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("  %-46s %-17s %7s %6s", "SNAPSHOT", "TAKEN", "TABLES", "SIZE")))
	b.WriteString("\n")

	for i, s := range m.snapshots {
		state := ""
		if !s.Complete() {
			state = dangerStyle.Render("  INCOMPLETE")
		}
		line := fmt.Sprintf("  %-46s %-17s %7d %6s", s.ID,
			s.StartedAt.Local().Format("2006-01-02 15:04"), len(s.Tables), engine.HumanBytes(s.Bytes))
		b.WriteString(m.row(i, line) + state + "\n")
	}
	return b.String()
}

func (m *Model) viewEnvs(prompt string) string {
	var b strings.Builder
	b.WriteString(prompt + "\n\n")
	for i, env := range m.envs {
		note := ""
		switch {
		case env.Protected:
			note = dangerStyle.Render("  protected — never a target")
		case env.Guarded:
			note = warnStyle.Render("  guarded")
		}
		host := env.Server.Host
		if host == "" {
			host = "from " + env.Secrets.File
		}
		line := fmt.Sprintf("  %-10s %s", env.Name, mutedStyle.Render(host))
		b.WriteString(m.row(i, line) + note + "\n")
	}
	return b.String()
}

func (m *Model) viewScope() string {
	var b strings.Builder
	fmt.Fprintf(&b, "How much of %s?\n\n", m.chosen.Database)

	b.WriteString(m.row(0, "  "+dangerStyle.Render("the whole database")+
		mutedStyle.Render("  drop and recreate it")) + "\n")

	for i, set := range m.scopeSets() {
		desc := set.Description
		if desc == "" {
			desc = strings.Join(set.Include, ", ")
		}
		line := fmt.Sprintf("  %-16s %s", set.Name, mutedStyle.Render(desc))
		b.WriteString(m.row(i+1, line) + "\n")
	}

	if len(m.scopeSets()) == 0 {
		b.WriteString("\n" + mutedStyle.Render("No sets declared for this database — declare some in pgctl.yaml."))
	}
	return b.String()
}

func (m *Model) viewPlan() string {
	if m.plan == nil {
		return mutedStyle.Render("planning…")
	}
	body := boxStyle.Render(strings.TrimRight(m.plan.Describe(), "\n"))
	if m.confirming {
		return body + "\n\n" + dangerStyle.Render(
			fmt.Sprintf("Type %q to confirm: ", m.plan.Target.Env.Name)) + m.confirmation + "▌"
	}
	return body
}

func (m *Model) viewRunning() string {
	var b strings.Builder

	// A running operation must answer three questions without being asked:
	// what is happening, how long it has been happening, and whether it is
	// still alive. The elapsed time redraws every second, so a screen that has
	// stopped moving means something is genuinely wrong rather than merely
	// quiet.
	fmt.Fprintf(&b, "%s  %s\n", titleStyle.Render(m.run.kind), mutedStyle.Render(elapsed(m.startedAt)))
	fmt.Fprintf(&b, "%s\n\n", mutedStyle.Render(m.run.explain))

	for _, ev := range m.summariseEvents(10) {
		switch ev.Kind {
		case engine.EventStep:
			fmt.Fprintf(&b, "  %s %s\n", mutedStyle.Render("·"), ev.Message)
		case engine.EventTable:
			fmt.Fprintf(&b, "    %s %s\n", ev.Table, mutedStyle.Render(ev.Message))
		case engine.EventWarning:
			b.WriteString("  " + warnStyle.Render("! "+ev.Message) + "\n")
		case engine.EventDone:
			b.WriteString("  " + okStyle.Render("✓ "+ev.Message) + "\n")
		case engine.EventFailed:
			b.WriteString("  " + dangerStyle.Render("✗ "+ev.Message) + "\n")
		}
	}

	// The progress line redraws in place, so a byte counter counts rather than
	// scrolling a hundred near-identical lines past.
	if m.progress.Message != "" {
		fmt.Fprintf(&b, "\n  %s %s\n", accentStyle.Render(spinner(m.startedAt)), m.progress.Message)
	}
	return b.String()
}

// spinnerFrames turn in one direction at one dot per frame, so a dropped
// redraw looks like a pause rather than a reversal.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner picks its frame from the clock rather than from a counter, so it
// turns at a steady rate however often the view happens to be rebuilt.
func spinner(since time.Time) string {
	return spinnerFrames[int(time.Since(since)/tickInterval)%len(spinnerFrames)]
}

// row renders one list line, highlighted when the cursor is on it.
func (m *Model) row(i int, line string) string {
	if i == m.cursor {
		return selectedStyle.Render(strings.TrimRight(line, " "))
	}
	return line
}

func (m *Model) footer() string {
	switch m.stage {
	case stageSnapshots:
		return "↑/↓ move · enter apply · n new snapshot · r reload · q quit"
	case stageSourceEnv, stageTargetEnv:
		return "↑/↓ move · enter choose · esc back · q quit"
	case stageScope:
		return "↑/↓ move · enter plan · esc back · q quit"
	case stagePlan:
		if m.confirming {
			return "type the environment's name · enter confirm · esc cancel"
		}
		if len(m.plan.Added) == 0 && m.plan.Snapshot != nil {
			return "enter apply · w widen the selection · esc back · q quit"
		}
		return "enter apply · esc back · q quit"
	case stageRunning:
		return "q cancel (failure hooks still run)"
	}
	return "q quit"
}
