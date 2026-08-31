# The DBA surface

*Recorded 2026-08-31. A spitball capture, not a plan: "everything it takes to
manage the database", deliberately excluding the two things that are somebody
else's job.*

**Out of scope, by instruction.** Authoring schema — `CREATE TABLE`,
`ALTER TABLE`, writing migrations — is developer work and belongs in migrations.
And pgctl is not a query client: no SQL box, no result grid. Everything below is
either a *measurement* pgctl takes and reports, or an *operation* it performs
with its own confirmation machinery.

That distinction is the whole test for what belongs here. "Show me the top
twenty queries by total time" is a report, and fine. "Let me type SQL" is a
client, and is not.

## Measured on the dev database, 2026-08-31

Grounding, so the list below argues from evidence rather than from everything
PostgreSQL can be told to do.

**Two caveats, and the second is the important one.** This server was restored
recently, so anything counter-based — index scan counts, transaction age — says
nothing about production; marked ⚠ below. And every reading here is from the
**local dev container**, a plain `postgres` image. The `shared_preload_libraries`
and logging settings in particular are *managed by Azure* on prd, qat and latest,
where they may well already be set differently. Nothing in the configuration rows
below should be believed about production until this is run there:

```sql
SELECT name, setting FROM pg_settings
 WHERE name IN ('shared_preload_libraries','track_io_timing',
                'log_min_duration_statement','log_lock_waits',
                'default_toast_compression','autovacuum_vacuum_scale_factor');
```

That query is the first thing a configuration-diff report would do, which is
itself an argument for building the report.

| Signal | Reading | Why it matters |
|---|---|---|
| Tables with rows, no statistics | **173 → 0** | Fixed 2026-08-31; `pg_restore` collects none |
| Table comments | **43 of 212** (20%) | A stated standardization goal, currently unmeasured |
| Column comments | **148 of 1,633** (9%) | Same |
| `default_toast_compression` | **pglz** | lz4 exists and is faster and smaller; `quotes.quote` is 19.6 GB of TOAST |
| `shared_preload_libraries` | **empty** | So `pg_stat_statements` is not loaded — all query performance work is blind |
| `log_min_duration_statement` | **-1** | No slow query is logged, ever |
| `log_lock_waits` | **off** | Lock waits invisible |
| `track_io_timing` | **off** | I/O attribution unavailable |
| `autovacuum_vacuum_scale_factor` | **0.2** | A 3.7M-row table waits for 740k dead rows |
| `maintenance_work_mem` | **64 MB** | Low for the index rebuilds pgctl itself performs |
| Per-table autovacuum overrides | **3 tables** | Somebody has been here before |
| Never-scanned indexes | **306, 1,738 MB** ⚠ | Counters reset on restore; real only over a long window |
| `age(datfrozenxid)` | **0.0% of limit** ⚠ | Meaningless here; has to be read on production |
| Replication slots | **0** | An inactive slot retains WAL forever; worth watching where they exist |
| Prepared transactions | **0** | One forgotten prepared transaction blocks vacuum indefinitely |
| Idle in transaction | **0** | Same effect, more common |
| int4 sequences | **2 found**, at 0.0014% | Latent: a busy int4 sequence dies at 2.1 billion |
| Extensions installed | pg_trgm, pgcrypto, plpgsql, plv8 | |
| Available, not installed | pg_stat_statements, amcheck, pgstattuple, pg_buffercache, pg_visibility | |
| **pg_repack** | **not available** | Answers an open question: reclaiming bloat without an outage is currently impossible |

---

## 1. Backup and recovery

pgctl's origin, and still the part with gaps.

- **Logical snapshots** — done.
- **Physical base backups** — `pg_basebackup`, and the WAL archive that makes
  point-in-time recovery possible. A logical dump loses everything since it was
  taken; PITR loses seconds. These are different products and both are needed.
- **WAL archiving health** — `archive_mode`, `archive_command`, and the failure
  nobody notices: archiving stops, WAL accumulates, the disk fills. `pg_stat_archiver`
  has `last_failed_wal` and a failure count.
- **Restore verification** — a backup nobody has restored is not a backup. A
  scheduled job that restores last night's snapshot into a scratch server, runs
  row counts against the source, and reports. pgctl can already do every step of
  this; it just does not do it on a schedule and compare.
