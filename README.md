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
`zstd` compression, and pgctl's speed starts there.

Nothing else. There is no credential store to set up: pgctl connects the way
`psql` does.

## Installing

```sh
curl -fsSL https://raw.githubusercontent.com/richarddavenport/pgctl/master/install.sh | bash
```

The repository is private, so `install.sh` fetches the release asset through the
`gh` CLI rather than plain `curl` — a token is needed either way, and `gh`
already has one.

To change pgctl rather than run it, clone it and `make install`. [tuikit] is a
tagged public module, so nothing else has to be checked out beside it.

[tuikit]: https://github.com/richarddavenport/tuikit

## Getting started

With no configuration at all, pgctl connects wherever `psql` with no arguments
would:

```sh
pgctl                                 # the terminal UI
pgctl snapshot --from default         # every database on that server
```

Naming your servers takes a `pgctl.yaml`:

```sh
pgctl snapshot --from prd --to-storage snapshots   # where it goes is asked, not assumed
pgctl ls                              # what is on disk and in every remote
pgctl plan prd/latest --to qat        # what a refresh would do
pgctl apply prd/latest --to qat       # do it
pgctl move --from prd --to qat        # refresh without keeping a snapshot
pgctl prune                           # what retention would remove
```

Restore a slice instead of everything:

```sh
pgctl plan  prd/latest --to qat --set claims
pgctl apply prd/latest --to qat --set claims --widen
```

`--widen` accepts the referential closure of the selection. Without it, a
selection that is not closed is refused and the missing tables are named.

## Configuration

`pgctl.yaml`, committed next to the schema it describes. Every key is optional.

```yaml
connections:
  prd:
    dsn: "service=prd"      # anything libpq accepts
    protected: true         # never an apply target
  qat:
    dsn: "service=qat"
    guarded: true           # an apply needs the name typed in full
  local: "postgres://localhost/postgres"

databases:
  exclude: [postgres]       # the rest are discovered from the server

sets:
  - name: claims
    database: product-development
    include: ["claims.*"]

rules:
  - table: quotes.quote
    where: "created_at > now() - interval '30 days'"
    why: "20 GB of jsonb payloads; recent quotes are enough to work with"
```

[`docs/config.md`](./docs/config.md) explains every section in prose — what a
rule does to a table, which rule wins when two match, what a set is closed
against — and `pgctl.example.yaml` is the whole surface as a commented file.

### Credentials

There are none in the config, and pgctl has no credential store of its own.

A `dsn` is a libpq connection string: a service name (`service=prd`, resolved
from `~/.pg_service.conf`), a URI, or keyword pairs. pgctl resolves it with pgx
and hands the identical string to `pg_dump` and `pg_restore` — so both halves of
an operation connect by exactly the same rules, and the password comes from
`~/.pgpass` or `PGPASSWORD` as it does for every other PostgreSQL tool.

The service file is libpq's own, at libpq's own path, so `psql service=prd`,
`pg_dump service=prd` and pgctl all read the same definitions with nothing
exported and nothing configured. To keep it somewhere else, set `PGSERVICEFILE`
— that is libpq's mechanism and pgctl neither sets it nor needs to know.

```
# ~/.pg_service.conf
[prd]
host=postgres.example.com
user=dbadmin
dbname=product-development
sslmode=require

# ~/.pgpass   (chmod 600)
postgres.example.com:5432:*:dbadmin:the-password
```

The trade is deliberate: connection details are per-machine rather than shared
through the repository. Each person sets theirs up once, and CI writes a
`.pgpass` from its secret store — which is the same thing every other
PostgreSQL tool in the stack already needs.

## Safety

- An environment marked `guarded` needs its name typed in full before an apply.
  `--yes` does not waive it; a non-interactive run must pass `--confirm <env>`.
- A `protected` environment can never be an apply target, and there is no flag
  to turn that off. With no `protect:` key, every guarded environment is
  protected.
- A snapshot that did not finish cannot be applied.
- A snapshot missing a table the target has is refused, not loaded partially.

## Design

`design/dba-surface.md` is the enumeration of database administration work —
fifteen domains, grounded in what this project's database actually reports, with
a verdict per domain on whether pgctl is the right home for it. Nothing there is
built except where it says so.

`design/maintenance.md` is the open notebook on PostgreSQL maintenance — what
pgctl already does after a restore, what it could report, and what it should
stay out of. Nothing there is built except where it says so.

`design/decisions.md` records every decision and the reasoning behind it —
directory format, zstd, why filtered tables use binary `COPY`, why constraints
are rebuilt `NOT VALID` and validated separately, and why masking is a seam with
no rules in it yet.

## Licence

MIT — see [`LICENSE`](./LICENSE).
