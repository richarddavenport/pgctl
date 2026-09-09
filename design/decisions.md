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

## 8. A connection is a libpq DSN, and nothing else

*Superseded the original decision, which read environments out of swarmctl's
config. That was wrong twice over: it tied a database tool to a deploy tool, and
it invented a credential scheme PostgreSQL already has.*

PostgreSQL has a complete, standard description of how to reach a server:
connection strings, `~/.pg_service.conf`, `~/.pgpass`, and the `PG*` variables.
libpq implements it, pgx implements the same resolution, and `pg_dump` is libpq.
So a connection in `pgctl.yaml` is a DSN and two safety flags:

    connections:
      prd: { dsn: "service=prd", protected: true }
      qat: "service=qat"

pgctl resolves that string with pgx for its own queries and hands the identical
string to `pg_dump` and `pg_restore` — appending `dbname=` to reach one database
of many. Both halves of an operation therefore connect by exactly the same
rules, which is not true of any scheme that reads credentials itself and passes
them on.

What this deletes is the point of it: an `environmentsFrom` key, a `credentials`
block mapping four config keys to four environment-file keys, a `postgres` map
of hosts, sops decryption, and a `Secrets` type. None of it was doing anything
`.pgpass` does not do, and all of it was something to get wrong.

The cost is real and worth stating: connection details become per-machine rather
than shared through the repository, so each person sets up a service file once
and CI writes a `.pgpass` from its secret store. That is the same setup every
other PostgreSQL client on those machines already needs.

## 8a. Databases are discovered, not declared

The same principle one level down. A server knows which databases it has; a list
in a config file can only ever disagree with it, and the disagreement shows up
as a database greyed out for reasons nobody remembers. `databases.exclude` trims
what is not interesting, and everything else is fair game — including the one
somebody added last week.

## 8b. The service file stays at libpq's default — tried elsewhere, reverted

The service file is `~/.pg_service.conf`, where libpq looks for it. pgctl does
not move it, does not set `PGSERVICEFILE`, and has no opinion about it.

**This entry records an attempt and its reversal**, because the attempt looked
reasonable and the reasoning against it is the useful part.

For about an hour the file lived at `~/.config/pgctl/pg_service.conf`, with
pgctl setting `PGSERVICEFILE` once in `main` so that pgx and the `pg_dump`
subprocesses resolved it from the same place. That worked — verified on all
three paths — and it did not violate decision 8 in the way a pgctl-shaped
connection schema would have: the file was still a libpq service file, in
libpq's format, parsed by libpq, and `PGSERVICEFILE` is libpq's own way of
saying where one lives.

It was still wrong, for the reason decision 8 exists:

- **It broke `psql`.** `psql service=prd` stopped working, because psql reads
  only libpq's default. The whole point of a service file is that every
  PostgreSQL tool on the machine shares one set of definitions; a location only
  pgctl knows about is a second scheme wearing the first one's file format.
- **The fix for that was a shell export**, which is a thing every person and
  every CI job has to remember — the same class of problem as distributing the
  file, now applied to knowing where it is.
- **libpq already solves relocation.** Anyone who wants the file elsewhere sets
  `PGSERVICEFILE` themselves. pgctl adding a default did not add a capability;
  it only disagreed with the ecosystem about the default.

So it is deleted, which is what has happened to every parallel scheme pgctl has
invented — and this one was barely parallel, just relocated. `Home()` and the
`config.yaml` search path keep the change that came with it, because that part
was a bug rather than a preference: the path used `os.UserConfigDir()`, which
answers `~/.config` on Linux and `~/Library/Application Support` on macOS, so
pgctl was looking for a hand-edited file where a GUI keeps its state. It is
`$XDG_CONFIG_HOME/pgctl` or `~/.config/pgctl` on both now.

**What would have to change to reverse this again.** libpq gaining an XDG
search path of its own — at which point pgctl would not have to do anything,
which is the point.

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

## 13. A set-level load disables the tables' user triggers

Measured, not theorised: loading `claims.policy_claim` into a live table ran at
**33 rows a second**. The table carries twelve user triggers, `COPY` fires row
triggers, and two of this project's are written in plv8 — a JavaScript
interpreter invoked per row.