- **Recovery drills** — PITR to a chosen timestamp, restore into a new instance,
  timed. Produces the RTO number somebody will eventually be asked for.
- **RPO and RTO tracking** — how old is the newest restorable artifact, and how
  long did the last restore take. Both are already measurable from the manifests.
- **Backup catalogue across environments** — what exists, where, how old, whether
  it verified. `pgctl ls` is this for logical snapshots only.
- **Geo-redundancy** — is the snapshot container replicated to a second region.
- **Integrity** — checksums per artifact, and a verify pass. `pg_restore --list`
  proves an archive is readable without restoring it, which is a cheap nightly
  check.
- **Azure's own automated backups** — the PITR window, whether geo-redundant
  backup is on, and creating a named restore point before a risky deploy. These
  exist independently of pgctl and are currently invisible from it.
- **Single-table extract for a developer** — "give me claims from prd" without
  a whole environment refresh. `--tables` nearly does this; what is missing is
  extracting to a file a person can take away.
- **Masked exports** — the open policy question; the seam exists.

## 2. Replication and high availability

Nothing here is built, and nothing here is optional once there is a standby.

- **Topology** — primary, standbys, and cascades, as a picture.
- **Lag**, in bytes and in seconds, per standby, with a threshold.
- **Replication slot management** — the single most common cause of a
  full-disk outage on a Postgres server: an inactive logical slot retains WAL
  forever. `pg_replication_slots.active` plus retained bytes, and the ability to
  drop a dead one.
- **Logical replication** — publications, subscriptions, worker state, conflicts.
- **Failover and switchover** — promoting a standby, and the checks before it.
- **Synchronous commit configuration** and which standbys are in
  `synchronous_standby_names`.
- **Timeline divergence** after a promotion, which is how split-brain shows up.
- **A delayed standby** — `recovery_min_apply_delay` gives an hour to catch a
  destructive mistake before it replicates. Cheap insurance nobody sets up.
- **Azure Flexible Server HA** — zone-redundant state, read replica health,
  and the fact that a failover changes the primary's identity.

## 3. Vacuum, bloat and freezing

Covered in depth in [maintenance.md](maintenance.md); summarised for
completeness.

- Wraparound monitoring (report, never fix automatically).
- Autovacuum tuning, per-table overrides for large tables.
- Bloat measurement; `pgstattuple` for accuracy, `n_dead_tup` for cheapness.
- **What blocks vacuum**: long transactions, idle-in-transaction sessions,
  prepared transactions, replication slots, and `hot_standby_feedback`. Vacuum
  failing silently for weeks is invisible without this.
- Freezing freshly restored data so the first autovacuum does not have to.
- `VACUUM FULL` / `CLUSTER` — exclusive locks; `pg_repack` is **not installed**,
  so there is currently no lock-free way to reclaim.
- **TOAST bloat specifically** — `quotes.quote` is almost entirely TOAST, which
  the ordinary table-bloat queries do not see.

## 4. Storage, indexes and layout

- **Invalid indexes** — a failed `CREATE INDEX CONCURRENTLY`; unambiguous,
  cheap, and there is no case for keeping one.
- **Bloated indexes** — `REINDEX CONCURRENTLY`.
- **Unused indexes** — with the measurement window stated, or somebody drops
  the index a month-end report needs.
- **Duplicate and redundant indexes** — one index whose columns prefix
  another's. Detectable from the catalogue alone.
- **Index corruption** — `amcheck` is available and not installed. This is the
  check that catches the damage a collation change does, which is the next item.
- **Collation and glibc version changes** — an OS or image upgrade can change
  string sort order, which silently corrupts every text index. Recording the
  collation version per database and comparing it after an upgrade is a small
  check that prevents a very confusing outage. `pg_database.datcollversion`
  exists for exactly this.
- **TOAST compression** — `default_toast_compression` is `pglz`; lz4 is
  available, decompresses several times faster, and on 19.6 GB of jsonb the
  difference is worth measuring. Changing it affects only new rows, so it wants
  a rewrite plan.
- **Partitioning operations** — creating next month's partition, detaching and
  archiving last year's, and noticing when the newest partition is nearly the
  current date. Rolling-window management is pure DBA work and a common
  scheduled job.
- **Growth and forecasting** — size per table and per database over time, and
  the date a threshold is reached. Azure Monitor trends storage at the server
  level; per-table is the one genuine gap, and decision 17 accepts it rather
  than build a store for it.
