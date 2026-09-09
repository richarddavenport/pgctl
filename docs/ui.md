# The interface

What the terminal UI is for, and the rules it follows. The screens themselves
are in [`screens.md`](./screens.md), which is generated from the capture test —
this file is the half a picture cannot tell you.

`pgctl` with no arguments opens it. Every operation it can perform, the CLI can
perform too, against the same engine: the interface is a peer of `pgctl apply`,
not a wrapper around it, and a refusal comes from the engine so both front ends
give the same answer.

## The panels are a hierarchy, not a menu

Five panels down the left, and each one is **about the row selected above it**.

```
[1] Connections   the servers pgctl can reach
[2] Databases       …on the selected connection, discovered from the server
[3] Snapshots       …taken from the selected connection
[4] Sets            …declared for the selected database
[5] Runs          this session's operations
```

The number is bracketed because it is a **key**, not a quantity: bare, it sat
beside a count in parentheses and read as two numbers about the panel, one of
which is not about the panel at all.

Moving the Connections cursor therefore changes what every panel below is
showing. That is why the reachability marker comes first on a connection row:
it answers "can I reach it" before the name answers "which is it", and an
unreachable connection changes what the whole column means.

`h`/`l` move between the panels and `j`/`k` within one, the way they do in
lazygit, lazydocker and swarmctl — the arrows do the same thing, `1`–`5` jump
straight to a panel, and `g`/`G` go to the first and last row. `/` filters the
focused panel only: filtering all five from one box would empty the panels above
and below the one being searched, which reads as data loss.

## The detail pane belongs to the focused panel

The tabs on the right are a property of **what is selected**, not of the pane,
so moving between panels changes the questions the pane can answer:

| panel | tabs |
|---|---|
| Connections | Overview · Config |
| Databases | Tables · Rules · Foreign keys |
| Snapshots | Manifest · Tables · Warnings · Drift |
| Sets | Members · Closure · Load order |
| Runs | Steps · Log |

`[` and `]` cycle the tabs, from either side of the divide — which tab the pane
shows is a question about what you are reading, so flipping from Manifest to
Warnings while your cursor stays on the snapshot you are choosing is the
ordinary way to use it. `tab` crosses the divide when you want to scroll the
pane itself, and comes back.

The selected tab is remembered per panel, so returning to a panel returns to the
tab you were reading. The chevrons around the strip say that it cycles. A tab
longer than the pane scrolls, and the bar down its right edge says where in it
you are — a 213-table manifest is the normal case here, not an edge one.

The **Rules** tab is the one worth a word here, because it is the only tab that
shows *config* rather than *state*: a rule says how much of a table a snapshot
carries — everything, the rows matching a predicate, or nothing at all — and the
tab shows each rule with how many tables it matched on this database. A rule that
matched **nothing** is coloured, because that is what a renamed table leaves
behind and it is otherwise invisible. [`config.md`](./config.md) has the rest,
including which rule wins when two match the same table.

## What the markers mean

A connection's first column is its reachability, and it is a **shape** as well
as a colour, so it survives a monochrome terminal and a highlighted row:

| | |
|---|---|
| `○` | not reached yet — pgctl has not tried |
| spinner | probing |
| `●` | reachable |
| `✗` | unreachable; the Overview tab has the error in full |

**Probing is lazy.** Opening the interface reaches only the selected
environment, and moving to one reaches it. Probing everything on startup meant
opening the interface opened a session to production, which is not something a
tool should do because you launched it. `r` asks for all of them.

The right-hand column is the **safety flag**, and nothing else:

- **`protected`** — never an apply target. There is no flag that changes it, and
  the refusal is the engine's, so the CLI refuses identically.
- **`guarded`** — an apply needs the connection's name typed in full.

A blank there is correct rather than missing data: the rows carrying text are
exactly the rows that will argue with you. The column used to hold the server
version as well, with a flag displacing it — one column doing three jobs, which
read as a gap. The version is a fact about the server and lives with the other
facts about it, on the **Overview** tab.

## Doing something

`n` snapshot, `a` apply, `m` move, `p` prune, `x` delete a snapshot. Each opens
a form over the frame, showing every field at once so you can see what you have
chosen rather than remember it. `enter` runs it, `esc` cancels, and the keys are
on the form's own bottom row rather than in the frame's footer: the one place a
reader looks when a box appears in front of them should say how to leave it.