Speed is the smaller half of it. A whole-database restore never meets this
problem, because `pg_dump` puts triggers in the post-data section and the data
lands before they exist. A set-level load goes into tables that are staying,
with their triggers already in place, so a refresh would:

- write a row into `audit.logged_actions` for every restored row — the table the
  rules deliberately excluded from the snapshot for being useless in another
  environment;
- enqueue a Hasura event for every restored row, through the `notify_hasura_*`
  triggers, turning a data refresh into millions of outbound events.

So pgctl runs `ALTER TABLE … DISABLE TRIGGER USER` in the teardown transaction
and re-enables it during the rebuild. `DISABLE TRIGGER USER` rather than
`session_replication_role = replica` or `pg_restore --disable-triggers`: those
need superuser, which Azure Database for PostgreSQL does not grant, while this
needs only ownership of the table. It also leaves the internal constraint
triggers alone, which is the right scope — pgctl manages the foreign keys
itself, explicitly.

Only triggers that are currently enabled are recorded, so re-enabling never
switches on something an operator had deliberately turned off.

## 14. "Direct" env-to-env keeps the artifact, and keeps parallelism

The obvious shape for "refresh QAT from prd now" is a pipe:

    pg_dump … | pg_restore …

It is wrong for anything large, and the reason is decision #2. A pipe requires
custom format, because a directory archive is a directory and cannot be written
to stdout. Custom format cannot be dumped in parallel and, streamed, cannot be
restored in parallel either — a pipe has no random access, so `pg_restore -j`
has nothing to work with. So the pipe trades away the single biggest speed win
in exchange for not touching a disk.

`pgctl move --from prd --to qat` therefore does the same thing an operator would
do by hand, and does it in one command: take a snapshot into a temporary
directory with `-j`, apply it with `-j`, and delete it afterwards unless asked
to keep it. No snapshot is catalogued, nothing is uploaded, and both ends run at
full width.

What that costs is disk on the machine in the middle, sized to the compressed
snapshot. What it buys, on a 2 GB archive with four jobs, is most of a factor of
two — and more on a server with more cores.

The pipe remains the right answer in one case: a small selection of tables where
the whole operation is over before parallelism would have paid for itself. That
is not the case worth building first.

## 15. A restore collects statistics; it does not vacuum

`pg_restore` leaves a database with no planner statistics. Nothing collects them
until autovacuum happens by, so until this was fixed every whole-database
refresh handed back an environment whose planner knew nothing about the data it
had just received — measured as 173 tables with rows and no statistics on a
database an earlier pgctl had restored. The first hours after a refresh were
slow for a reason nobody would have connected to the refresh.

So an apply now finishes by collecting statistics, with
`vacuumdb --analyze-only --jobs=N`: it ships with the same client tools as
pg_dump, and it analyses tables concurrently where a single `ANALYZE` statement
works through them one at a time — 7 seconds for 212 tables. A failure warns
rather than failing the restore, because the data is in and correct and
statistics can be collected afterwards.

It stops there. It does not vacuum, and pgctl has no `vacuum` command, because
autovacuum is on and does that job — a command people run by hand would mostly
be a way to add I/O at a bad moment. Where pgctl has something to add is
reporting the maintenance state nobody looks at until it is urgent, and the one
job only it knows to do: acting on the moment a restore just finished. The
candidates, with what is measured and what is guessed, are in
[maintenance.md](maintenance.md).

## 16. One tool, with a read-only boundary rather than a second tool

The enumeration in [dba-surface.md](dba-surface.md) is about a hundred and
twelve tasks against pgctl's six verbs, which raised a fair question: is
database administration a second tool?

It is not, for three reasons. The connection model — DSNs, discovery, guarded
and protected — is the expensive shared part, and a second tool would need all
of it and then drift from it. The overlap points are real rather than
incidental: a restore is when statistics must be collected, a migration is when
lock analysis matters, and restore verification is simultaneously a backup
concern and a health check — split the tools and those fall in the gap between
them. And swarmctl is the precedent: it is not one narrow verb but the thing you
operate a swarm with, so a single broad operator tool per domain is the house
shape.

The boundary that matters is not two binaries. It is **reading against
changing**, inside one:

