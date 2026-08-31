# Maintenance

*Recorded 2026-08-31, from a conversation that started "sometimes we have to
vacuum things" and honestly added "I don't know all of them". This file is the
place to work that out, not a plan that has been agreed. Nothing below is built
except where it says so.*

## Why this belongs in pgctl at all

Most PostgreSQL maintenance is autovacuum's job and a tool that invites people
to do it by hand mostly does harm. There are three reasons pgctl is the right
home for the parts that are left.

**A restore is when a database needs maintenance most, and pgctl is what just
restored it.** A freshly loaded database has no planner statistics, nothing
frozen, and indexes built in one pass over data that arrived in a different
order than it will be queried in. Whatever else is true, the moment after a
refresh is the moment to act, and only the thing that did the refresh knows it
has happened.

**pgctl already holds the two things a maintenance task needs**: a resolved
connection to every environment, and a model of which tables matter — sets,
rules, the referential graph. A maintenance command written anywhere else would
have to reinvent both.

**The reporting is the valuable half.** "Which tables are bloated, which
indexes are dead weight, how close is production to a wraparound" is a question
nobody asks until it is urgent. It is a panel, not a command.

## Already done

- **ANALYZE after a whole-database restore.** `pg_restore` does not collect
  statistics and nothing else will until autovacuum gets round to it, so until
  2026-08-31 every full refresh left the planner blind. Measured on a database
  restored by an earlier pgctl: **173 tables with rows and no statistics at
  all**. Now run with `vacuumdb --analyze-only --jobs=N`, which took **7
  seconds** for 212 tables where a serial `ANALYZE` would have worked through
  them one at a time. A failure here warns rather than failing the restore: the
  data is in and correct, and statistics can be collected later.
- **ANALYZE after a set-level apply**, for the tables the apply touched.
- Index rebuilds, foreign-key revalidation and sequence values, all as part of
  a set-level apply rather than as maintenance in their own right.

## Candidates

Grouped by what goes wrong if nobody does them. The verdict column is a
judgement about pgctl, not about the task.

### Freezing and transaction-ID wraparound

The only one on this page that takes a database **down** if ignored — at
2 billion transactions PostgreSQL stops accepting writes. `age(datfrozenxid)`
against `autovacuum_freeze_max_age` is the whole measurement.

On the dev database this reads 0.0% of the limit, but that number is worthless
there: it was restored last week, so its transaction counter is new. **This has
to be read on production to mean anything**, which is a good argument for
pgctl reporting it, since pgctl is the thing with a production connection.

*Verdict: report it, prominently. Never fix it automatically — an aggressive
freeze on a large table is hours of I/O and wants scheduling.*

### Planner statistics beyond the default

`default_statistics_target` is 100. For a column with a skewed distribution —
a status column where 99% of rows are one value, which every one of these
tables has — 100 buckets is not enough and the planner chooses badly. The fix
is per-column (`ALTER TABLE … ALTER COLUMN … SET STATISTICS`) or, for
correlated columns, `CREATE STATISTICS`.

*Verdict: worth reporting where a column looks skewed. The fix is a schema
change and belongs in a migration, not in a tool run by hand.*

### Bloat, and reclaiming it

Dead tuples that autovacuum has marked but not returned to the operating
system. `n_dead_tup` against `n_live_tup` is the cheap signal;
`pgstattuple` is the accurate one and costs a full scan.

Reclaiming means rewriting the table: `VACUUM FULL` and `CLUSTER` both take an
**exclusive lock** for the duration, which on `operations.policy_contract`
means the application is down for it. `pg_repack` does the same job without
the lock and is an extension the servers do not currently have.

*Verdict: report bloat. Offer a rewrite only behind the same guarded/protected
machinery an apply uses, and only after the lock is spelled out on screen. This
is the task most likely to cause an outage if made convenient.*

### Indexes

Four separate things wearing one label:

- **Invalid indexes** — a failed `CREATE INDEX CONCURRENTLY` leaves an index
  that is not used and not maintained. `pg_index.indisvalid = false`, cheap and
  unambiguous. Currently **0** on dev. *Report, and offer to drop: there is no
  case for keeping one.*
- **Bloated indexes** — `REINDEX CONCURRENTLY` since PostgreSQL 12 rebuilds
  without an exclusive lock. *Report; offer per index.*
- **Unused indexes** — `idx_scan = 0`. Dev reports **306 never-scanned indexes
  totalling 1,738 MB**, and that number is close to meaningless: the counters
  reset on restart and this server was restored. Read over a long window on
  production it is real, and the write cost of an unused index is paid on every
  insert. *Report only with the window it was measured over, or it will get
  somebody to drop an index that a month-end report needs.*
- **Duplicate and redundant indexes** — an index whose columns are a prefix of
  another's. Detectable from `pg_index` alone, no statistics needed.

### Autovacuum tuning

`autovacuum_vacuum_scale_factor` is 0.2 — a table waits until a fifth of it is
dead before autovacuum touches it. On `operations.policy_contract` at 1.5M rows
that is 300,000 dead tuples, and on `shared.entity_note` at 3.7M rows it is
740,000. Large tables generally want a per-table override; three tables in this
database already have `reloptions` set, so somebody has been here before.

`maintenance_work_mem` is 64 MB, which is low for the index rebuilds pgctl
itself performs during a set-level apply. That one pgctl can simply set on its
own session, and probably should.

*Verdict: report which tables are big enough to want an override. Setting it is
a schema change — a migration.*

### Routine vacuuming

*Verdict: stay out.* Autovacuum is on and does this. A `pgctl vacuum` that
people run by hand would mostly be a way to add I/O at a bad moment. The
exception already noted: freezing freshly restored data so the first autovacuum
does not have to.

## Shape, when it gets built

Two surfaces, neither of them a `vacuum` command:

**A Maintenance panel** in the TUI — a sixth panel, or a tab on Databases —
listing what is wrong with the selected database in severity order: wraparound
percentage, invalid indexes, tables with no statistics, bloat, tables wanting an
autovacuum override. Each row says how it was measured and how stale the number
is, because half of these signals are meaningless on a freshly restored server
and the panel must say so rather than let somebody act on them.

**`pgctl check`** — the same thing headless, exiting non-zero on anything
urgent, so the nightly can run it against production and complain.

Fixes stay opt-in, per item, and anything holding an exclusive lock goes through
the guarded confirmation an apply uses.

## Open questions

1. **Which of these actually bite you?** Everything above is either measured on
   a restored dev database — where half the signals are noise — or reasoned
   from the schema. The same measurements against production would say which
   are real, and that is one read-only query set away.
2. **Is a report enough to start with?** A panel that tells you the truth about
   production is useful on its own, and it is a fraction of the work of making
   any of these fixable from the tool.
3. ~~**Is `pg_repack` available, or installable?**~~ **Answered 2026-08-31: it is
   not available on this server.** So there is currently no way to reclaim bloat
   without an exclusive lock, which makes reporting bloat more useful than
   offering to fix it, and makes getting `pg_repack` installed a prerequisite for
   the fix rather than a detail of it.

The wider enumeration this came out of is in
[dba-surface.md](dba-surface.md) — fifteen domains, with what was measured on
this database against each.