- **Tablespaces**, **fillfactor**, and **temp file usage** — `log_temp_files`
  shows queries spilling `work_mem` to disk.

## 5. Performance and observability

`pg_stat_statements` keeps a running tally per *query shape* — literals
normalised away, so ten thousand executions of one query collapse to a single
row with total time, calls, mean time, rows and, with `track_io_timing`, I/O
time. Postgres records none of this on its own: `pg_stat_activity` shows only
what is running this instant, so "the database was slow at 3pm yesterday" has no
data behind it at all.

It hooks the executor, so it must be in `shared_preload_libraries`, which is
read only at startup. `CREATE EXTENSION` alone does nothing. That means a
restart, and on Azure it means a server-parameter change.

**It is not required for all of this section, and the split matters.** Available
without it: what is running now, blocking trees, lock waits, deadlocks,
idle-in-transaction sessions, sequential scans per table, vacuum and bloat state
— everything needed for live triage. Requiring it: everything retrospective and
query-level, which is the rest of the list.

The dev container has an empty `shared_preload_libraries`. Whether production
does is unknown and is one query away — see the caveat above the table.

- **Top statements** by total time, mean time, calls, rows, I/O — the standard
  first question of any "the database is slow" conversation.
- **Statement regression** — the same query slower than last week.
  `pg_stat_statements` counters are cumulative, so this needs two readings to
  subtract — which pgctl does not do (decision 17). Azure's **Query Store**
  stores query performance in time windows already; whether it is enabled is the
  question, not whether to build it.
- **Active session inventory** — what is running now, for how long, waiting on what.
- **Blocking trees** — who blocks whom, transitively. `pg_locks` joined to
  `pg_stat_activity`, which is unpleasant to write by hand and perfect for a tool.
- **Deadlock reports** — from the log, with the two statements involved.
- **Idle in transaction** — holds locks and blocks vacuum; the most common
  self-inflicted wound.
- **Cancel and terminate** — `pg_cancel_backend` and `pg_terminate_backend` are
  real DBA actions and are not a query client. Worth having, worth confirming.
- **Connection accounting** — current against `max_connections` (100), per user
  and per database, and how close a pooler is to its own limits.
- **Cache hit ratio, checkpoint frequency, WAL generation rate** — the three
  numbers that say whether the server is sized correctly.
- **Wait event sampling** — where time actually goes.
- **Sequential scans on large tables** — from `pg_stat_user_tables`, no
  extension needed.
- **`auto_explain`** for plans of slow statements, and plan changes over time.
- **Logging that is currently off**: `log_min_duration_statement` is `-1`,
  `log_lock_waits` is `off`, `track_io_timing` is `off`. Three settings, no
  restart needed for the first two, and the difference between diagnosable and
  not.

## 6. Configuration management

The domain where four environments make a tool obviously worth it.

- **Setting inventory and cross-environment diff** — what differs between prd,
  qat and latest, and which of those differences is deliberate. This is a
  half-day of work and would answer questions that currently take an afternoon.
- **Which pending changes need a restart** — `pg_settings.pending_restart`.
- **Configuration drift over time** — recording the set nightly makes "when did
  this change" answerable.
- **Azure server parameters** — Azure disallows `ALTER SYSTEM` for many
  settings, so the real interface is the Azure parameter API. A tool that reads
  both and reconciles them would be genuinely useful.
- **Extension inventory and version drift across environments** — pgctl already
  records extensions in every snapshot manifest, so half of this exists. An
  extension at 1.6 in prd and 1.4 in qat explains bugs that otherwise look
  impossible.
- **`pg_hba` and firewall rules** — who may connect from where. On Azure this is
  firewall rules and private endpoints.
- **Collation, encoding and timezone consistency** across environments.
- **Data checksums** — on or off, and it cannot be changed without a rebuild.
- **Sizing recommendations** from the actual workload rather than from a blog
  post: `shared_buffers`, `work_mem`, `effective_cache_size`,
  `maintenance_work_mem`, autovacuum workers.

## 7. Users, roles and access

- **Role inventory** — who exists, attributes, membership, and the tree of
  inherited grants.
- **Effective privilege report** — "who can read `auth.user`", answered
  transitively rather than one `\dp` at a time. Nobody can do this by hand
  correctly.
