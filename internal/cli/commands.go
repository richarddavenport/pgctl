package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/richarddavenport/tuikit/app"
	"github.com/richarddavenport/tuikit/harness"
	"github.com/richarddavenport/tuikit/spec"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/snapshot"
	"github.com/richarddavenport/pgctl/internal/tui"
)

func runSnapshot(ctx context.Context, c spec.Call) error {
	from, database := c.Flag("from"), c.Flag("db")
	if from == "" {
		return fmt.Errorf("--from is required")
	}
	e, err := load(c.Flag("config"))
	if err != nil {
		return err
	}

	databases := []string{database}
	if database == "" {
		// Every database the server has, since the config no longer claims to
		// know which exist.
		if databases, err = e.DatabaseNames(ctx, from); err != nil {
			return err
		}
	}

	dest, err := chosenDestinations(e, c.Flag("to-storage"))
	if err != nil {
		return err
	}

	// One timestamp for every database in the run, so that a nightly of six
	// databases is one snapshot set rather than six unrelated ones.
	at := time.Now()
	report := printer(c.Bool("verbose"))
	for _, db := range databases {
		if _, err := e.Dump(ctx, engine.DumpRequest{
			Connection:   from,
			Database:     db,
			At:           at,
			Destinations: dest,
		}, report); err != nil {
			return err
		}
	}
	return nil
}

