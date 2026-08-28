package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/pg"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// Apply restores a snapshot onto a target. It plans first and executes the
// plan, so what runs is what an operator could have been shown.
//
// Two mechanisms, chosen by whether the whole database is being replaced:
//
//   - Whole database: pg_restore --clean --create, which drops and rebuilds
//     everything. Data lands in bare tables and constraints arrive afterwards
//     because that is how pg_dump sections an archive, and -j parallelises both.
//
//   - Some tables: the target keeps the tables that are staying, so that
//     mechanism is unavailable. pgctl drops the affected constraints and
//     secondary indexes itself, truncates children before parents, loads, then
//     rebuilds — validating each constraint as its own statement so the
//     application is not locked out for the length of a full scan.
func (e *Engine) Apply(ctx context.Context, req ApplyRequest, report Reporter) (err error) {
	plan, err := e.Plan(ctx, req, report)
	if err != nil {
		return err
	}
	return e.Execute(ctx, plan, report)
}

// Execute runs an already-computed plan. Separate from Apply so that a front
// end can show the plan, take a confirmation, and then run the same plan rather
// than recomputing one that may have changed underneath the confirmation.
func (e *Engine) Execute(ctx context.Context, plan *Plan, report Reporter) (err error) {
	if plan.Target.Env.Protected {
		return &RefusalError{fmt.Sprintf("%s is a protected environment", plan.Target.Env.Name)}
	}

	env := hookEnv(plan.Target.Env.Name, plan.Snapshot.Database, plan.Snapshot.ID)
	if err := e.runHooks(ctx, "preApply", e.cfg.Hooks.PreApply, env, true, report); err != nil {
		return err
	}

	defer func() {
		phase, hooks, fatal := "postApply", e.cfg.Hooks.PostApply, false
		if err != nil {
			phase, hooks = "onFailure", e.cfg.Hooks.OnFailure
		}
		// Hooks that bring an environment back up must run even when the
		// operation was cancelled, so they get a context of their own.
		hookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
		defer cancel()
		_ = e.runHooks(hookCtx, phase, hooks, env, fatal, report)
	}()

	dir := snapshot.Path(e.storageRoot(), plan.Snapshot.ID)
	if plan.WholeDatabase {
		err = e.applyWholeDatabase(ctx, plan, dir, report)
	} else {
		err = e.applyTables(ctx, plan, dir, report)
	}
	if err != nil {
		report.send(Event{Kind: EventFailed, Message: err.Error(), Err: err})
		return err
	}

	report.send(Event{Kind: EventDone, Message: fmt.Sprintf("applied %s to %s",
		plan.Snapshot.ID, plan.Target.Env.Name)})
	return nil
}

// applyWholeDatabase drops and recreates the database from the archive.
func (e *Engine) applyWholeDatabase(ctx context.Context, plan *Plan, dir string, report Reporter) error {
	target := plan.Target

	// Nothing can drop a database that has connections. Terminating them is
	// pgctl's own business — a hook can scale an application down, but only
	// this knows which backends are in the way.
	report.step("restore", "terminating connections to "+plan.Snapshot.Database)
	if err := e.terminateBackends(ctx, target, plan.Snapshot.Database, report); err != nil {
		return err
	}

	jobs := target.Jobs(e.cfg.Defaults)
	report.step("restore", fmt.Sprintf("pg_restore --clean --create, %d jobs", jobs))

	args := []string{
		fmt.Sprintf("--jobs=%d", jobs),
		// See runPgDump: pg_restore prompts on /dev/tty too, and with --create
		// it reconnects to the database it has just made — a second chance to
		// hang on authentication, long after anyone stopped watching.
		"--no-password",
		"--clean",
		"--if-exists",
		"--create",
		"--no-owner",
		"--no-acl",
		// The target's roles differ from the source's and Azure grants no
		// superuser, so ownership and privileges are the target's own business.
		"--host=" + target.Host,
		fmt.Sprintf("--port=%d", target.Port),
		"--username=" + target.User,
		"--dbname=" + target.MaintenanceDB(),
		filepath.Join(dir, snapshot.DumpDir),
	}
	if err := e.runPgRestore(ctx, target, args, report); err != nil {
		return err
	}

	return e.loadFilteredSidecars(ctx, plan, dir, plan.Snapshot.Filtered(), report)
}

