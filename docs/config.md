# The config, explained

`pgctl.yaml` describes an estate: which servers exist, which of them may be
written to, how much of each table is worth carrying, and which tables have to
move together. This is the prose version, in the order you meet the ideas.
[`pgctl.example.yaml`](../pgctl.example.yaml) is the same surface as a
commented file, and [`ui.md`](./ui.md) is how the interface renders it.

**Every key is optional.** With no config at all, pgctl connects wherever `psql`
with no arguments would, discovers the databases on that server, and can
snapshot them. Each section below buys you something specific; none of it is
scaffolding you have to write before the tool works.

The file is discovered by searching upward from the working directory, so it
lives next to the schema it describes and is committed with it. `--config`
overrides that.

---

## Connections: where the servers are, and which are dangerous

```yaml
connections:
  prd:
    dsn: "service=prd"
    protected: true       # never an apply target
    jobs: 8
  qat:
    dsn: "service=qat"
    guarded: true         # an apply needs the name typed in full
  local: "postgres://localhost/postgres"
```

A connection is **a libpq DSN and two safety flags**, and that is deliberately
all it is. PostgreSQL already has a complete way to describe how to reach a
server; every time pgctl has invented a parallel scheme for one, it has been
deleted again. A DSN may be a service name (`service=prd`, resolved from
`~/.pg_service.conf`), a URI, or keyword pairs.

**There are no passwords here, and pgctl has no credential store.** libpq finds
the password in `~/.pgpass` or `PGPASSWORD`, exactly as it does for `psql` — and
pgctl hands the *same* DSN to `pg_dump` and `pg_restore`, so both halves of an
operation connect by identical rules. A password cannot be in the config, in
argv, or in a subprocess's environment; a test asserts the last one.

The two flags are the whole safety model, and both are enforced in the engine,
so `pgctl apply` and the interface refuse identically:

| flag | what it means |
|---|---|
| `protected` | **Never** an apply target. There is no flag, prompt or `--yes` that overrides it. Put it on production. |
| `guarded` | An apply must name the connection: `--confirm qat` on the CLI, the name typed in full in the interface. `--yes` does not waive it. |

`jobs` is the parallelism for that server — `pg_dump`/`pg_restore` `--jobs`, and
the parallel transfer to storage. It belongs per connection because a production
server has more vCPUs to spare than a laptop.

## Databases: discovered, not declared

```yaml
databases:
  exclude: [postgres, template*]
```

pgctl asks the server what databases it has. A list in a config file can only
ever disagree with the server, and when it does, the file is wrong. `exclude`
trims the ones nobody wants to think about; a pattern may end in `*`.

`excludeSchemas` drops whole schemas from a snapshot, and **it is the one
setting here that can produce an archive you cannot restore.** A trigger on a
table you kept may call a function in the schema you dropped, so every `CREATE
TRIGGER` fails at restore — after the data has loaded. On the schema this tool
was built for, both obvious candidates are exactly that: `audit` owns
`audit.if_modified_func`, and Hasura's `hdb_catalog` owns thirteen
`notify_hasura_*` functions. `pgctl snapshot` checks for this and warns, naming
the first trigger it found. **The bulk of a noisy schema is removed with a rule
instead**, which is the next section.

---

## Rules: how much of a table to carry

A rule answers one question about one table, or about a pattern of tables:
**what should a snapshot contain for this?** There are three answers.

```yaml
rules:
  # Everything, which is the default and never needs writing.

  # The rows worth having, and nothing else.
  - table: quotes.quote
    where: "created_at > now() - interval '30 days'"
    why: "20 GB of jsonb payloads; recent quotes are enough to work with"

  # The table, with no rows in it at all.
  - table: audit.logged_actions
    data: none
    why: "an append-only audit log; a copied environment has no use for it"
```

