# Decisions

Each entry is a decision that shaped the tool, with the reasoning that produced
it. New decisions go at the bottom.

## 1. The unit of work is a snapshot, not a script run

The tooling this replaces (`tools/postgres/pg_dump.sh` and friends in the
MBPNetwork monorepo) has no artifact identity: it writes `$db.sql` into a
working directory and the operator remembers what it holds. Every question that
matters afterwards — which environment did this come from, when, at what schema
version, was `quotes.quote` in it — is unanswerable.

A **snapshot** is therefore a first-class, immutable, catalogued thing: source
environment, database, taken-at, server version, the rule set that produced it,
and a per-table manifest. Restores name a snapshot. The nightly produces one.
Retention deletes one.

## 2. Directory format, not custom format

`pg_dump -Fd` writes one file per table plus a table of contents.

Two consequences, both decisive:

- **`-j` only works with `-Fd`.** Parallel dump is unavailable in custom format,
  and single-threaded dump is the status quo we are trying to leave.
- **A full snapshot is already a per-table backup.** `pg_restore` can select any
  subset of a directory dump's tables. So "migrate these tables from prd to
  QAT" needs no special-purpose per-table dump — it reads the tables it wants
  out of last night's full snapshot. This collapses what looked like two
  features (nightly backup, table migration) into one artifact.

## 3. zstd, because the cluster is PostgreSQL 17

The default compression for `-Fc`/`-Fd` is gzip level 6, which is the slowest
part of a dump once parallelism is in play. PostgreSQL 16 added
`--compress=zstd:N`; 17 is what every MBPNetwork environment runs. zstd at a low
level is both faster and smaller than gzip-6 — there is no trade to make.

Recorded as a decision rather than a flag default because it sets the floor on
supported server versions: pgctl requires PostgreSQL 16 or newer.

## 4. Row-filtered tables are produced by COPY, and live in the same snapshot

`pg_dump` cannot dump part of a table. It has no row predicate and never has —
PostgreSQL 17's `--filter` selects *objects*, not rows. For a 20 GB table whose
useful test content is the last 30 days, whole-table or nothing is the only
choice pg_dump offers, and the monorepo scripts consequently choose nothing:
`--exclude-table-data 'quotes.quote'`.

So a table carrying a `where:` rule is excluded from the pg_dump invocation and
produced alongside it by `COPY (SELECT … WHERE …) TO STDOUT (FORMAT binary)`,
compressed with zstd and recorded in the snapshot manifest. Binary COPY, not
CSV: CSV is where the "the files are just huge" problem comes from — it renders
every value as text, and a jsonb column costs several times its stored size.

The snapshot format is therefore one directory with two kinds of producer
inside it, and `pgctl` — not `pg_restore` — is what knows how to apply it.

## 5. A selection is refused unless it is FK-closed

The recurring failure being designed out: restoring a set of tables whose
foreign keys point at tables that were not in the set, so the load fails (or
worse, succeeds against stale parents).

pgctl reads `pg_constraint` from the target and computes the **referential
closure** of the selected tables. A selection that is not closed is refused,
naming the missing tables, with an offer to widen the selection to the closure.
Ordering within a load is a topological sort of the same graph.

This is the tool's main claim to existing. Everything else is orchestration of
binaries the operator could invoke by hand; this is the part a human gets wrong.

## 6. Set-level applies rebuild constraints; whole-database applies do not

For a whole-database refresh, `pg_restore --clean --create` already does the
right thing: FKs and indexes are in the post-data section, so data loads into
bare tables and constraints arrive afterwards. `-j` parallelises both.

A set-level apply lands data into a database whose other tables are staying, so
that mechanism is unavailable. pgctl instead: drops the set's own FKs and
indexes, truncates in reverse topological order, loads in parallel, rebuilds
indexes, then re-adds each FK `NOT VALID` and `VALIDATE`s it as a separate
statement. Splitting the validation matters — `ADD CONSTRAINT` validating inline
holds a lock that keeps the application out for the whole scan, while
`VALIDATE CONSTRAINT` takes a weaker one.