**A panel selection is a DEFAULT, not an answer.** `n` opens with panel 2's
database chosen and every other one on the connection offered beside it; `↑↓`
moves, `space` chooses, `a` takes all — which is what the nightly does. The
connection is *not* asked: panel 1 is the only statement of which server this
is, and the field that used to ask it once offered one server's name beside
another server's database names.

That distinction was learned rather than designed. For a while the form asked
nothing and took panel 2's cursor as the answer, and in use it was
unanswerable — there were **two** lists of databases on screen, panel 2 and the
Connections pane's own Databases tab, each with its own cursor, and `n` acted on
one while the reader was looking at the other. The duplicate tab is gone, and
the form asks anyway, because a snapshot covering several databases is a thing
people want and no single cursor can say it.

`m` still moves one database, from the panel. A move drops and reloads a
database on the target, so six at once is an hour of somebody's environment
being unusable.

**It also asks where the snapshot goes**, because no panel says that: the config
declares the destinations and the answer differs from one run to the next.
Nothing is pre-chosen, and `enter` with nothing chosen is a refusal rather than
a default — a snapshot is gigabytes and a shared account is not somewhere to end
up by accident. Choosing a remote and *not* `local` is the "do not fill my
laptop" answer: it uploads, then deletes the local copy, and the row under the
list says so. A config with no remotes has one destination and is not asked
about.

The cost is that the interface takes one database at a time. `pgctl snapshot
--from <conn> --to-storage <where>` with no `--db` still covers every declared
database, which is what the nightly runs.

A field that cannot apply is **disabled, not hidden** — widening means nothing
to a whole-database apply — because a field that vanishes is one the operator
has to re-find, and the row under the cursor always explains itself in the same
place.

**`ctrl+p` is the whole list**, with the key that runs each one, and a command
that cannot run is listed with the reason rather than left out: "apply — no
snapshot selected", "move — every other connection is protected". Running an
entry presses its key, so the directory cannot drift from the keyboard.

A destructive operation shows its **plan** first — what would be dropped,
truncated, loaded and rebuilt — and a set-level apply is **refused** unless the
selection is referentially closed. The Closure tab says which tables it reaches
into, and `--widen` accepts them; the refusal names all of them rather than
letting the load fail halfway.

While something runs, the Runs panel is where it lives, and it has two tabs
because there are two questions. **Steps** is where it has got to: the phases
come from the engine's own event stream, one glyph each and what each one cost,
with a bar above them where there is a real fraction to show — an apply knows
how many tables it will load, and a snapshot does not until `pg_dump` has read
the catalog. **Log** is what it said, in order, tailing, scrollable back.

`q` leaves, here as everywhere — it is one of the four keys tuikit reserves. A
run in flight makes leaving expensive, so it asks first, and answering the
question cancels the operation rather than abandoning it: the engine's
`onFailure` and `postApply` hooks are what bring an environment that was scaled
down back up. `ctrl+c` does the same without the question, because ctrl+c is the
terminal's own "stop" and a tool that puts a dialog in front of it has taken
away the one key a reader is certain of.

A refusal arrives as a bounded note in the corner with `esc` to dismiss it,
because pgctl's refusals are sentences — the tables a selection reaches into,
the extension a target cannot install — and a header row shared with the config
path truncated the one message in the tool most worth reading in full.

`?` is every binding, by screen. The footer names only what acts on what is
focused right now — a footer that listed every action on every panel would grow
a letter per feature and read as a menu of things mostly not applicable.

## Looking at it without a database

The interface used to need a live server to render at all, which is why nobody
had looked at most of it. It does not now:

```sh
make frames     # regenerate docs/screens.md and its SVGs
make watch      # recapture on save, reload the browser, while building a screen
```

Both drive `TestCaptureFrames`, whose forty states are the same list the
goldens, the narrow-terminal run, the colour check and the fits-its-terminal
check all walk — so a screen added without a frame is a screen added without any
of them, and the tab bodies are generated from `paneTabs()` rather than listed.
`go test ./internal/tui -update-goldens` after an intended layout change, and
read the diff.

The interface this replaced is in [`_attic/tui`](../_attic) with its own 38
goldens, unbuilt. It is what "parity" means when there is a question about
whether a screen still says something it used to.