| `data` | in the snapshot | on apply |
|---|---|---|
| `all` (default) | every row | every row |
| `filtered` | the rows matching `where` | those rows; the rest of the table is **empty afterwards** |
| `none` | the table's definition, no rows | the table is **emptied** |

Setting `where` implies `filtered`, so you rarely write `data:` at all. Setting
`data: none` wins over a `where` on the same rule — no rows means no rows.

### Why this exists

Two reasons, and they are different.

**Size.** `quotes.quote` on the schema this was built for is 20 GB across
146,597 rows — roughly 143 KB of jsonb per row, two thirds of the whole
database, against about 11 GB for everything else combined. Thirty days of
quotes is enough to develop against, and carrying the other 20 GB costs the
dump, the upload, the download and the restore, every night, forever.

**Noise.** An audit log and an event queue are append-only histories of a
*different* environment. Copying them into QAT is not useful, and in the audit
log's case it is actively confusing.

### How a filtered table actually gets carried

Worth knowing, because it explains what a snapshot directory contains.
`pg_dump` has no row predicate — there is no way to ask it for part of a table
— so pgctl does it in two pieces:

1. The table is dumped **schema-only** (`--exclude-table-data`), so its
   definition, indexes and constraints are in the archive like everything else.
2. Its rows are produced separately as a **binary `COPY` sidecar** —
   `filtered/<table>.bin.zst`, beside the archive, compressed comparably —
   along with the column list they were written with.

On apply, the archive is restored first and the sidecars are loaded after it.
So a snapshot's `filtered/` directory **is part of the snapshot**: copy an
archive without it and the apply fails when it reaches the missing file, having
already truncated the table. Nothing checks for that at plan time yet, which is
the one gap in this area worth knowing about.

### Patterns, and which rule wins

```yaml
rules:
  - table: hdb_catalog.*_log*     # every table whose name contains "_log"
    data: none
```

A pattern is `schema.table`, and **either part** may carry a `*` at its start,
its end, or both. `*` on its own is "any". Never in the middle: `oper*ions.foo`
is refused, because it is either a typo or a regexp somebody expected to work.

```yaml
rules:
  - table: operations.policy_contract   # exactly that table
  - table: operations.policy_contract*  # anything in operations starting with it
  - table: hdb_catalog.*_log*           # anything in hdb_catalog containing _log
  - table: audit.*                      # every table in audit
  - table: "*.*as400*"                  # anything containing as400, in ANY schema
  - table: "*as400*"                    # the same thing, said shorter
```

**A bare wildcard pattern spans every schema.** That is what `*as400*` means,
and it is the form worth knowing about, because the tables a rule needs to catch
are often not in one schema: on the schema this was built for, the tables that
sync to an AS/400 are **nine tables across five** — `claims`, `operations`,
`ory`, `public` and `shared`. Five rules would cover them today and would
silently stop covering them the day a sixth schema gained one.

**A bare EXACT name is still refused.** `policy` is a table somebody forgot to
qualify, and the same name in another schema is a different table whose
truncation is unrecoverable, so the error says how to mean it on purpose:
`*.policy`.

The wildcard schema cuts both ways and it is worth saying so: `*.*log*` blanks
tables in every schema, and `*log*` versus `*_log*` is a one-character
difference this repo has already got wrong once (see below). What replaces the
old "a rule cannot reach another schema" safety is the count — the interface's
**Rules** tab says how many tables each rule matched, and the manifest records
the fate of every table individually.

**The last matching rule wins.** Rules are read in order and each match replaces
the one before it, so the general pattern goes first and the exception after it:

```yaml
rules:
  - table: audit.*                       # the whole schema carries nothing
    data: none
  - table: audit.retention_policy        # …except this one, which is config
    why: "small, and the app reads it at boot"
```