- **Cross-environment privilege diff** — a grant that exists in qat and not prd
  is either a missing deploy or a hole.
- **Default privileges** — `ALTER DEFAULT PRIVILEGES` is invisible until it
  surprises you.
- **Row-level security policies** — inventory and diff.
- **Stale roles** — no login since a date, or no privileges at all.
- **Password rotation** — expiry dates, and coordinating the change with the
  applications that hold it. This is where `.pgpass` and the deploy secrets meet.
- **TLS enforcement and certificate expiry** — a `sslmode` audit per environment.
- **Audit logging** — `pgaudit` is not available on this server; log-based
  auditing is the fallback.
- **Failed connection monitoring** — repeated failures are either a broken
  deploy or somebody trying.
- **Superuser and `azure_pg_admin` usage** — who used it and when.
- **PII inventory** — which columns hold personal data, which is the input to
  the masking rules that are currently a seam with no rules in it.
- **Access review export** — the artifact a compliance question asks for.

## 8. Schema operations, as distinct from schema authoring

Not writing DDL; running it safely, and knowing what is there.

- **Pre-flight lock analysis** — will this migration take an `ACCESS EXCLUSIVE`
  lock, and for how long? The difference between a deploy and an outage.
- **Safe-DDL patterns**, enforced: `lock_timeout` with retries, adding a column
  with a default, validating a constraint in two steps. pgctl already does the
  last one — `NOT VALID` then `VALIDATE` — during a set-level apply.
- **Long-running DDL visibility** — `pg_stat_progress_create_index`.
- **Schema drift between environments** — pgctl has a table-level Drift tab
  already; columns, indexes, constraints and functions are the rest of it.
- **Constraints left `NOT VALID`** — including by pgctl itself if a validation
  failed. An unvalidated constraint is a lie the planner believes.
- **Sequence exhaustion** — two int4 sequences exist here. At current rates they
  are centuries away, but the check costs nothing and the failure mode is a
  hard stop on inserts.
- **Orphaned objects** — sequences nothing owns, indexes on dropped columns,
  grants to dropped roles.
- **Catalogue bloat** — from heavy temp table churn.
- **Migration state across environments** — which migrations have run where.
  Hasura owns this today; reading it is still useful.

## 9. Data integrity and lifecycle

- **Referential integrity verification** — orphan rows where a foreign key is
  `NOT VALID`, absent, or was dropped by an earlier apply that failed halfway.
  pgctl has the referential graph already, so this is a query per edge.
- **Restore verification by comparison** — row counts, and checksums per table,
  between a source and a restored copy. Turns "the restore exited zero" into
  "the restore is correct".
- **Corruption detection** — `pg_checksums`, and `amcheck` for indexes.
- **Retention and purging** — deleting data past its policy. `audit.logged_actions`
  is the obvious candidate and is currently handled by not copying it, which is
  not the same as managing it.
- **Cold archiving** — moving old partitions or rows out to cheaper storage.
- **Comment coverage** — 20% of tables and 9% of columns carry a comment against
  a stated goal that every new table and column should. A report per schema, and
  a diff of what a migration added, would make the goal measurable instead of
  aspirational.
- **Data dictionary generation** — the readable artifact those comments exist for.

## 10. Capacity and cost

- **Growth trend and forecast** per database and per table, to a threshold date.
- **Azure sizing** — vCore and storage tier against actual usage, IOPS ceiling
  against actual I/O, and whether storage autogrow is on.
- **Cost per environment** — and the obvious question of whether qat and latest
  need to be running at 3am.
- **Connection pooling** — pgbouncer's own pool saturation, which is invisible
  from the server side.
- **Idle environment suspension** — Azure can stop a Flexible Server; a
  scheduled stop and start of non-production is real money.

## 11. Environment lifecycle

pgctl's core, with the gaps named.

- Provisioning a new environment or database from nothing.
- Refreshing — done.
- **Cloning for a PR environment** — a parked project of yours, and the natural
  consumer of a set-level restore from a nightly.
- Tearing down, including the storage.
- Seeding reference data into an empty environment.
- **Ephemeral branch databases** — a database per branch, from a template,
  measured in seconds rather than the 100 seconds a full restore takes.

## 12. Upgrades and patching

- **Minor version patching** — which environments are behind, and the
  maintenance window.