- The reporting surface cannot mutate, by construction, and must work through a
  PostgreSQL role holding no write privileges at all. That makes it safe on a
  cron, in CI, and in the hands of somebody who should not be given a tool that
  can drop a database — and safe regardless of a bug in pgctl.
- Everything destructive keeps the machinery it already has: a plan shown
  before it runs, guarded targets that need their name typed, protected ones
  that can never be a target.

## 17. pgctl reports; it does not sample

No daemon, no metrics table, no background collection. pgctl reads the current
state when asked and prints it.

This is affordable because most of what looked like it needed history does not,
and the rest is already being retained by something else:

- **Sizes, bloat, wraparound age, invalid indexes, comment coverage,
  configuration values, grants, replication lag** are facts about now. There was
  never a series to keep.
- **What is expensive** is answered by `pg_stat_statements`, whose counters are
  cumulative since reset. That is enough for "what costs the most", which is the
  question actually asked.
- **What happened at a particular time** is in the PostgreSQL log, which records
  slow statements, lock waits, autovacuum runs and deadlocks with timestamps —
  when `log_min_duration_statement`, `log_lock_waits` and
  `log_autovacuum_min_duration` are set. That is configuration, not code.
- **Which query got slower** is Azure's Query Store, which already stores query
  performance in time windows on Flexible Server.
- **CPU, IOPS, storage and connection trends** are Azure Monitor, which has been
  sampling all along.

So the useful question is not "should pgctl store history" but "is the retention
that already exists switched on" — which a configuration report answers, and
which needs nothing built.

What this deliberately gives up is narrow: per-table growth forecasting, and
configuration drift as a series rather than a diff. Neither justifies a storage
design, a schema to migrate, or a process that has to be running.

**What would have to change to reverse this.** A requirement for sub-minute
sampling, or for state held between runs that no PostgreSQL or Azure facility
retains. Wanting a nightly report is not that: a scheduler running the CLI
covers it without anything long-lived. This decision exists mainly to be read
by whoever later proposes "just a small metrics table".

## 18. The interface is built on tuikit, migrated rather than restarted

**Superseded in part by decision 19**, which restarted it, and by decision 20,
which removed the `replace`. The reasoning below is what produced both, so it
stays.

pgctl is one of the four tools tuikit was extracted from, so the question was
never whether the framework fits — its `harness` package comment names this
repo's `screenshot_probe_test.go` as the prototype it generalises. The question
was whether to run `tuikit new` and backfill, or migrate in place.

**Migrate in place.** `tuikit new` generates the structure that azctl's
migration turned out to need, and pgctl already had all of it: `internal/`
split into `engine`, `tui` and `cli`; an engine with no terminal imports, which
`guard.Engine` passed on five packages unedited; a CLI that is a peer over the
engine rather than a wrapper around it; and a **pointer** model, which the
tuikit README calls out as the one change azctl had to make in every file.
Starting over would have re-earned the three layout bugs the probe tests had
already found and fixed, and thrown away 3,286 lines that work.

The scaffold is still the donor. One was generated into a scratch directory and
read, and what pgctl lacked was lifted from it: the palette and glyph set, the
three `guard_test.go` files, the workflows, `install.sh`. What pgctl already had
was left alone.

**What this bought, in the order it arrived.** The colour roles stop being
seven hex pairs and become the terminal's own ANSI indices, so pgctl is themed
by whatever themed the terminal — decision 28 in tuikit argues this out. The
glyph set is closed, which found four characters pgctl printed that a terminal
font may not have. And a fixture plus `harness.Golden` made the frames visible
without a database, which found four layout bugs in one run, including one — the
detail pane two lines taller than the terminal — that had pushed the footer off
the bottom of every screen pgctl had ever drawn.

**What it costs.** tuikit is private and untagged, so `go.mod` resolves it
through `replace ... => ../tuikit`: building pgctl needs a sibling checkout, CI
checks out two repositories with a PAT, and `go install` does not work. All of
that goes when tuikit is tagged. The `replace` is the whole of the coupling —
there is no vendored copy and no fork.

