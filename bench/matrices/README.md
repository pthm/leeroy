# matrices

Configs for `azbench matrix`: a set of tasks crossed with races, classes, durations and repetitions, in YAML. Every
cell is a solo run with a new character and an empty workspace, so cells don't share notes or scripts.

```sh
bin/azbench matrix bench/matrices/starter.yaml --dry-run   # list the cells and the wall-clock estimate
bin/azbench matrix bench/matrices/starter.yaml
```

| Field | |
|---|---|
| `name` | the run directory is `runs/<time>-matrix-<name>/` |
| `parallel` | cells at once (ports 8090 and up); default 1. Concurrent cells play on the same server |
| `reps` | runs per cell; default 1. Use 2 or more for standard errors |
| `minutes` | a number or a list (every task runs at each); default for tasks without their own |
| `races`, `classes` | names (human, dwarf, night elf, gnome; warrior, paladin, rogue, priest, mage, ...) or ids. Pairs the game doesn't allow are skipped |
| `runtime`, `models`, `provider` | as in a scenario: `claude` (default), `codex`, `opencode` or `none`; runtime to model; `openrouter` |
| `tasks[]` | `key`, `goal` (any judge goal), and optionally its own `minutes`, `races`, `classes`, `reps`, and the starting conditions `level`, `money`, `start` (a place from `places.yaml`, or `"x y z map zone"`), `channel`, `hint` (as in a scenario) |

Goals: `level:10`, `skill:164:75` (Blacksmithing 75), `quest:176` ("Wanted: Hogger": Hogger killed and the
quest turned in), and the rest in the [judge](../../azbench/judge/README.md).

The run directory has `matrix.yaml`, `cells.jsonl`, `results.jsonl` (each cell's judge result plus its cell),
`failed.jsonl` (cells that never got a result), each cell's own run directory (named after its character), and
`summary.json`: the results grouped by task, by task × class and by cell, with n, goals met, time to goal for the
cells that met it, and the judge's value and score as mean ± standard error.
