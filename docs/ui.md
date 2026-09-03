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
1 Connections   the servers pgctl can reach
2 Databases       …on the selected connection, discovered from the server
3 Snapshots       …taken from the selected connection
4 Sets            …declared for the selected database
5 Runs          this session's operations
```

Moving the Connections cursor therefore changes what every panel below is
showing. That is why the reachability marker comes first on a connection row:
it answers "can I reach it" before the name answers "which is it", and an
unreachable connection changes what the whole column means.

`1`–`5` jump to a panel, `J`/`K` move between them, `↑↓`/`jk` move within one,
`/` filters the focused panel only — filtering all five from one box would empty
the panels above and below the one being searched, which reads as data loss.

## The detail pane belongs to the focused panel

The tabs on the right are a property of **what is selected**, not of the pane,
so moving between panels changes the questions the pane can answer:

| panel | tabs |
|---|---|
| Connections | Overview · Databases · Config |
| Databases | Tables · Rules · Foreign keys |
| Snapshots | Manifest · Tables · Warnings · Drift |
| Sets | Members · Closure · Load order |
| Runs | Log |

`tab` moves focus into the pane and then cycles its tabs; the selected tab is
remembered per panel, so returning to a panel returns to the tab you were
reading. The chevrons around the strip say that it cycles.

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

Beside the name, `protected` and `guarded` are the two safety flags:

- **`protected`** — never an apply target. There is no flag that changes it, and
  the refusal is the engine's, so the CLI refuses identically.
- **`guarded`** — an apply needs the connection's name typed in full.

## Doing something

`n` snapshot, `a` apply, `m` move, `p` prune, `x` delete a snapshot. Each opens
a form over the frame, showing every field at once so you can see what you have
chosen rather than remember it. `enter` runs it, `esc` cancels.

A destructive operation shows its **plan** first — what would be dropped,
truncated, loaded and rebuilt — and a set-level apply is **refused** unless the
selection is referentially closed. The Closure tab says which tables it reaches
into, and `--widen` accepts them; the refusal names all of them rather than
letting the load fail halfway.

While something runs, the Runs panel is where it lives, `q` cancels it, and
cancelling still runs the engine's failure hooks so an environment that was
scaled down comes back up.

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

Both drive `TestCaptureFrames`, whose nineteen states are the same list the
goldens and the narrow-terminal run walk — so a screen added without a frame is
a screen added without any of them. `go test ./internal/tui -update-goldens`
after an intended layout change, and read the diff.
