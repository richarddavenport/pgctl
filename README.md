# pgctl

Move PostgreSQL data between environments: nightly snapshots, whole-database
refreshes, and migrations of individual tables that respect the foreign keys
between them.

pgctl is a thin, careful layer over `pg_dump` and `pg_restore`. It exists
because the parts those tools leave to the operator — which tables have to
travel together, what order they load in, which constraints must come out
first, which table is too big to move whole — are the parts that go wrong.

## What it does

- **Snapshots** are immutable, catalogued artifacts. Each one records the
  environment it came from, when, the server version, and every table's fate.
  A restore names a snapshot; nothing is inferred from a filename.
- **A snapshot is also a per-table backup.** Dumps are written in directory
  format, one file per table, so tables can be cherry-picked out of last
  night's full snapshot. There is no separate per-table backup to remember to
  take.
- **A table selection is refused unless it is referentially closed.** pgctl
  reads the target's foreign keys, computes the closure of what you asked for,
  and tells you what is missing rather than letting the load fail halfway.
- **Load order comes from the graph, not from a list in a script.** The
  selection is condensed into strongly connected components and ordered
  parents-first, so a schema containing foreign-key rings still gets a complete,
  correct plan.
- **Tables too large to move whole are filtered.** `pg_dump` has no row
  predicate, so a table with a `where:` rule is produced by binary `COPY`
  alongside the archive and recorded in the same manifest.

## Requirements

PostgreSQL **16 or newer**, server and client tools both — below 16 there is no
`zstd` compression, and pgctl's speed starts there. `sops` on PATH if your
environments keep their credentials in encrypted dotenv files.

## Getting started

```sh
pgctl snapshot --env prd              # every declared database
pgctl ls                              # what is on disk
pgctl plan prd/latest --to qat        # what a refresh would do
pgctl apply prd/latest --to qat       # do it
```

Restore a slice instead of everything:

```sh
pgctl plan  prd/latest --to qat --set claims
pgctl apply prd/latest --to qat --set claims --widen
```

`--widen` accepts the referential closure of the selection. Without it, a
selection that is not closed is refused and the missing tables are named.

## Configuration

`pgctl.yaml`, committed next to the schema it describes:

```yaml
# Environments come from swarmctl's config when there is one, so that "which
# host is prd" has a single answer. Host, port, user and password are read from
# each environment's sops-encrypted secrets file.
environmentsFrom: swarmctl.yaml

storage:
  kind: azureblob
  container: pg-snapshots
  retention: { daily: 7, weekly: 4, monthly: 3 }

defaults:
  jobs: 8
  compression: zstd:3

databases:
  - name: product-development
    excludeSchemas: [hdb_catalog]

sets:
  - name: claims
    database: product-development
    include: ["claims.*"]

rules:
  - table: quotes.quote
    where: "created_at > now() - interval '30 days'"
    why: "20 GB of jsonb payloads; recent quotes are enough to work with"

  - table: audit.logged_actions
    data: none

# Quiescing a target is project-specific, so it is a hook rather than a
# built-in step. Terminating database connections is built in — that is about
# the database, not about what is connected to it.
hooks:
  preApply:
    - name: scale the app down
      run: swarmctl set api_api --replicas 0 --env "$PGCTL_ENV"
  postApply:
    - name: scale the app up
      run: swarmctl set api_api --replicas 2 --env "$PGCTL_ENV"
  onFailure:
    - name: scale the app back up
      run: swarmctl set api_api --replicas 2 --env "$PGCTL_ENV"
```

## Safety

- An environment marked `guarded` needs its name typed in full before an apply.
  `--yes` does not waive it; a non-interactive run must pass `--confirm <env>`.
- A `protected` environment can never be an apply target, and there is no flag
  to turn that off. With no `protect:` key, every guarded environment is
  protected.
- A snapshot that did not finish cannot be applied.
- A snapshot missing a table the target has is refused, not loaded partially.

## Design

`design/decisions.md` records every decision and the reasoning behind it —
directory format, zstd, why filtered tables use binary `COPY`, why constraints
are rebuilt `NOT VALID` and validated separately, and why masking is a seam with
no rules in it yet.