**What would have to change to reverse this.** tuikit going unmaintained while
pgctl still needs to ship, or a screen pgctl needs that `comp` actively gets in
the way of. Neither is a reason to fork: the components are ordinary Go, and a
tool that outgrows one can draw that screen itself, which is what `theme` and
`guard` exist to keep honest either way.

## 19. The interface was restarted on the scaffold, not migrated again

Decision 18 said the opposite, and it was right at the time. This supersedes its
conclusion and keeps its reasoning: the migration in place was the cheap way to
get pgctl onto tuikit, and it left the interface a *port* — five panels, a tab
strip, modal forms and a run log, all of them pgctl's own shapes with components
substituted underneath where a component happened to fit.

**Restarted.** `tuikit new pgctl` was generated into a scratch directory and its
`internal/tui` was the starting point rather than the donor. The old interface
moved to `_attic/tui`, where Go does not compile it — a directory whose name
starts with `_` is invisible to the toolchain — and its 38 goldens are the
reference for what the rebuilt one has to be able to say.

**What changed, and none of it is cosmetic.** The key routing is `app.Keys`, so
the order that makes a filter box typeable is the shape of a struct rather than
an early return anybody can delete. A run is `comp.StepList` over the engine's
own event stream, so the phases are the engine's account of what it is doing and
a phase added to `apply.go` appears on screen without an edit here — the flat
log it replaces was 60 lines of this package deciding what a phase looked like.
The state glyphs are `Row.Status`, a column that keeps its colour under the
selection, which is tuikit decision 46's distinction and not something the port
could have expressed. `ctrl+p` lists every operation with the key that runs it
*and the reason a refused one cannot*, which is the question this tool's readers
ask most and previously got answered only by pressing the key. And the filter
and the form's text fields have a caret you can move, which the hand-drawn `▏`
could never be.

**What it cost.** The 38 goldens were rewritten, and every one of them had to be
read rather than accepted. Two behaviours went deliberately: `q` no longer
means "cancel the run" — tuikit reserves it for leaving (its decision 42) — so a
run in flight puts a question in front of it and answering the question is what
cancels; and the plan preview is no longer `Plan.Describe()` reflowed, it is
built from the `Plan`'s fields, because the CLI's description is written for a
terminal that has already scrolled.

**What would have to change to reverse this.** Nothing plausible. The reason
decision 18 could be reversed cheaply is that the engine was never involved: the
whole restart is one package, and `guard.Engine` still passes on the five that
matter.

## 20. tuikit is a dependency, not a directory

Decision 18's "what it costs" paragraph — a sibling checkout, two
`actions/checkout` steps, a PAT, and `go install` not working — was all one
cause: tuikit was private and untagged, so `go.mod` resolved it through
`replace github.com/richarddavenport/tuikit => ../tuikit`.

tuikit went public and tagged `v0.1.1` on 2026-09-08, so the `replace` is gone
and it is an ordinary `require`. What that deleted, in one commit: the relative
path in `go.mod`, the second checkout in both workflows, the `TUIKIT_TOKEN`
secret CI could not run without, the `working-directory: pgctl` every step
needed because pgctl could not be checked out at the workspace root, and the
paragraph in `AGENTS.md` telling an agent it needed two repositories.

**What it costs.** A version to bump. `tuikit news` was the mechanism for
noticing that a pull in `../tuikit` had changed this tool's behaviour with
nothing to read; now there is something to read and something to bump, which is
the ordinary arrangement. Developing both together is `go work` or a temporary
`replace`, and neither is committed.

## 21. Storage is a list of named destinations, and none of them is a default

Decision 12 said a snapshot stays a tree in blob storage rather than a tarball,
and that is what this builds on: a tree can be listed, partially downloaded, and
held in more than one place without any of the copies knowing about the others.
What decision 12 did not say is how many places there are, and the answer was
one — `storage.kind: azureblob` with a container and a credential beside it.

**A list.** `storage.remotes` is named destinations, each with its own container,
its own credentials and its own retention. The local directory is not one of
them: `pg_dump` writes a directory, so every snapshot begins on this machine.

