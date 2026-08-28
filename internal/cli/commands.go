package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/richarddavenport/pgctl/internal/engine"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

func runSnapshot(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	configPath := fs.String("config", "", "path to a pgctl config")
	env := fs.String("env", "", "environment to snapshot")
	database := fs.String("db", "", "database to snapshot (default: every declared database)")
	verbose := fs.Bool("v", false, "report every table")
	noPush := fs.Bool("no-push", false, "keep the snapshot local even when remote storage is configured")
	_ = fs.Parse(Permute(fs, args))

	if *env == "" {
		return fmt.Errorf("--env is required")
	}
	e, err := load(*configPath)
	if err != nil {
		return err
	}

	databases := []string{*database}
	if *database == "" {
		databases = nil
		for _, d := range e.Config().Databases {
			databases = append(databases, d.Name)
		}
	}

	// One timestamp for every database in the run, so that a nightly of six
	// databases is one snapshot set rather than six unrelated ones.
	at := time.Now()
	report := printer(*verbose)
	for _, db := range databases {
		if _, err := e.Dump(ctx, engine.DumpRequest{
			Environment: *env,
			Database:    db,
			At:          at,
			NoPush:      *noPush,
		}, report); err != nil {
			return err
		}
	}
	return nil
}

func runList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	configPath := fs.String("config", "", "path to a pgctl config")
	env := fs.String("env", "", "only this environment")
	_ = fs.Parse(Permute(fs, args))

	e, err := load(*configPath)
	if err != nil {
		return err
	}
	entries, err := e.Index(ctx, printer(false))
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SNAPSHOT\tTAKEN\tTABLES\tSIZE\tWHERE\tSTATE") //nolint:errcheck // a tabwriter error surfaces on Flush
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		m := entry.Manifest
		if *env != "" && m.Environment != *env {
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

// applyFlags are shared by plan and apply, since a plan is the first half of an
// apply and taking different flags would make the preview a different question.
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

func (a *applyFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&a.config, "config", "", "path to a pgctl config")
	fs.StringVar(&a.to, "to", "", "environment to apply to")
	fs.StringVar(&a.set, "set", "", "restore only this table set")
	fs.StringVar(&a.tables, "tables", "", "restore only these tables (comma separated, schema-qualified)")
	fs.BoolVar(&a.widen, "widen", false, "include tables the selection references but does not name")
	fs.BoolVar(&a.verbose, "v", false, "report every table")
	fs.BoolVar(&a.yes, "yes", false, "skip the confirmation prompt")
	fs.StringVar(&a.confirm, "confirm", "",
		"name of the environment being written to, required for a guarded one")
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

func runPlan(ctx context.Context, args []string) error {
	var f applyFlags
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	f.bind(fs)
	_ = fs.Parse(Permute(fs, args))
	f.snapshot = fs.Arg(0)

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
	fmt.Print(plan.Describe())
	return nil
}

func runApply(ctx context.Context, args []string) error {
	var f applyFlags
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	f.bind(fs)
	_ = fs.Parse(Permute(fs, args))
	f.snapshot = fs.Arg(0)

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
	fmt.Print(plan.Describe())

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
//
// A guarded environment always needs its name typed, and --yes does not waive
// it: --yes exists so a routine refresh of a scratch environment is not a
// prompt, and a guarded environment is by definition not routine. In a
// non-interactive run the name comes from --confirm, which a CI job has to
// spell out in the workflow file where a reviewer can see it.
func confirm(plan *engine.Plan, f applyFlags) error {
	name := plan.Target.Env.Name
	if plan.Target.Env.Guarded {
		if f.confirm == name {
			return nil
		}
		if f.confirm != "" {
			return fmt.Errorf("--confirm %q does not name the target environment %q", f.confirm, name)
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

	if f.yes || !interactive() {
		return nil
	}
	fmt.Print("\nApply? [y/N] ")
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

func runPrune(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	configPath := fs.String("config", "", "path to a pgctl config")
	env := fs.String("env", "", "only this environment")
	apply := fs.Bool("apply", false, "actually delete; without it, prune only reports")
	_ = fs.Parse(Permute(fs, args))

	e, err := load(*configPath)
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

	groups, err := e.SnapshotGroups(ctx, *env, printer(false))
	if err != nil {
		return err
	}

	var deleted, freed int64
	for _, group := range groups {
		keep, remove := snapshot.Keep(group.Snapshots, policy)
		if len(remove) == 0 {
			continue
		}
		fmt.Printf("%s/%s: keeping %d, removing %d\n", group.Environment, group.Database, len(keep), len(remove))
		for _, m := range remove {
			fmt.Printf("  %s  %s  %s\n", m.ID,
				m.StartedAt.Local().Format("2006-01-02 15:04"), engine.HumanBytes(m.Bytes))
			freed += m.Bytes
			deleted++
			if *apply {
				if err := e.DeleteSnapshotEverywhere(ctx, m.ID); err != nil {
					return err
				}
			}
		}
	}

	switch {
	case deleted == 0:
		fmt.Println("nothing to prune")
	case *apply:
		fmt.Printf("removed %d snapshot(s), %s freed\n", deleted, engine.HumanBytes(freed))
	default:
		// Reporting by default rather than deleting by default: a retention
		// policy is easy to get wrong, and the run that discovers it should not
		// be the run that acts on it.
		fmt.Printf("%d snapshot(s), %s — re-run with --apply to delete\n", deleted, engine.HumanBytes(freed))
	}
	return nil
}
