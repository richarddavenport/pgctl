# pgctl — functional inventory

What pgctl does, with no reference to its interface — the behaviour that has to
survive whatever the front end looks like. It began as the input to the
interface rebuild (decision 19) and is kept current after it, so a claim here
that the engine does not implement is a bug in this file.

## Configuration

- `pgctl.yaml` (optional; discovered upward from cwd, or `--config`). Every key
  optional — with no config, connect as `psql` with no arguments would.
- **Connections**: name → libpq DSN (service name, URI, or keyword pairs).
  Per-connection `jobs`. No credentials ever in config: `~/.pgpass`,
  `~/.pg_service.conf`, `PGPASSWORD`.
- **Flags**: `protected` (never an apply target, no override), `guarded` (apply
  requires the name typed in full).
- **Databases**: discovered from the server; `exclude` patterns (trailing `*`),
  `excludeSchemas`.
- **Defaults**: `jobs`, `compression` (zstd:3), `lockTimeout`.
- **Storage**: local directory plus optional Azure blob container (account and
  key from named environment variables), with a retention policy.
- **Sets**: named table selections scoped to a database, `include` glob patterns.
- **Rules** per table pattern: `where` (row filter) or `data: none`, each with a
  `why` recorded in the manifest.
- **Hooks**: `preApply`, `postApply`, `onFailure` — named shell commands with
  timeouts, given `$PGCTL_CONNECTION`, `$PGCTL_DATABASE`, `$PGCTL_SNAPSHOT`.
  `onFailure` and `postApply` run even when the operation was cancelled.

## Commands

- `snapshot --from <conn> [--db] [--to-storage <dest,…|all>]` — dump every
  declared database as one RUN. `--to-storage` is required once a remote is
  declared: there is no default destination.
- `ls [--from]` — snapshot runs on disk and in every remote, one row per run.
- `plan <snapshot> --to <conn> [--set|--tables] [--widen]` — what an apply would
  do; refuses rather than guesses.
- `apply <snapshot> --to <conn> [--db] [--set|--tables] [--widen]
  [--yes|--confirm <env>]` — one plan per database of the run, `--db` to narrow.
- `move --from --to [--db] [--set|--tables] [--widen] [--keep]` — snapshot plus
  restore; the staged snapshot is discarded unless `--keep`.
- `prune [--from] [--apply]` — reports by default, deletes with `--apply`.

Snapshot naming: a RUN id (`env/20260828T030000Z`) is the thing a command names
— every database taken at that instant, decision 26. `env/latest` is the newest
COMPLETE run of that connection, and a bare timestamp works when it is
unambiguous. A single-database id (`env/db/20260828T030000Z`) still resolves, as
a run of one, so a script that names one keeps working. Shell completion for
connection names and snapshot ids, config-only — a completion never probes a
server.

## Snapshotting

- `pg_dump` directory format, one file per table, zstd, parallel `--jobs` — so
  tables can be cherry-picked out of a full snapshot later; there is no separate
  per-table backup to remember to take.
- Tables with a `where` rule are excluded from the archive and produced as
  sidecar binary `COPY` files (`pg_dump` has no row predicate).
- Tables with `data: none` are dumped schema-only.
- Manifest per snapshot: schema version, id, connection, database, start and
  finish times, server and `pg_dump` versions, compression, jobs, excluded
  schemas, extensions, every table's fate (data mode, where, why, file, columns,
  source bytes and rows, loaded rows), the full foreign-key set, total bytes,
  warnings. An unfinished snapshot has no finish time and cannot be applied.
- Warns when a schema exclusion would produce an unrestorable archive —
  functions called by triggers on retained tables.
- Optional push to blob storage after a successful dump.

## Referential-integrity engine

- Builds a foreign-key graph from the **target's** catalog — what the load has
  to satisfy — not the source's.
- Computes the referential closure of a selection; refuses an unclosed selection
  and names the missing tables unless `--widen`.
- Condenses to strongly connected components and orders parents-first, so a
  schema with foreign-key rings still gets a complete plan. Self-referential
  foreign keys are ignored for ordering.
- Distinguishes foreign keys within the selection (must be dropped) from those
  pointing into it from outside (the reason a truncate fails).
- Reports drift: foreign keys that differ between the snapshot's source and the
  target.

## Planning and refusals

- A plan is computed before anything is touched and is what gets confirmed;
  execution runs that exact plan rather than recomputing one that may have
  changed underneath the confirmation.
- Refusals — a considered decision, distinct from a failure:
  - the target is protected;
  - the selection is not referentially closed and `--widen` was not given;
  - the snapshot did not finish;
  - the snapshot is missing a table the target has;
  - the target lacks an extension the source needs (checked against
    `pg_available_extensions`, since one the target could install is not a
    problem);
  - the target database does not exist, or the credentials do not work.
- Warnings: applying a snapshot back to the environment it came from;
  foreign-key drift.
- **Not a warning, and it should be:** a manifest saying a table is `filtered`
  whose sidecar file is absent fails MID-APPLY, when `copyTableIn` opens it —
  after that table has been truncated. `Plan` checks the snapshot for missing
  tables and does not check for missing sidecar FILES. This file claimed a
  warning here ("will load empty") that the engine has never emitted; the claim
  is gone, the gap is not.
- A guarded target requires its exact name via `--confirm`; `--yes` does not
  waive it.

## Applying

- **Whole database**: `pg_restore --clean --create`, parallel. Terminates the
  target database's own backends first. Ignores source ownership and privileges
  — target roles differ and no superuser is assumed.
- **Subset**: drop the affected foreign-key constraints and secondary indexes in
  one transaction; `DISABLE TRIGGER USER` on the loaded tables (a correctness
  requirement, not a speed optimisation — it prevents writing millions of audit
  rows and enqueuing an event per restored row); truncate children before
  parents; load; then rebuild indexes and re-add constraints `NOT VALID`,
  validating each as its own statement so the application is not locked out for
  the length of a full scan. Rebuilding is attempted even after a load failure.
- Loads `pg_dump` entries through a generated `pg_restore` restore list, plus the
  filtered tables from their sidecars.
- A set-level apply fetches only the archive files holding its tables; a
  whole-database apply needs all of them.
- `vacuumdb --analyze --jobs` after a whole-database restore — without it the
  first hours after a refresh run against a planner that knows nothing. Failure
  is reported loudly but does not fail the restore.
- `lockTimeout` is applied so a lingering application connection fails fast
  rather than hanging.

## Storage

- One store interface over a local directory and Azure blob: list, read
  manifest, put, get (whole snapshot or a named subset of files), delete — with
  per-file progress and parallel transfer.
- The index merges the local and remote catalogues; a delete can remove a
  snapshot from both.
- Retention: grandfather-father-son by calendar period (`daily`, `weekly`,
  `monthly`), keeping the newest snapshot of each period. An unset policy deletes
  nothing; an incomplete snapshot is never a keeper; the single newest snapshot
  is always kept, so a prune can run unattended.

## Inspection

- Probe a connection without acting on it: reachability (a failure comes back as
  state, not an error), server version, user, host, port, the databases on it
  with their on-disk sizes, and when the probe was taken.
- Live table list and catalogue for a connection and database; database name
  discovery; set membership, including which tables `--widen` would add.

## Cross-cutting

- Passwords are stripped from error text before it surfaces — pgx and libpq
  quote the connection string in some failures.
- Progress and events are reported through one reporter, so every front end
  renders the same run.
- Every refusal lives in the engine, never in a front end.
- Requires PostgreSQL 16 or newer, server and client tools both (zstd).