**Nothing is default.** `pgctl snapshot` requires `--to-storage` once a remote
is declared, and the interface offers a toggle per destination with none ticked.
Asked for, and the reason is worth keeping: *"It's really for flexibility. We
don't exactly know when we will use one over the other."* A destination chosen
by the config is a destination chosen once, months ago, by someone deciding a
different question — and the cost of guessing wrong is gigabytes in a shared
account, or a snapshot a colleague cannot reach because it stayed on a laptop.
With no remotes declared there is one destination and nothing to ask, so the
flag is optional until the day a remote appears.

**Which makes "not local" expressible for the first time.** A snapshot placed
only in a remote is uploaded and then deleted from disk, in that order, with the
delete conditional on every upload having succeeded — so a failure leaves the
local copy, which is the recoverable state. That is the answer to a laptop
accumulating 1.9 GB nightlies, and the old `--no-push` bool could not say it.

**Retention had to follow.** One policy for every destination deleted the
archive's only copy on the day the local copy aged out, because `prune` ran a
single policy and then called `DeleteSnapshotEverywhere`. Each destination now
carries its own and prune walks them one at a time, deleting only from the one
whose policy it is holding. A destination with no policy keeps everything and
prune says so, because silence is indistinguishable from a policy that found
nothing to do.

**No compatibility shim.** `storage.kind` and its neighbours are refused by name
with the shape to write instead. A config half-migrated is worse than one that
will not load: it would keep pushing to the destination it always did while the
interface offered a list that did not include it. `Storage` keeps the five old
fields for exactly that error, and `TestTheOldStorageKeysAreRefusedWithTheNewShape`
holds it closed.

**What would have to change to reverse this.** Nothing about the mechanism — a
single-remote config is a list of one, and that is how most of them will read.
The part with a cost is the missing default, which is a keystroke and a flag on
every snapshot forever; if that becomes the complaint rather than the safety,
`default: true` on a destination is the smaller half of this decision to give
back, and the list stays.

## 22. A rule may wildcard the schema, and a bare name still may not

The pattern language was `schema.table` with a `*` allowed at either end of the
table part only, and the reasoning was written into the test: *"A schema is
never implied: the same table name in another schema is a different table, and
truncating the wrong one is unrecoverable."*

That is a good rule about NAMES and it was applied to wildcards as well, which
is where it stopped being right. The first real config wanted the opposite on
its first day:

```yaml
  - table: "*as400*"
    data: none
    why: "we don't care about backing up anything that syncs to the as400"
```

Against the live catalogue that is **nine tables across five schemas** —
claims, operations, ory, public and shared. Five rules cover them and stop
covering them the day a sixth schema gains one, silently: the Rules tab flags a
rule that matches NOTHING, and nothing can flag a schema for which nobody wrote
a rule. The pattern the operator wrote is both what they meant and more correct
than the workaround.

**So either half may be wildcarded**, and a pattern with no `.` in it spans
every schema. `*.policy` says it explicitly for an exact name.

**A bare exact name is still refused**, which is the half of the old reasoning
that survives: `policy` is a table somebody forgot to qualify. Nobody writes
`*as400*` meaning one schema, and everybody who writes `policy` means one —
that asymmetry is the whole decision, and it is why this is not simply "allow
globs".

**What was traded away, named.** `*.*log*` blanks tables in every schema, and
`*log*` against `*_log*` is the cata·log trap this repo fell into once already
(hdb_source_catalog_version). The safety that replaces "a rule cannot reach
another schema" is measurement rather than restriction: the Rules tab reports
how many tables each rule matched on the selected database, the manifest records
every table's fate individually, and `pgctl plan` shows what an apply will do
before it does it. A rule that quietly matches nothing and a rule that quietly
matches forty are both visible; they were not, when the interface had no Rules
tab at all.

**Still not glob or regexp.** `*` at the ends of either part, and nothing else.
A pattern that selects tables for truncation has to be legible to whoever
reviews the config, and the forms here cover every rule anybody has written.

## 23. A storage credential may come from a command, which decision 8 refused

Decision 8 deleted pgctl's sops decryption and its `Secrets` type, and the
reasoning holds: PostgreSQL has a complete credential scheme in `~/.pgpass` and
`~/.pg_service.conf`, and a tool that reads credentials itself and passes them
on connects by different rules from the `pg_dump` it shells out to. **This does
not reopen that.** Database credentials still come from libpq's own files and
pgctl never sees them.