Rules are **not scoped to a database** — they apply to every database a snapshot
covers. That is why a rule matching nothing is a *warning* rather than an error:
`rule "hdb_catalog.*_log*" matches no table in this database` is expected when
you snapshot a database that has no `hdb_catalog`, and is a red flag when you
snapshot the one that does. The interface's **Rules** tab colours a rule that
matched nothing, which is the fastest way to catch a rule left behind by a
rename.

Take that warning seriously. `hdb_catalog.*log*` — no underscore — matches
`hdb_source_catalog_version`, because of the "cata·**log**" in the middle.
Blanking that table leaves Hasura unable to find its own metadata. `*_log*` is
the correct pattern, and the difference is one character.

### `why` is not a comment

```yaml
  - table: quotes.quote
    where: "created_at > now() - interval '30 days'"
    why: "20 GB of jsonb payloads; recent quotes are enough to work with"
```

`why` is recorded **in the snapshot's manifest**, per table, alongside the row
counts and the predicate. Six months later, someone finds `quotes.quote` in QAT
holding a fraction of its rows and has to decide whether that is a bug. The
manifest answers it without a commit log, and the interface shows it on the
snapshot's Tables tab. A rule without a `why` is a rule whose reason lives only
in the head of whoever wrote it.

### Two things rules are not

**Not masking.** `where` chooses *which rows* to carry; it does not change the
values in them. There is no `mask:` yet, and the seam for one exists with no
rules behind it deliberately — the policy question (which columns, and replaced
with what, for a production-to-QAT copy) has to be answered before the mechanism
is worth building.

**Not a selection.** A rule is about *how much of a table*; a **set** is about
*which tables*. They compose without knowing about each other: a set-level apply
of a set containing `quotes.quote` loads that table's sidecar, filtered exactly
as the nightly filtered it.

One caution: `where` is interpolated into `COPY (SELECT … WHERE …)` and pgctl
makes no attempt to sandbox it. It is configuration written by your team and
reviewed like code, not input from anywhere else.

---

## Sets: which tables move together

```yaml
sets:
  - name: claims
    database: product-development
    description: claims and everything a claim points at
    include: ["claims.*"]
    exclude: ["claims.*_audit"]
```

A set is a named group of tables, scoped to one database, for refreshing part of
an environment rather than all of it:

```sh
pgctl apply prd/product-development/latest --to qat --set claims
```

The patterns work as a rule's do. What makes a set more than a list is that
**pgctl checks it against the foreign keys of the target** — the constraints a
load actually has to satisfy — and **refuses** a selection that is not
referentially closed, naming every table the selection reaches into. `--widen`
accepts the closure. On the schema this was built for, a `claims` set naming 25
tables widens to 38.

The refusal is the point. Loading two tables whose rows reference a third that
was not loaded is a failure halfway through a restore, with the target already
truncated. Being told the thirteen table names up front is a decision you can
make.

---

## Storage: where snapshots live, and when they die

```yaml
storage:
  dir: .pgctl/snapshots        # this machine, always
  retention:                   # …and how long it keeps them
    daily: 2

  remotes:
    - name: snapshots
      kind: azureblob
      container: pg-snapshots
      accountEnv: AZURE_STORAGE_ACCOUNT
      keyEnv: AZURE_STORAGE_KEY
      retention: { daily: 7, weekly: 4, monthly: 3 }

    - name: archive
      container: pg-archive
      accountEnv: AZURE_ARCHIVE_ACCOUNT
      keyEnv: AZURE_ARCHIVE_KEY
      retention: { monthly: 12 }
```

**The local directory is not a remote and is not optional.** `pg_dump` writes a
directory, so every snapshot begins on this machine whatever its destination.
What *is* optional is whether the local copy stays: a snapshot placed only in a
remote is uploaded and then deleted from disk, which is how a laptop stops
accumulating 1.9 GB nightlies.

**Each destination has a name**, and the name is its whole identity — nothing
keys off the container or the account, so a container can be renamed without
pgctl thinking its snapshots moved. `local` is reserved, and a remote may not
take it.