// chosenDestinations resolves --to-storage.
//
// There is NO DEFAULT once a remote is declared, and that is the decision worth
// reading: a snapshot is gigabytes, and where it goes — a shared account a
// colleague restores from, an archive nobody prunes, or this laptop — is not
// something to infer from silence. With no remotes there is exactly one
// destination and nothing to ask about, so the flag is optional until the day a
// remote appears, and required from then on. `all` is the shorthand.
func chosenDestinations(e *engine.Engine, flag string) ([]string, error) {
	cfg := e.Config()
	if len(cfg.Remotes()) == 0 {
		if flag != "" && flag != config.LocalStorage && flag != "all" {
			return nil, fmt.Errorf("no storage remotes are declared in %s, so %q is "+
				"the only destination", cfg.Source, config.LocalStorage)
		}
		return []string{config.LocalStorage}, nil
	}

	if flag == "" {
		return nil, fmt.Errorf("--to-storage is required: %s declares %s. "+
			"Name one, several comma-separated, or all",
			cfg.Source, strings.Join(cfg.Destinations(), ", "))
	}
	if flag == "all" {
		return cfg.Destinations(), nil
	}

	var out []string
	for _, name := range strings.Split(flag, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if name != config.LocalStorage {
			if _, ok := cfg.RemoteByName(name); !ok {
				return nil, fmt.Errorf("no storage destination named %q — declared: %s",
					name, strings.Join(cfg.Destinations(), ", "))
			}
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--to-storage named nothing")
	}
	return out, nil
}

func runList(ctx context.Context, c spec.Call) error {
	from := c.Flag("from")
	e, err := load(c.Flag("config"))
	if err != nil {
		return err
	}
	entries, err := e.Index(ctx, printer(false))
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(c.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SNAPSHOT\tTAKEN\tTABLES\tSIZE\tWHERE\tSTATE") //nolint:errcheck // a tabwriter error surfaces on Flush
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		m := entry.Manifest
		if from != "" && m.Connection != from {
			continue
		}
		state := "complete"
		if !m.Complete() {
			state = "INCOMPLETE"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", m.ID, //nolint:errcheck // as above
			m.StartedAt.Local().Format("2006-01-02 15:04"), len(m.Tables),
			engine.HumanBytes(m.Bytes), entry.Location(), state)
	}
	return w.Flush()
}

// applyFlags is what plan and apply were given, read off the Call once so the
// rest of each command reads a struct rather than a map.
type applyFlags struct {
	config   string
	to       string
	set      string
	tables   string
	widen    bool
	verbose  bool
	yes      bool
	confirm  string
	snapshot string
}

// applyFlagsOf reads plan's and apply's shared flags. Their declaration is in
// tree.go — applyFlagSet — and the two have to name the same things, which is
// what having one declaration and one reader is for.
func applyFlagsOf(c spec.Call) applyFlags {
	return applyFlags{
		config:   c.Flag("config"),
		to:       c.Flag("to"),
		set:      c.Flag("set"),
		tables:   c.Flag("tables"),
		widen:    c.Bool("widen"),
		verbose:  c.Bool("verbose"),
		yes:      c.Bool("yes"),
		confirm:  c.Flag("confirm"),
		snapshot: c.Arg("snapshot"),
	}
}

func (a *applyFlags) request() engine.ApplyRequest {
	return engine.ApplyRequest{
		Snapshot: a.snapshot,
		Target:   a.to,
		Set:      a.set,
		Tables:   splitList(a.tables),
		Widen:    a.widen,
	}
}

func runPlan(ctx context.Context, c spec.Call) error {
	f := applyFlagsOf(c)
	e, err := load(f.config)
	if err != nil {
		return err
	}
	if err := f.validate(); err != nil {
		return err
	}

	plan, err := e.Plan(ctx, f.request(), printer(f.verbose))
	if err != nil {
		return err
	}
	fmt.Fprint(c.Out, plan.Describe()) //nolint:errcheck // a closed stdout is the caller's business
	return nil
}

func runApply(ctx context.Context, c spec.Call) error {
	f := applyFlagsOf(c)
	e, err := load(f.config)
	if err != nil {
		return err
	}
	if err := f.validate(); err != nil {
		return err
	}

	report := printer(f.verbose)
	plan, err := e.Plan(ctx, f.request(), report)
	if err != nil {
		return err
	}
	fmt.Fprint(c.Out, plan.Describe()) //nolint:errcheck // as in runPlan

	if err := confirm(plan, f); err != nil {
		return err
	}
	// Executing the plan that was displayed, rather than planning again: a
	// second plan could differ from the one that was confirmed.
	return e.Execute(ctx, plan, report)
}

func (a *applyFlags) validate() error {
	if a.snapshot == "" {
		return fmt.Errorf("name a snapshot (try `pgctl ls`, or <env>/latest)")
	}
	if a.to == "" {
		return fmt.Errorf("--to is required")
	}
	if a.set != "" && a.tables != "" {
		return fmt.Errorf("--set and --tables are alternatives")
	}
	return nil
}

// confirm gates a destructive apply.
func confirm(plan *engine.Plan, f applyFlags) error {
	return confirmConn(plan.Target.Conn, f.confirm, f.yes, "Apply?")
}

// confirmConn is the gate itself.
//
// A guarded environment always needs its name typed, and --yes does not waive
// it: --yes exists so a routine refresh of a scratch environment is not a
// prompt, and a guarded environment is by definition not routine. In a
// non-interactive run the name comes from --confirm, which a CI job has to
// spell out in the workflow file where a reviewer can see it.
func confirmConn(conn config.Connection, confirmFlag string, yes bool, question string) error {
	name := conn.Name
	if conn.Guarded {
		if confirmFlag == name {
			return nil
		}
		if confirmFlag != "" {
			return fmt.Errorf("--confirm %q does not name the target connection %q", confirmFlag, name)
		}
		if !interactive() {
			return fmt.Errorf("%s is guarded: pass --confirm %s", name, name)
		}
		fmt.Printf("\n%s is guarded. Type its name to continue: ", name)
		typed, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(typed) != name {
			return fmt.Errorf("not confirmed")
		}
		return nil
	}

	if yes || !interactive() {
		return nil
	}
	fmt.Printf("\n%s [y/N] ", question)
	typed, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if s := strings.ToLower(strings.TrimSpace(typed)); s != "y" && s != "yes" {
		return fmt.Errorf("not confirmed")
	}
	return nil
}

func interactive() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func runPrune(ctx context.Context, c spec.Call) error {
	from, apply := c.Flag("from"), c.Bool("apply")
	e, err := load(c.Flag("config"))
	if err != nil {
		return err
	}
	policy := snapshot.Policy{
		Daily:   e.Config().Storage.Retention.Daily,
		Weekly:  e.Config().Storage.Retention.Weekly,
		Monthly: e.Config().Storage.Retention.Monthly,
	}
	if policy.Unset() {
		return fmt.Errorf("no storage.retention configured, so there is nothing to prune")
	}

	groups, err := e.SnapshotGroups(ctx, from, printer(false))
	if err != nil {
		return err
	}

	var deleted, freed int64
	for _, group := range groups {
		keep, remove := snapshot.Keep(group.Snapshots, policy)
		if len(remove) == 0 {
			continue
		}
		fmt.Printf("%s/%s: keeping %d, removing %d\n", group.Connection, group.Database, len(keep), len(remove))
		for _, m := range remove {
			fmt.Printf("  %s  %s  %s\n", m.ID,
				m.StartedAt.Local().Format("2006-01-02 15:04"), engine.HumanBytes(m.Bytes))
			freed += m.Bytes
			deleted++
			if apply {
				if err := e.DeleteSnapshotEverywhere(ctx, m.ID); err != nil {
					return err
				}
			}
		}
	}

	switch {
	case deleted == 0:
		fmt.Println("nothing to prune")
	case apply:
		fmt.Printf("removed %d snapshot(s), %s freed\n", deleted, engine.HumanBytes(freed))
	default:
		// Reporting by default rather than deleting by default: a retention
		// policy is easy to get wrong, and the run that discovers it should not
		// be the run that acts on it.
		fmt.Printf("%d snapshot(s), %s — re-run with --apply to delete\n", deleted, engine.HumanBytes(freed))
	}
	return nil
}

func runMove(ctx context.Context, c spec.Call) error {
	source, to := c.Flag("from"), c.Flag("to")
	database, set, tables := c.Flag("db"), c.Flag("set"), c.Flag("tables")
	widen, keep, verbose := c.Bool("widen"), c.Bool("keep"), c.Bool("verbose")

	if source == "" || to == "" {
		return fmt.Errorf("--from and --to are both required")
	}
	e, err := load(c.Flag("config"))
	if err != nil {
		return err
	}

	// A guarded target is confirmed once, before any work: a move takes a
	// snapshot first, and asking after that has spent the time is asking too
	// late to be a choice.
	conn, ok := e.Config().Lookup(to)
	if !ok {
		return fmt.Errorf("unknown connection %q", to)
	}
	if err := confirmConn(conn, c.Flag("confirm"), c.Bool("yes"),
		fmt.Sprintf("Refresh %s from %s?", to, source)); err != nil {
		return err
	}

	databases := []string{database}
	if database == "" {
		if databases, err = e.DatabaseNames(ctx, source); err != nil {
			return err
		}
	}

	report := printer(verbose)
	for _, db := range databases {
		if err := e.Move(ctx, engine.MoveRequest{
			From:     source,
			To:       to,
			Database: db,
			Set:      set,
			Tables:   splitList(tables),
			Widen:    widen,
			Keep:     keep,
		}, report); err != nil {
			return err
		}
	}
	return nil
}

// runBrowse opens the interface, or captures it.
//
// The whole of what pgctl writes to get --snapshot and --script. Decision 10:
// two capture mechanisms, deliberately — the test helper reaches any state at
// all because it can touch unexported fields, and this reaches what a keystroke
// reaches and needs no test. The difference that matters is that an agent finds
// this one from --help.
func runBrowse(_ context.Context, c spec.Call) error {
	m, err := tui.Open(c.Flag("config"))
	if err != nil {
		return err
	}

	dir := c.Flag("snapshot")
	if dir == "" {
		return tui.Run(m)
	}

	// An empty --script is "just the first frame", not a file called "".
	// harness.ScriptFile reads a path when what it is given is not obviously a
	// script body, so handing it the empty string is an os.ReadFile("") and an
	// error that says `open : no such file or directory`. tuikit issue 89 — the
	// generated scaffold has the same line, so this goes when that is fixed.
	var script string
	if path := c.Flag("script"); path != "" {
		script, err = harness.ScriptFile(path)
		if err != nil {
			return err
		}
	}
	// The world, read here rather than in Init: a capture never runs a tea.Cmd,
	// so a model that loads asynchronously captures the screen from before its
	// data arrived — harness.LoadsBeforeCapture is the check that reports it,
	// and this is what makes the check unnecessary.
	m.LoadNow()

	// Wrapped in a runner because the model has no View: the runner owns the
	// canvas, so it is what the harness drives. No pixel layer — a snapshot is
	// capturing frames for documentation and must record the characters,
	// whatever terminal it was started from.
	frames, err := harness.Snapshot(app.New(m, app.WithChrome(tui.Chrome)), dir, script)
	if err != nil {
		return err
	}
	for _, f := range frames {
		fmt.Fprintln(c.Out, filepath.Join(dir, f.File)) //nolint:errcheck // as above
	}
	return nil
}