Deliberately *not* using `session_replication_role = replica` or
`pg_restore --disable-triggers` to skip FK checking: both need privileges Azure
Database for PostgreSQL Flexible Server does not hand out to `azure_pg_admin`,
and a restore path that only works as superuser is a restore path that does not
work here.

## 7. Pre/post hooks, not a built-in "stop the services" step

A restore cannot proceed against a database with live connections —
`--clean --create` cannot drop it — so something must quiesce the target. The
monorepo does this today in `stop_services.sh`/`start_services.sh`: scale the
swarm services to zero, terminate the remaining backends, scale back up.

pgctl does not hardcode that. An apply has **hook points** — `preApply`,
`postApply`, and their failure counterpart — and the config supplies shell
commands to run at each. Terminating backends stays built in, because it is
about the database rather than about what is connected to it; scaling Docker
Swarm services is one project's answer to quiescing and belongs in config.

A failed `postApply` is reported but does not fail the apply — the data is
already in. A failed `preApply` aborts before anything is touched.

## 8. Environments are read from swarmctl.yaml when it is there

The environments pgctl needs — name, SSH host/port, sops-encrypted secrets file,
and which ones are guarded — are exactly what `swarmctl.yaml` already declares,
in the same repository. Restating them in `pgctl.yaml` creates two truths about
which host is prd, and the failure mode of them disagreeing is a restore
pointed at the wrong environment.

So `pgctl.yaml` declares databases, sets, table rules, storage and hooks, and
takes its environments from `environmentsFrom: swarmctl.yaml` (the default when
that file exists). Inline `environments:` is supported for projects with no
swarmctl.

The coupling is to a *stable subset* of swarmctl's schema — four fields — parsed
independently, not to swarmctl's Go types. pgctl does not import swarmctl.

## 9. Production is never a target, and guarded environments need typed consent

`guarded: true` in the environment config makes an apply require the
environment's name typed in full, in the TUI and headless alike. Production is
additionally refused as an apply target outright, at the lowest level in the
engine rather than in the front end, the same way `pg_restore.sh` refuses it
today. There is no flag to turn that off.

## 10. Masking is a hook point without rules, for now

Whether prd data may land in QAT unmasked is an open policy question at
MBPNetwork, not a technical one. Deciding it wrongly in either direction is
expensive: masking that is not required costs throughput on every refresh, and
skipping masking that is required is a disclosure.

The pipeline therefore has the seam in the right place — a per-table transform
applied as rows land — and ships with no rules and no masking. When the policy
lands, rules go in `pgctl.yaml` next to the `where:` predicates they resemble.

## 11. pgx for everything that is not pg_dump

`pg_dump` and `pg_restore` have to be subprocesses — there is no library form of
them, and reimplementing an archive format is not a thing to do. Everything
else talks to PostgreSQL through pgx rather than by shelling out to `psql`:
catalog introspection, the DDL an apply performs, and both directions of the
filtered `COPY`.

The deciding factor is `COPY`. pgx exposes the protocol-level copy in and out,
so a filtered table streams `COPY (SELECT …) TO STDOUT (FORMAT binary)` through
a zstd encoder and into a file without a temporary file, a shell pipeline, or a
psql `\copy` whose failures arrive as text on stderr. Doing that through `psql`
would mean parsing its output to find out whether it worked.

The secondary factor is credentials. A subprocess needs the password in its
environment; pgx takes it in a struct, and pgctl already has to redact pgx's
errors because they quote the connection string.

## 12. A snapshot stays a tree in blob storage, not a tarball

The obvious way to put a directory-format dump in object storage is to tar it
into one blob. That would throw away the property decision #2 was chosen for.

Kept as a tree — one blob per archive file, the snapshot's id as the prefix — a
set-level restore from blob storage downloads the table of contents and the
files for the tables it needs, and nothing else. Moving the claims tables out of
a 2 GB nightly costs the claims tables. Tarred, it costs 2 GB every time.

The price is many small blobs per snapshot and a listing that has to be
prefix-based. Both are what object storage is good at.