A storage account key is not a database credential and has no libpq equivalent.
Decision 21 left it as an environment variable per remote, which is right for CI
and leaves a person with one honest option:

```sh
sops exec-env envs/prd.env 'pgctl snapshot --from prd --to-storage snapshots'
```

That works today, needs no code, and is the shape decision 8 argues for — the
credential store decrypts, pgctl knows nothing about sops. It was the
recommendation until the cost was measured.

**`sops exec-env` decrypts the whole file into the environment**, and pgctl
hands its environment to `pg_dump`, `pg_restore`, `vacuumdb` and every hook a
config declares. On the file this was built against that is **239 variables**,
taking the process environment from 71 to 309, to deliver one key. A `preApply`
hook scaling swarm services then runs with the whole of production's secrets in
its environment. That is a strange thing for a tool with a test asserting
`PGPASSWORD` never appears in `SubprocessEnv`.

**So a remote may name a command.** `accountCommand` and `keyCommand`, run with
a shell, relative to the config's directory, cached per process. It is the
`git credential.helper` pattern, and the narrow version of what decision 8
deleted: no file format, no key paths, no `Secrets` type — pgctl runs what the
config tells it to and reads one line of stdout.

**What the guarantees are, and why each one exists.** The value never reaches an
error message: a failure reports stderr and the exit status, and the command
text is dropped too — the test written for the first rule found the second, by
using `printf THE-SECRET` as its command and watching the error quote it. The
value never reaches a subprocess. A command that exits zero and prints nothing
is an error, because that is exactly what sops does when an extract path matches
no key, and the alternative is an authentication failure minutes later naming
nothing.

**What would have to change to reverse this.** A credential helper turning into
a config format — a `sops:` block, key paths, a file type — would be decision 8
happening again, and the answer then is the same: it is not pgctl's job to know
how a team encrypts a file.

## 24. A panel selection is a default, not an answer

The forms used to ask which connection AND which database, and decision-shaped
reasoning removed both in one day: the panels are a hierarchy, a row of
Connections with a database selected under it is already a complete statement of
what an operation is about, and a form that asks again is a second way to say
one thing that can disagree with the first. The connection field had in fact
disagreed — it offered one server's name beside another server's database names.

**That was right about the connection and wrong about the database**, and the
evidence was a person using it and not being able to answer "what am I
snapshotting?":

> I think I know why I'm so confused about what I'm snapshotting… I am on
> claims, but in the other pane I'm on product-development but the modal reads
> "Take a snapshot of prd/claims". I don't know what the pane that is labeled
> Overview · Databases · Config, that pane's "Databases" is for.

Two causes, and only the second is the interesting one.

**The duplicate list.** The Connections pane had a Databases tab listing the
connection's databases with their sizes — which is exactly what panel 2 is, and
what the Overview tab already counts. Three statements of one fact, two of them
with cursors, and the cursor that meant something was not the one in the pane a
reader was working in. The tab is deleted. Nothing was lost: `databases 6` and
`total size` are on Overview, and the sizes are in panel 2.

**The missing question.** Even with one list, taking the cursor as the answer
cannot express a snapshot of several databases — and `pgctl snapshot --from prd`
with no `--db` has always covered every declared database, so the interface was
strictly less able than the CLI it is a peer of. The nightly's whole shape is
"every database, one timestamp".

So the form asks, with the panel's selection **chosen by default**. That keeps
what the panel-decides design got right — looking at one database is a statement
of intent, and six is rarely what somebody means when they were looking at one —
and gives up nothing: `a` takes all, and the run is one snapshot set with one
timestamp, which is what `<env>/latest` has to be able to mean.

**The connection stays unasked**, and the asymmetry is the decision rather than
an inconsistency. Panel 1 has one cursor, there is nothing to combine, and the
failure it prevents actually happened. Databases have a plural answer; a
connection does not.

**What this cost.** The multi-select comp does not have (tuikit 49), for the
second time in one tool — deleted with the connection field in the morning,
rebuilt for destinations in the afternoon, and now serving both. `m` is left
asking for one database from the panel, deliberately: a move drops and reloads
the target, and six at once is an hour of an environment being unusable. If that
turns out to be wrong in use, it should change the way this did.