// applyTables replaces a subset of a database's tables in place.
func (e *Engine) applyTables(ctx context.Context, plan *Plan, dir string, report Reporter) error {
	target := plan.Target

	conn, err := target.Connect(ctx, plan.Snapshot.Database)
	if err != nil {
		return err
	}
	defer conn.Close(ctx) //nolint:errcheck // nothing useful to do with a close failure

	if err := e.setTimeouts(ctx, conn); err != nil {
		return err
	}

	sequences, err := ownedSequences(ctx, conn, plan.Selection)
	if err != nil {
		return err
	}

	dropFKs := append(append([]FKDrop{}, asDrops(plan.DropFKs)...), asDrops(plan.BlockingFKs)...)

	// One transaction for the teardown: PostgreSQL's DDL is transactional, so
	// either every constraint and index is out of the way or none is, and a
	// failure here leaves the target exactly as it was found.
	report.step("prepare", fmt.Sprintf("dropping %d foreign keys and %d indexes",
		len(dropFKs), len(plan.DropIndexes)))
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	for _, fk := range dropFKs {
		stmt := fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s",
			quoteTable(fk.Table), quoteIdentifier(fk.Name))
		if _, err := tx.Exec(ctx, stmt); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("drop %s on %s: %w", fk.Name, fk.Table, err)
		}
	}
	for _, idx := range plan.DropIndexes {
		if _, err := tx.Exec(ctx, "DROP INDEX "+idx.Name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("drop index %s: %w", idx.Name, err)
		}
	}

	// Truncating children before parents inside the same transaction: the
	// foreign keys between selected tables are already gone, but the ones
	// pointing in from outside are only gone if they were blocking, and order
	// costs nothing to get right.
	truncate := plan.Order
	report.step("prepare", fmt.Sprintf("truncating %d tables", len(plan.Selection)))
	for i := len(truncate.Layers) - 1; i >= 0; i-- {
		layer := truncate.Layers[i]
		quoted := make([]string, len(layer))
		for j, t := range layer {
			quoted[j] = quoteTable(t)
		}
		if _, err := tx.Exec(ctx, "TRUNCATE TABLE "+strings.Join(quoted, ", ")); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("truncate %s: %w", strings.Join(layer, ", "), err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit teardown: %w", err)
	}

	// From here a failure leaves the target with data loaded and constraints
	// missing, which is why rebuilding is attempted even after a load failure.
	loadErr := e.loadSelection(ctx, plan, dir, sequences, report)

	rebuildErr := e.rebuild(ctx, conn, plan, dropFKs, report)
	if loadErr != nil {
		return loadErr
	}
	return rebuildErr
}

// loadSelection restores the selected tables' data: pg_dump's entries via
// pg_restore, and the filtered tables from their sidecars.
func (e *Engine) loadSelection(ctx context.Context, plan *Plan, dir string,
	sequences []string, report Reporter) error {

	archive := filepath.Join(dir, snapshot.DumpDir)
	entries, err := ReadTOC(ctx, archive)
	if err != nil {
		return err
	}

	// Filtered tables have no TABLE DATA entry — pg_dump was told to skip them
	// — so they are expected to be missing from the archive and loaded from
	// their sidecar instead.
	var fromArchive []string
	var sidecars []snapshot.TableEntry
	for _, name := range plan.Selection {
		entry, _ := plan.Snapshot.Table(name)
		switch entry.Data {
		case config.DataFiltered:
			sidecars = append(sidecars, entry)
		case config.DataNone:
			// Nothing to load: the table is left empty, which the plan warned
			// about.
		default:
			fromArchive = append(fromArchive, name)
		}
	}

	if len(fromArchive) > 0 {
		selected, missing := SelectData(entries, fromArchive, sequences)
		if len(missing) > 0 {
			return fmt.Errorf("snapshot %s has no data for %s", plan.Snapshot.ID, strings.Join(missing, ", "))
		}
		listPath, err := WriteRestoreList(dir, selected)
		if err != nil {
			return err
		}
		defer os.Remove(listPath) //nolint:errcheck // a leftover list file is harmless

		jobs := plan.Target.Jobs(e.cfg.Defaults)
		report.step("load", fmt.Sprintf("pg_restore %d entries, %d jobs", len(selected), jobs))
		args := []string{
			fmt.Sprintf("--jobs=%d", jobs),
			"--no-password",
			"--data-only",
			"--no-owner",
			"--no-acl",
			"--use-list=" + listPath,
			"--host=" + plan.Target.Host,
			fmt.Sprintf("--port=%d", plan.Target.Port),
			"--username=" + plan.Target.User,
			"--dbname=" + plan.Snapshot.Database,
			archive,
		}
		if err := e.runPgRestore(ctx, plan.Target, args, report); err != nil {
			return err
		}
	}

	return e.loadFilteredSidecars(ctx, plan, dir, sidecars, report)
}

