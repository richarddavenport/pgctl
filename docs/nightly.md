# The nightly

A nightly snapshot of production, kept in blob storage, is what makes every
other operation cheap: refreshing QAT is then a restore of something that
already exists rather than an hour of reading production.

## What it does

```sh
pgctl snapshot --from prd    # every database on the server, one timestamp for the set
pgctl prune --apply          # enforce storage.retention
```

`snapshot` uploads as part of taking it. A nightly whose artifact is still on
the runner when the runner is recycled has not backed anything up.

## Where to run it

**In the same region as the database.** This is the largest single win
available and it is free: a dump taken on a laptop crosses the WAN twice, once
to read production and once to write it back to storage. A runner in Azure
crosses it zero times.

For MBPNetwork that means the Forgejo runner on `vm-forge-0`, which is already
an Azure VM. Failing that, any Azure-hosted runner.

## What it needs

- **PostgreSQL 16+ client tools** on the runner: `pg_dump` and `pg_restore`, at
  least as new as the server. Use 17 to match the cluster.
- **A `.pgpass` and, if the config names services, a `~/.pg_service.conf`**,
  written from CI's secret store. Nothing pgctl-specific: it is what any
  PostgreSQL client on that runner would need.
- **The storage account and key** for the snapshots container, under
  `AZURE_STORAGE_ACCOUNT` / `AZURE_STORAGE_KEY`.
- **Network reach to the database.** On Azure Database for PostgreSQL that means
  the runner's address is allowed by the server's firewall rules.

## The workflow

Committed as `.github/workflows/pgctl-nightly.yml` in the project, not here —
the schedule, the runner label and the secret names belong to the project. The
shape:

```yaml
name: Nightly database snapshot

on:
  schedule:
    # 07:00 UTC — after the overnight batch work, before the working day.
    - cron: "0 7 * * *"
  workflow_dispatch:

# One at a time. Two concurrent snapshots of the same database would both
# succeed and one would be wasted, having read production twice for it.
concurrency:
  group: pgctl-nightly
  cancel-in-progress: false

jobs:
  snapshot:
    runs-on: [self-hosted, azure]
    timeout-minutes: 180
    steps:
      - uses: actions/checkout@v4

      - name: Install the PostgreSQL 17 client and pgctl
        run: |
          # …distribution's postgresql-client-17, then the pinned pgctl release

      - name: Write the connection credentials
        env:
          PGPASS: ${{ secrets.PGPASS }}
        run: |
          install -m 600 /dev/null ~/.pgpass
          printf '%s\n' "$PGPASS" > ~/.pgpass

      - name: Snapshot production
        run: pgctl snapshot --from prd

      - name: Enforce retention
        run: pgctl prune --apply
```

## Refreshing QAT from it

```sh
pgctl apply prd/latest --to qat --confirm qat
```

`prd/latest` resolves to the newest complete snapshot of production. The
snapshot need not be on the machine: pgctl reads its manifest from storage to
plan, and downloads only what the apply will read — which for
`--set claims --widen` is the table of contents and the claims tables' own
files, not the whole nightly.

`--confirm qat` is required because QAT is `guarded`. `--yes` does not waive a
guarded environment; a scheduled refresh has to name its target in the workflow
file, where a reviewer can see it.

## What it will not do

Production is `protected`. There is no flag, no environment variable and no
config key that makes it an apply target.