**`kind`** is `azureblob`, and defaults to it, since it is the only one.

**`endpoint`** (per remote) overrides the blob service URL. Leave it unset for
Azure — pgctl builds `https://<account>.blob.core.windows.net/`. It exists for a
storage emulator and for a private endpoint.

**Credentials come from where they already are**, per remote, because two
accounts have two keys. You do not have to create the container — pgctl creates
it before the first upload and treats "already exists" as success.

### Two routes to a credential

**An environment variable** — the default, and what CI uses:

```yaml
      accountEnv: AZURE_STORAGE_ACCOUNT
      keyEnv: AZURE_STORAGE_KEY
```

Those two are the defaults and are the variables `az` itself sets, so a remote
using the account you are logged into names neither.

**A command that prints the value** — for a team whose secrets live in an
encrypted file:

```yaml
      accountCommand: sops -d --extract '["AZURE_STORAGE_ACCOUNT"]' envs/prd.env
      keyCommand:     sops -d --extract '["AZURE_STORAGE_KEY"]' envs/prd.env
```

A command **wins** over a set variable. Naming a command is a statement about
where the credential lives; a variable that happens to be set is often an
accident of the shell, and silently preferring the accident is how a snapshot
ends up in the wrong account.

The command runs **relative to the config's own directory**, so `envs/prd.env`
means the same thing wherever you run pgctl from — and **once per process**,
however many remotes name the same file, because each run of sops is a
decryption and possibly a prompt for a hardware token.

### Why a command, when wrapping the whole thing works

This needs no pgctl feature at all and is a perfectly good answer:

```sh
sops exec-env envs/prd.env 'pgctl snapshot --from prd --to-storage snapshots'
```

The reason `keyCommand` exists beside it is what `exec-env` does: it decrypts
the **whole file** into the environment, and pgctl hands its environment to
`pg_dump`, `pg_restore`, `vacuumdb` and **every hook you have configured**.
Measured against the file this was built for, that is **239 variables** — the
process environment goes from 71 to 309 — reaching every subprocess in order to
deliver one key. A command fetches the one value, at the moment it is needed,
and it never reaches a subprocess.

Three things pgctl guarantees about a credential command, because they are the
kind of promise that rots quietly:

- **its output never reaches an error message.** A failure reports the command's
  *stderr* and exit status. Stdout is the secret and is dropped — and so is the
  command text, since `keyCommand: printf hunter2` is a config somebody writes
  while trying this out.
- **its output never reaches a subprocess.** It goes to the blob client and
  nowhere else: no exported variable, no argv.
- **a command that succeeds and prints nothing is an error.** sops exits zero
  when its extract path matches no key, and without this the snapshot fails
  minutes later against the storage account with an authentication error that
  names nothing.

### There is no default destination

Once a remote is declared, **where a snapshot goes is asked every time**:

```sh
pgctl snapshot --from prd --to-storage snapshots
pgctl snapshot --from prd --to-storage local,snapshots
pgctl snapshot --from prd --to-storage all
pgctl snapshot --from prd                        # refused: names the destinations
```

and the interface's snapshot form offers one toggle per destination with nothing
pre-ticked. A snapshot is gigabytes, and whether it lands in a shared account a
colleague will restore from is not something to infer from silence.

With **no** remotes declared there is exactly one place a snapshot can go, so
nothing is asked: `--to-storage` is optional until the day a remote appears, and
required from then on. That is also why adding a remote is a change worth
noticing — it changes what an unqualified `pgctl snapshot` does, from "take it"
to "tell me where".

### Retention is per destination

```yaml
storage:
  retention: { daily: 2 }                          # this machine
  remotes:
    - name: snapshots
      retention: { daily: 7, weekly: 4, monthly: 3 }
    - name: archive
      retention: { monthly: 12 }
```

A laptop keeping two days while an archive container keeps a year is the
ordinary arrangement, and one policy cannot say both. `pgctl prune` walks each
destination with that destination's policy and deletes **only from there**.