- **Major version upgrades** — `pg_upgrade` in place, or a logical replication
  cutover with a short switchover. The second is how you do it without a long
  outage, and it is a project each time.
- **Pre-upgrade compatibility checks** — removed features, deprecated settings,
  extensions without a version for the target.
- **Extension upgrades** — `ALTER EXTENSION … UPDATE`, and the drift report that
  says which environment is behind.
- **The collation trap, again** — a major upgrade or base image change can
  change sort order and silently corrupt text indexes. Reindex, and verify with
  `amcheck`.

## 13. Scheduling and automation

- **A nightly** — designed, not built ([nightly.md](nightly.md)).
- **Scheduled verification** — restore last night's snapshot and compare.
- **Scheduled reporting** — the maintenance and drift reports, on a cadence,
  somewhere people see them. A scheduler running the CLI, not a process that
  stays up.
- **Maintenance windows** — a declared window per environment, which everything
  destructive checks.
- **`pg_cron` inventory** where it is used; and this database has its own job
  system in `system.job*`, whose failures are worth surfacing.
- **Alert routing** — the reports are only useful if a threshold reaches a person.

## 14. Incident response

The runbook side. Each of these is a sequence of measurements pgctl could
already take, assembled for a moment when nobody wants to remember them.

- **"The database is slow"** — active sessions, blocking tree, top statements,
  checkpoint and I/O state, in one screen.
- **Disk filling** — largest relations, bloat, WAL volume, inactive replication
  slots, and archiving failures, in that order, because that is the order of
  likelihood.
- **Connection exhaustion** — who holds the connections, and which are idle in
  transaction.
- **Wraparound emergency** — what to freeze first.
- **A runaway query** — find it, cancel it, and record what it was.
- **Post-incident reconstruction** — from the logs and the statistics snapshots,
  which is only possible if something was recording them beforehand.

## 15. Documentation and stewardship

- **Comment coverage**, above — the measurable half of a standardization goal.
- **A relationship map** — pgctl already computes the foreign-key graph and its
  strongly connected components. Rendering it is nearly free and there is
  currently no ERD anywhere.
- **Ownership per schema** — who to ask about `classes`.
- **A generated data dictionary** per environment, versioned, so a schema change
  shows up as a diff.

---

## Where this would start

Ordered by value against effort, from the evidence above rather than from taste.

1. **Find out what production is actually configured with**, which is one
   read-only query and settles several rows of the table above. Then, if
   `pg_stat_statements` is not loaded there, plan the restart that loads it:
   nothing retrospective in section 5 is possible without it, while live triage
   needs none of it. `log_min_duration_statement` and `log_lock_waits` need no
   restart and help immediately.
2. **A read-only health report** — `pgctl check`, and a panel. Wraparound,
   vacuum blockers, invalid indexes, tables without statistics, bloat, comment
   coverage, sequence headroom. Every query is one pgctl can already run against
   every environment, and the report is useful the day it exists.
3. **Cross-environment configuration and extension diff.** Half of it already
   exists in the snapshot manifests. Four environments make it pay.
4. **Restore verification on a schedule.** pgctl can do every step; it does not
   yet do them unattended and compare the result.
5. **Replication slot and WAL monitoring** — before there is a standby, not
   after the disk fills.

## Open questions

1. **Which of these is a real problem for you, as opposed to a real problem in
   general?** The same measurements against production would sort them, and it
   is one read-only query set away.
2. ~~**Report first, or act first?**~~ **Settled: report first**, and the
   reporting surface is read-only by construction — see decisions 16 and 17. It
   is a fraction of the work and most of the value, and it can run as a role
   with no write privileges.
3. **Where do Azure's own controls fit?** Backups, HA, firewall rules and server
   parameters all have an Azure API that is authoritative over anything pgctl
   would do with SQL. Reading both and reconciling them is a coherent product;
   ignoring one of them is not.
4. ~~**Is some of it a second tool?**~~ **Settled: one tool** with a read-only
   boundary inside it rather than two binaries — decision 16. The connection
   model is the expensive shared part, and the overlap points (statistics after
   a restore, lock analysis before a migration, restore verification) fall in the
   gap if they are split.

5. **Is Azure Query Store enabled, and are the log settings on?** This replaced
   the question of whether pgctl should record history. If the retention is
   already switched on, several items above need nothing built.
