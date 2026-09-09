# The landscape

A running list of tools that do some part of what pgctl does, gathered so that
each can be dug into properly later. **This file is a register, not a verdict**
— nothing here is a recommendation, and the one-line descriptions are the
projects' own claims until someone has actually run them.

Status column: `listed` = named by a source, nothing verified. `read` = the
project's own docs have been read. `run` = actually exercised against a
database.

## Sources

- [postgresgui.com — best PostgreSQL backup solution](https://postgresgui.com/blog/best-postgresql-backup-solution)
  — a vendor comparison piece; frames the choice around RPO/RTO rather than
  tool popularity, and ends on "a successful backup job does not prove
  recoverability."
- [r/PostgreSQL — introducing SnapVault](https://www.reddit.com/r/PostgreSQL/comments/1dnosng/introducing_snapvault_a_postgresql_backup_tool/)
  — June 2024, 18 comments. Read; the thread is more useful than the post, and
  what it argues about is summarised under "What the SnapVault thread settled"
  below.
- [PostgreSQL wiki — Ecosystem:Backup](https://wiki.postgresql.org/wiki/Ecosystem:Backup)
  — the closest thing to a canonical list. Fifteen entries, heavy on
  enterprise agents.
- [github.com/mixxorz/DSLR](https://github.com/mixxorz/DSLR)

## Category A — developer snapshot tools

The closest neighbours to what pgctl does for a person moving between
environments. All of them clone with template databases rather than dump and
restore, and all of them say in their own README that they are not for
production.

| Tool | Status | Language | What it is |
|---|---|---|---|
| [DSLR](https://github.com/mixxorz/DSLR) | read | Python | Named snapshots of a local database via `CREATE DATABASE … TEMPLATE`. `snapshot`, `restore`, `list`, `rename`, `delete`, `export`/`import` (the last two shell out to pg_dump/pg_restore). Claims 8× faster snapshots and 3× faster restores than the dump/restore path; ~4s vs 36s on 1 GB. Successor to Stellar. |
| [SnapVault](https://github.com/cotramarko/snapvault) | read | Go | The same idea as a single static binary, no Python runtime. `save`, `restore`, `list`, `delete`. Clones live as sibling databases on the same server. Homebrew and prebuilt binaries for macOS/Linux/Windows, Intel and ARM. |
| Stellar | listed | Python | DSLR's predecessor; named by both of the above. Needs looking at for what it did that they dropped. |

## Category B — physical backup and point-in-time recovery

The serious operational tools. pgctl is not competing here, but their
retention, verification and cataloguing behaviour is the mature version of
problems pgctl also has.

| Tool | Status | License | What it is |
|---|---|---|---|
| [pgBackRest](https://pgbackrest.org) | listed | MIT | Full/differential/incremental backups, parallel operations, WAL archiving. The article's default recommendation for self-managed production. Needs a repository and PostgreSQL configuration. |
| [Barman](https://pgbarman.org/) | listed | GPLv3 | Centralised backup management across many servers. Fleet-level coordination, at the cost of a backup service to operate. |
| [WAL-G](https://github.com/wal-g/wal-g) | listed | Apache 2.0 | Cloud-native archival to object storage; also does MySQL/MariaDB and SQL Server. Restore automation is the operator's job. |
| [pg_probackup](https://github.com/postgrespro/pg_probackup) | listed | PostgreSQL | Periodic backups with full cluster restoration. |
| [pgmoneta](https://pgmoneta.github.io/) | listed | BSD 3-clause | On-disk encryption, pluggable storage engines, monitoring. |
| [pghoard](https://github.com/Aiven-Open/pghoard) | listed | — | Aiven's backup and restore service. Found by search, not on the wiki list. |
| [pg_rman](https://github.com/ossc-db/pg_rman) | listed | — | Backup and restore management. Found by search. |
| [Databasus](https://databasus.com) | listed | Apache 2.0 | Logical *and* physical backups, WAL streaming, and **automated restore verification** — the last of those is worth reading properly. |
| `pg_basebackup` | listed | PostgreSQL | In-tree physical backup over the replication protocol. No scheduling, retention or archive management — the thing everything above wraps. |

## Category C — logical dump wrappers

The category pgctl is actually in.

| Tool | Status | License | What it is |
|---|---|---|---|
| [pg_back](https://github.com/orgrim/pg_back) | listed | — | "Simple backup tool for PostgreSQL." Dump-based. Closest structural comparison to pgctl's snapshot command. |
| [BackupDrill](https://github.com/backupdrill/cli) | listed | MIT | Streams `pg_dump` to S3-compatible buckets with SHA-256 checksums. |
| `pg_dump` | — | PostgreSQL | The baseline. Portable, no PITR, restore time grows with database size. |

## Category D — general-purpose backup suites with a PostgreSQL agent

Named by the wiki for completeness. Almost certainly irrelevant to pgctl, but
listed so nobody re-researches them.

- [Amanda](http://www.amanda.org/) — BSD/GPLv2 community, proprietary enterprise. LAN backup to tape/disk.
- [Bacula](https://www.bacula.org/) — AGPLv3 community, proprietary enterprise. Network backup across diverse systems.
- [Handy Backup](https://www.handybackup.net/) — proprietary.
- [Iperius Backup](https://www.iperiusbackup.com/) — proprietary, Windows.
- [NetVault Backup](https://www.quest.com/products/netvault-backup/) (Quest) — proprietary, deduplicating.
- [Commvault Simpana](https://www.commvault.com/) — proprietary; hardware snapshots, global dedup.
- [IBM Spectrum Protect](https://www.ibm.com/us-en/marketplace/data-protection-and-recovery) — proprietary.
- [Veritas NetBackup PostgreSQL Agent](https://www.enterprisedb.com/blog/veritas-netbackup-and-edb-postgres) — proprietary; filesystem snapshots.

## Category F — filesystem snapshots

Not a PostgreSQL tool at all, and the fastest thing in the register. Raised in
the SnapVault thread by someone using it in preference to any of the above.

| Approach | Status | What it is |
|---|---|---|
| `zfs snapshot` / rollback | listed | Snapshot and roll back the dataset the cluster lives on. Measured by a commenter at **~0.01s for snapshot plus rollback on a 20 GB database**, against DSLR's ~4s + ~4s on 1 GB. |
| btrfs snapshots | listed | Named as the same trick on a different filesystem. |

The catch, established in the same thread: rolling back under a running server
corrupts data, so the server must be stopped first. In Docker that commenter
measured stop-and-start at ~4s, which is the real cost — and it is a fixed cost
that does not grow with the database, which is why it still beat a 1 GB
dump/restore by a wide margin. It also snapshots the whole cluster, not a
database, and cannot move anything between machines. Worth measuring against
pgctl's own dump timings before dismissing.

## Category E — managed services

Provider-automated snapshots and recovery: RDS, Cloud SQL, Azure Database for
PostgreSQL, Neon, Supabase. Retention, portability and restore controls vary by
provider, and none of them address moving data *between* environments, which is
the whole of pgctl's problem.

## Category G — moving data between environments

**Found after the first pass, and this is pgctl's actual category** — not
backup. These tools exist to get production-shaped data into a development or
staging environment, which is the problem pgctl was written for. Everything in
categories A to F was the wrong neighbourhood.

| Tool | Status | Stars | What it is |
|---|---|---|---|
| [pgsync](https://github.com/ankane/pgsync) | read | 3.5k | **The closest neighbour by a distance.** A committed `.pgsync.yml` naming groups of tables, per-table `where` clauses, `exclude`, and `data_rules` that mask sensitive columns so they never leave the source. Read the foreign-key section before anything else. |
| [pgcopydb](https://github.com/dimitri/pgcopydb) | listed | 1.6k | "pg_dump \| pg_restore on steroids" — copy a whole database to a target server, parallelised. |
| [Jailer](https://github.com/Wisser/Jailer) | listed | 3.2k | Database **subsetting** and relational data browsing. Java, Apache 2.0. Explicitly walks foreign keys to extract a coherent slice. The one tool in the register whose stated purpose overlaps pgctl's hardest problem. |
| [pg_sample](https://github.com/mla/pg_sample) | listed | 355 | Creates a small sample database from a larger one, preserving referential integrity. |
| [Replibyte](https://github.com/Qovery/replibyte) | listed | 4.4k | "Seed your development database with real data." Last pushed 2025-08; check whether it is still maintained. |
| [Neosync](https://github.com/nucleuscloud/neosync) | listed | 4.1k | Detect PII, anonymise production data, sync across environments. |
| [Greenmask](https://github.com/GreenmaskIO/greenmask) | listed | 1.8k | Database anonymisation and test-data management, Apache 2.0. |

### pgsync's foreign-key section is the finding

Asked how to handle foreign keys, pgsync offers three options and recommends
none of them confidently:

1. defer constraints;
2. **manually specify the order of tables** (with `--jobs 1`, one at a time);
3. `--disable-integrity`, which its own docs say "can silently break referential
   integrity (not recommended)" — and which needs superuser, unavailable on RDS
   without `rds_superuser` and, it notes, apparently impossible on Heroku.

That is the gap pgctl's graph fills, stated by the closest comparable tool in
its own words: 3,500 stars, and the answer to "what order do these tables load
in" is still *you write the list*. Azure grants no superuser either, which is
why pgctl disables user triggers per table rather than reaching for
`session_replication_role` — the same constraint, met differently.

## Upstream issues worth reading

The issue trackers are more useful than the READMEs: they say what the
maintainers decided *not* to build, and how long people have been asking.

| Issue | Age | Why it matters |
|---|---|---|
| [pgmoneta#911 — Restore Planning and Preflight Validation](https://github.com/pgmoneta/pgmoneta/issues/911) | open, Feb 2026 | **This is pgctl's `plan` command, argued from first principles by someone else.** Wants the restore to expose what will be used, whether the target is compatible, and expected size — because "restore failures often happen after filesystem changes have started" and "no rollback mechanism exists". Worth reading in full before touching `plan.go`. A PR (#991) exists. |
| [pgbackrest#773 — restore "dry-run"](https://github.com/pgbackrest/pgbackrest/issues/773) | open since **2019** | The same request, seven years unanswered in the most serious tool in the register. |
| [wal-g#1453 — Tables partial restore for PostgreSQL](https://github.com/wal-g/wal-g/pull/1453) | open since 2023, zero comments | A contributed implementation of per-table restore, ignored. Motivation given: "skipping big tables (>1GB)" — pgctl's `where:` rules answer the same complaint differently. |
| [pg_probackup#223 — partial restore (tablespace level)](https://github.com/postgrespro/pg_probackup/issues/223) | open since 2020 | Partial restore wanted, at the coarsest possible granularity. |
| [pgbackrest#693 — cannot restore a dropped table when foreign keys are present](https://github.com/pgbackrest/pgbackrest/issues/693) | closed, 2019 | Someone tried to use a physical backup tool to recover one table and hit the foreign keys. Closed, not solved — it is out of scope for that tool. |
| [stellar#16 — Plans for partial snapshots and exporting](https://github.com/fastmonkeys/stellar/issues/16) | open since **2014** | The oldest open request in the register. Twelve years. |
| [stellar#79 — synchronise db snapshots with git checkout](https://github.com/fastmonkeys/stellar/issues/79) | open, 2019 | Recurring wish: the database state should follow the branch. |
| [stellar#80 — snapshot retention](https://github.com/fastmonkeys/stellar/issues/80) | open, 2019 | Retention asked for and never built; pgctl has it. |
| [DSLR#29 — slowness on PostgreSQL 17 with a large DB](https://github.com/mixxorz/DSLR/issues/29) | open, Oct 2025 | The template-database trick degrading at size. The failure mode to check before believing the 8× figure. |
| [DSLR#13 — ssh support](https://github.com/mixxorz/DSLR/issues/13) / [snapvault#15 — copying between databases](https://github.com/cotramarko/snapvault/issues/15) | open | Both dev-snapshot tools are being asked to reach another machine. That is where they stop and pgctl starts. |

### Category G's trackers — the second sweep

The first issue sweep covered only the backup tools, and was keyword search
rather than a read. These were listed properly afterwards. They are the more
useful half, because three of them attempt referential subsetting and their
bugs say what is hard about it.

| Issue | Why it matters |
|---|---|
| [greenmask#396 — Subset: polymorphic scope predicates produce ~0 rows](https://github.com/GreenmaskIO/greenmask/issues/396) | **Greenmask has a subset system with a foreign-key traversal, and it is subtly wrong.** With multiple polymorphic references on one table the generated query "silently drops nearly all rows — only rows where the FK column is `NULL` survive." Open since Feb 2026 alongside [#392 "epic: subset system revision"](https://github.com/GreenmaskIO/greenmask/issues/392). This is the closest thing to a competitor for pgctl's closure computation, and it is being rewritten. |
| [pg_sample#66 — Missing referenced rows](https://github.com/mla/pg_sample/issues/66) | The subsetting failure mode in one line: "not all foreign keys are followed". Reported April 2025, no reply. A sample that *looks* complete and is not — which is precisely what pgctl refuses rather than produces. |
| [pgsync#247 — Truncates the source if from and to use the same database](https://github.com/ankane/pgsync/issues/247) | "So, it truncated the tables from source db for me." **This is the accident pgctl's `protected` flag exists to make impossible**, happening to a real person on the closest comparable tool, July 2025, still open. The strongest single piece of evidence for decision 8's safety flags in the whole register. |
| [pgcopydb#1034 — failed `SET session_replication_role` turns apply into a silent no-op](https://github.com/dimitri/pgcopydb/issues/1034) | Direct vindication of pgctl using `DISABLE TRIGGER USER` instead: the superuser-requiring mechanism fails *silently* when the privilege is absent. |
| [pgsync#237 (show missing tables)](https://github.com/ankane/pgsync/issues/237), [#210 (table allowlist)](https://github.com/ankane/pgsync/issues/210), [#203 (exclude rows by condition in yml)](https://github.com/ankane/pgsync/issues/203) | Three open requests for things pgctl already has: naming what is missing, an allowlist, and per-table row predicates in config. |
| [replibyte#295 — provide a "sequential" subset strategy](https://github.com/Qovery/replibyte/issues/295) | Asked Feb 2024. Replibyte cannot build on current Rust ([#307](https://github.com/Qovery/replibyte/issues/307)) and has no new release ([#308](https://github.com/Qovery/replibyte/issues/308)) — treat as abandoned. |
| [neosync#3565 — "What is the post-acquisition plan?"](https://github.com/nucleuscloud/neosync/issues/3565) | Neosync was acquired; the tracker has been quiet since August 2025. Also [#3433](https://github.com/nucleuscloud/neosync/issues/3433): generating a UUID for a PK breaks the referencing FKs. |
| Jailer | **Issues are enabled and there are zero open.** Either exceptionally well maintained or triaged elsewhere; do not read the silence as either until someone looks at how it actually subsets. |

pgcopydb's tracker is worth a separate note: almost every open issue is a
precise, self-reported correctness bug in logical replication apply
(unreachable commits, silently discarded transactions, `compare data` exiting 0
when the databases differ). That is what moving data between environments looks
like when it is done continuously rather than as a snapshot, and it is a good
argument for pgctl's stance that a snapshot is an immutable artifact.

### Repository vitality, September 2026

Measured, not guessed — so a dead project is not read as a live one.

| Repo | Stars | Open | Last push | Note |
|---|---|---|---|---|
| databasus/databasus | 8,470 | 5 | 2026-09-06 | Very active; restore verification is real work, not a slogan |
| Qovery/replibyte | 4,408 | — | 2025-08-08 | A year quiet |
| pgbackrest | 4,363 | 31 | 2026-09-03 | Active |
| wal-g | 4,242 | **309** | 2026-09-08 | Active but a large backlog |
| nucleuscloud/neosync | 4,141 | — | 2025-08-30 | A year quiet |
| fastmonkeys/stellar | 3,851 | 31 | 2024-12-13 | **Effectively dead**, and still the most-starred dev snapshot tool |
| ankane/pgsync | 3,473 | — | 2026-08-15 | Active |
| Wisser/Jailer | 3,196 | — | 2026-09-08 | Active |
| EnterpriseDB/barman | 3,232 | 40 | 2026-08-27 | Active |
| GreenmaskIO/greenmask | 1,757 | — | 2026-08-25 | Active |
| dimitri/pgcopydb | 1,556 | — | 2026-08-28 | Active |
| Aiven-Open/pghoard | 1,422 | 53 | 2026-08-20 | Active |
| postgrespro/pg_probackup | 800 | 189 | 2026-07-12 | Large backlog |
| orgrim/pg_back | 565 | 14 | 2026-06-02 | Active |
| ossc-db/pg_rman | 504 | 32 | 2026-02-06 | Slow |
| mla/pg_sample | 355 | — | 2025-11-23 | Slow |
| pgmoneta/pgmoneta | 315 | 85 | 2026-08-30 | Active |
| mixxorz/DSLR | 174 | 8 | 2026-03-30 | Alive |
| cotramarko/snapvault | 21 | 2 | 2025-10-03 | Alive, barely |
| backupdrill/cli | 4 | 0 | 2026-09-06 | New; note its pitch is *restore-drilling* a backup to prove it restores |

## What the SnapVault thread settled

Three things came out of the discussion that are worth keeping, because they are
judgements rather than claims:

- **"Backup" was the wrong word, and the author accepted it.** A clone living as
  another database on the same cluster is not a backup in any sense that
  survives losing the server; DSLR's word, *snapshot*, was held to be the honest
  one. The author changed the description (Reddit would not let him change the
  title) and said he would avoid "backup" from then on. pgctl's snapshots are
  separate artifacts that can be pushed off the machine, so the word is defensible
  here — but the distinction is worth stating rather than assuming.
- **A safety default was proposed and accepted: refuse non-localhost by default,
  with a flag to opt into a remote connection.** That is the same shape as
  pgctl's `protected`/`guarded`, arrived at independently by someone reviewing a
  dev tool. Evidence that the refusal-in-the-engine idea is not idiosyncratic.
- **"How does it compare to pgBackRest and Barman?" was asked, and was the wrong
  question.** Two people answered that a template-database wrapper is not in that
  category at all. The register's categories exist so that comparison is not
  attempted across them.

Also asked and not answered: what happens on a 70 GB database, and whether the
approach uses multiple cores. Nobody in the thread had run any of these tools at
that size. That is the gap pgctl's own probe measurements sit in.

## What nobody in this list appears to do

**Revised after finding category G.** The first version of this section was
written having read only backup tools, and two of its four claims were too
strong. What survives contact with pgsync, Jailer and pg_sample:

- **Refuse a selection that is not referentially closed, with the closure
  computed from the *target's* foreign keys.** Greenmask, Jailer and pg_sample
  all walk a graph, but they build a coherent extract *from a source* and none
  answers "is this set safe to load into that server as it stands today".
  pgsync, the closest tool, does not attempt the question. The failure that
  distinguishes them is visible in their trackers: pg_sample#66 and
  greenmask#396 both produce a *silently incomplete* result where pgctl would
  refuse. **Still believed unique; Jailer remains unread.**
- **Derive load order from the graph — strongly connected components, parents
  first — rather than from a list.** pgsync's own documentation says to write the
  list by hand. Evidenced above.
- **Treat one full snapshot as a per-table backup**, cherry-picking tables out of
  last night's dump. The backup tools refuse this (pg_probackup#223, wal-g#1453
  ignored); the dev tools have no artifact to cherry-pick from.

Withdrawn or weakened:

- ~~Per-table rules as committed configuration.~~ **pgsync already does this**, and
  arguably better: `.pgsync.yml` carries groups, per-table `where` clauses,
  excludes, and `data_rules` that mask columns *before they leave the source*.
  pgctl's masking seam is still empty (decision on `redact` aside); pgsync's is
  shipped. If pgctl ever fills that seam, read pgsync's rule matching first.

Still open, and now worth answering deliberately:

- **Restore verification.** Databasus and backupdrill both sell it, pgmoneta#911
  wants it, and pgctl has no answer. Its closest equivalent is the goldens and
  probe tests — which verify the tool, not the artifact.
- **Preflight validation as a named feature.** pgctl has it and calls it `plan`.
  Two of the most serious tools in the register have been asked for it for
  between one and seven years without shipping it. That is worth stating in the
  README in those terms.