An **unset** policy deletes nothing, and prune says which destinations it is
therefore leaving alone — silence would be indistinguishable from a policy that
found nothing to do.

### How the periods actually work

Grandfather-father-son, **per calendar period rather than per count**. Three
independent passes run over the snapshots, newest first:

- `daily: 7` — the newest snapshot of each of the **7 most recent days** that
  have one
- `weekly: 4` — the newest of each of the **4 most recent ISO weeks**
- `monthly: 3` — the newest of each of the **3 most recent months**

**A snapshot kept by any pass is kept**, so the three overlap heavily and the
total is not the sum. A hundred nightlies under `daily: 7, weekly: 4,
monthly: 3` keeps **eleven**, not fourteen:

```
KEEP 2026-09-09 Wed   day, week 2026-W37, month 2026-09
KEEP 2026-09-08 Tue   day
KEEP 2026-09-07 Mon   day
KEEP 2026-09-06 Sun   day, week 2026-W36
KEEP 2026-09-05 Sat   day
KEEP 2026-09-04 Fri   day
KEEP 2026-09-03 Thu   day  ← the seventh and last day
KEEP 2026-08-31 Mon        week 2026-W36 was already held above, so this is 2026-08's month keeper
KEEP 2026-08-30 Sun        week 2026-W35
KEEP 2026-08-23 Sun        week 2026-W34  ← the fourth and last week
KEEP 2026-07-31 Fri        month 2026-07  ← the third and last month
```

Read it as "the last week in detail, then one a week for a month, then one a
month" — which is what a grandfather-father-son policy is for, and it costs
eleven copies rather than a hundred.

Three details that decide what a policy actually means:

- **Periods are UTC**, matching the snapshot ids, so a policy does not shift
  under daylight saving or change meaning when someone runs prune from another
  timezone.
- **A period counts only if it has a snapshot.** `daily: 7` is "the 7 most
  recent days that have one", not "the last 7 calendar days" — so a fortnight
  with the nightly switched off does not consume the budget, and you come back
  to seven days of history rather than none.
- **Retention applies per connection *and* database.** One environment's history
  of one database says nothing about how much of another's to keep, so
  `daily: 7` against six databases keeps seven of each.

### Two rules that make it safe to run unattended

**An incomplete snapshot is never a period keeper.** It cannot be restored, so
retaining it in place of a complete one would silently reduce the real depth of
history — `daily: 1` whose newest snapshot is a failed dump keeps the good one
behind it.

**The single newest snapshot is always kept**, whatever the policy says and even
if it is that failed dump: a prune that can leave an environment with no
snapshot at all is a prune nobody can run unattended. Note the interaction —
the broken newest is kept *as the newest*, not as the day's backup, so the day's
keeper is still the complete one behind it. Both are held.

`pgctl prune` reports by default and deletes only with `--apply`, so the way to
read a new policy is to run it and look.

### Where a restore reads from

A snapshot can be in several places. An apply prefers **local** (it costs
nothing), then tries each remote **in the order the config declares them** — so
the file's order is read as an order of preference, which is the only statement
of one anybody has made. Put the working container before the cheap archive.

A snapshot stays a **tree** in every remote — one file per table, never a
tarball — which is what lets a set-level apply download only the files it needs:
40 of 210 files, 504 MB of a 1.9 GB snapshot, in the run this was measured on.

### Reading where something is

`pgctl ls`'s `WHERE` column and the Snapshots panel both say it. The panel is
twenty-eight columns wide, so it abbreviates — `l` on disk, `r` in a remote,
`l+r` both — and the snapshot's **Manifest** tab names the destinations in full,
where there is room for them.

## Hooks: making room for the restore