// rebuild puts the indexes and constraints back.
//
// Indexes first: validating a foreign key scans the child table, and doing that
// before its indexes exist is a sequential scan per constraint.
func (e *Engine) rebuild(ctx context.Context, conn *pgx.Conn, plan *Plan,
	dropped []FKDrop, report Reporter) error {

	var failures []string

	report.step("rebuild", fmt.Sprintf("recreating %d indexes", len(plan.DropIndexes)))
	for _, idx := range plan.DropIndexes {
		if _, err := conn.Exec(ctx, idx.Def); err != nil {
			failures = append(failures, fmt.Sprintf("index %s: %v", idx.Name, err))
		}
	}

	report.step("rebuild", fmt.Sprintf("recreating %d foreign keys", len(dropped)))
	for _, fk := range dropped {
		// NOT VALID first, then VALIDATE as a separate statement. Adding a
		// constraint that validates inline holds a lock strong enough to keep
		// the application out for the whole scan; VALIDATE CONSTRAINT takes a
		// weaker one, so a large table stays readable and writable while its
		// history is checked.
		add := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s NOT VALID",
			quoteTable(fk.Table), quoteIdentifier(fk.Name), fk.Def)
		if _, err := conn.Exec(ctx, add); err != nil {
			failures = append(failures, fmt.Sprintf("add %s on %s: %v", fk.Name, fk.Table, err))
			continue
		}
		if fk.NotValid {
			// The source had it NOT VALID too. Validating it here would assert
			// something production does not.
			report.warn(fmt.Sprintf("%s on %s was already NOT VALID upstream; left unvalidated",
				fk.Name, fk.Table))
			continue
		}
		validate := fmt.Sprintf("ALTER TABLE %s VALIDATE CONSTRAINT %s",
			quoteTable(fk.Table), quoteIdentifier(fk.Name))
		if _, err := conn.Exec(ctx, validate); err != nil {
			// A validation failure is a finding, not a glitch: the data that
			// just landed does not support rows that were already there.
			failures = append(failures, fmt.Sprintf("validate %s on %s: %v", fk.Name, fk.Table, err))
		}
	}

	report.step("rebuild", "analyzing "+plural(len(plan.Selection), "table"))
	for _, t := range plan.Selection {
		// Without fresh statistics the planner is working from the old table's
		// shape, and the first queries after a refresh are the slow ones people
		// notice.
		if _, err := conn.Exec(ctx, "ANALYZE "+quoteTable(t)); err != nil {
			failures = append(failures, fmt.Sprintf("analyze %s: %v", t, err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("the data loaded but %d rebuild steps failed:\n  %s",
			len(failures), strings.Join(failures, "\n  "))
	}
	return nil
}

// FKDrop is a constraint pgctl removed and must put back.
type FKDrop struct {
	Name     string
	Table    string
	Def      string
	NotValid bool
}

func asDrops(fks []pg.FK) []FKDrop {
	out := make([]FKDrop, 0, len(fks))
	for _, fk := range fks {
		out = append(out, FKDrop{Name: fk.Name, Table: fk.Child, Def: fk.Def, NotValid: fk.NotValid})
	}
	return out
}

// runPgRestore invokes pg_restore, with the password out of argv.
func (e *Engine) runPgRestore(ctx context.Context, target *Target, args []string, report Reporter) error {
	cmd := exec.CommandContext(ctx, "pg_restore", args...)
	cmd.Env = target.SubprocessEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_restore: %w: %s", err, lastLines(string(out), 8))
	}
	report.send(Event{Kind: EventProgress, Step: "restore", Message: "pg_restore complete"})
	return nil
}

// terminateBackends disconnects everything else from a database.
func (e *Engine) terminateBackends(ctx context.Context, target *Target, database string, report Reporter) error {
	conn, err := target.Connect(ctx, target.MaintenanceDB())
	if err != nil {
		return err
	}
	defer conn.Close(ctx) //nolint:errcheck // nothing useful to do with a close failure

	const q = `SELECT count(pg_terminate_backend(pid))
	             FROM pg_stat_activity
	            WHERE datname = $1 AND pid <> pg_backend_pid()`
	var killed int64
	if err := conn.QueryRow(ctx, q, database).Scan(&killed); err != nil {
		return fmt.Errorf("terminate connections to %s: %w", database, err)
	}
	if killed > 0 {
		report.warn(fmt.Sprintf("terminated %d connection(s) to %s", killed, database))
	}
	return nil
}

// setTimeouts bounds lock waits on the session doing the DDL. A short
// lock_timeout is the difference between "the application is still holding this
// table" reported in seconds and an apply that hangs.
func (e *Engine) setTimeouts(ctx context.Context, conn *pgx.Conn) error {
	ms := e.cfg.Defaults.LockTimeout.Milliseconds()
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET lock_timeout = %d", ms)); err != nil {
		return fmt.Errorf("set lock_timeout: %w", err)
	}
	if st := e.cfg.Defaults.StatementTimeout; st > 0 {
		if _, err := conn.Exec(ctx, fmt.Sprintf("SET statement_timeout = %d", st.Milliseconds())); err != nil {
			return fmt.Errorf("set statement_timeout: %w", err)
		}
	}
	return nil
}

// ownedSequences returns the sequences the given tables own, so that their
// values travel with the data.
func ownedSequences(ctx context.Context, conn *pgx.Conn, tables []string) ([]string, error) {
	const q = `
SELECT DISTINCT sn.nspname || '.' || s.relname
  FROM pg_depend d
  JOIN pg_class s ON s.oid = d.objid AND s.relkind = 'S'
  JOIN pg_namespace sn ON sn.oid = s.relnamespace
  JOIN pg_class t ON t.oid = d.refobjid
  JOIN pg_namespace tn ON tn.oid = t.relnamespace
 WHERE d.deptype IN ('a', 'i')
   AND tn.nspname || '.' || t.relname = ANY($1::text[])
 ORDER BY 1`

	rows, err := conn.Query(ctx, q, tables)
	if err != nil {
		return nil, fmt.Errorf("read owned sequences: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