```yaml
hooks:
  preApply:
    - name: scale down the API
      run: swarmctl scale api=0 --env qat
      timeout: 5m
  postApply:
    - name: scale the API back up
      run: swarmctl scale api=3 --env qat
  onFailure:
    - name: scale the API back up
      run: swarmctl scale api=3 --env qat
```

Quiescing a target is project-specific — scaling swarm services is one answer
and does not belong inside a data-movement tool — so it is a shell command with
a timeout. `$PGCTL_CONNECTION`, `$PGCTL_DATABASE` and `$PGCTL_SNAPSHOT` are in
the environment.

The important part is the failure behaviour: **`onFailure` and `postApply` run
even when the operation was cancelled.** A `preApply` that scaled an
environment down has to be undone whether the restore succeeded, failed, or you
pressed `q` — which is why `q` in the interface cancels the run rather than
abandoning it, and why `ctrl+c` cancels before exiting.

A `preApply` failure aborts the apply, on the grounds that it was asked for
first. A `postApply` failure is reported and does not fail the apply, because
the data has already landed and pretending otherwise would be a lie.

---

## Defaults

```yaml
defaults:
  jobs: 4
  compression: zstd:3
  lockTimeout: 30s
```

`zstd:3` rather than `pg_dump`'s gzip-6: on a real 31 GB database, 202s and
2.04 GB against 333s and 2.27 GB — 39% faster and 10% smaller at once. It needs
PostgreSQL 16 or newer, which pgctl requires anyway (server *and* client tools).

`lockTimeout` is short on purpose. What it catches is an application still
holding a table pgctl is about to truncate, and the useful behaviour there is to
fail in thirty seconds with a clear message rather than to wait — it is not a
budget for slow work.

---

## Putting it together

A config that says all of the above, in the order pgctl reads it:

```yaml
connections:
  prd:  { dsn: "service=prd",  protected: true, jobs: 8 }
  qat:  { dsn: "service=qat",  guarded: true }
  local: "postgres://localhost/postgres"

databases:
  exclude: [postgres]

defaults: { jobs: 4, compression: zstd:3, lockTimeout: 30s }

storage:
  kind: azureblob
  container: pg-snapshots
  accountEnv: AZURE_STORAGE_ACCOUNT
  keyEnv: AZURE_STORAGE_KEY
  retention: { daily: 7, weekly: 4, monthly: 3 }

sets:
  - name: claims
    database: product-development
    description: claims and everything a claim points at
    include: ["claims.*"]

rules:
  - table: quotes.quote
    where: "created_at > now() - interval '30 days'"
    why: "20 GB of jsonb payloads; recent quotes are enough to work with"
  - table: audit.logged_actions
    data: none
    why: "an append-only audit log of a different environment"
  - table: hdb_catalog.*_log*
    data: none
    why: "Hasura's event queue; *_log* and not *log*, which also matches hdb_source_catalog_version"
```

Read as a sentence: *snapshot every database on prd except `postgres`, carrying
thirty days of quotes and no audit or event history, four files at a time,
zstd-compressed; keep 7 daily, 4 weekly and 3 monthly; never apply to prd, and
make anyone applying to qat type its name. `claims` is a group of tables that
can be refreshed on its own.*

## Where to look when something is wrong

| symptom | where the answer is |
|---|---|
| a table arrived empty | the snapshot's **Tables** tab: `none`, or `filtered` with its row count and `why` |
| a rule seems to do nothing | the **Rules** tab — a rule that matched nothing is coloured |
| an apply was refused | the refusal names the reason; a selection that is not closed lists every table it reaches into |
| `CREATE TRIGGER` failed at restore | an `excludeSchemas` entry owning a function a retained trigger calls. `pgctl snapshot` warns about this at dump time |
| the tool cannot reach a server | the connection's **Overview** tab has the libpq error in full; `r` retries |

`pgctl plan <snapshot> --to <conn>` answers "what would this do" against the
real target without touching it, and is the cheapest way to find out that a
config means something other than what you meant.
