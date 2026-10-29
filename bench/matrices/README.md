# matrices

Configs for `../matrix.sh`: a set of tasks crossed with races, classes, durations and repetitions. Every cell is a
solo `bench.sh` run with a new character and an empty workspace, so cells don't share notes or scripts.

```sh
DRY=1 bench/matrix.sh bench/matrices/starter.json   # list the cells and the wall-clock estimate
bench/matrix.sh bench/matrices/starter.json
```

| Field | |
|---|---|
| `name` | the run directory is `runs/<ts>-matrix-<name>/` |
| `parallel` | cells at once (ports 8090 and up); default 1. Concurrent cells play on the same server |
| `reps` | runs per cell; default 1. Use 2 or more for standard errors |
| `minutes` | a number or a list (every task runs at each); default for tasks without their own |
| `races`, `classes` | ids (race 1 Human, 3 Dwarf, 4 Night Elf, 7 Gnome; class 1 Warrior, 2 Paladin, 4 Rogue, 5 Priest, 8 Mage, ...). Pairs the game doesn't allow are skipped |
| `tasks[]` | `key`, `goal` (any judge goal), and optionally its own `minutes`, `races`, `classes`, `reps`, and the starting conditions `level`, `money`, `start`, `channel`, `hint` (as in `bench.sh`) |

Goals: `level:10`, `skill:164:75` (Blacksmithing 75), `quest:176` ("Wanted: Hogger": Hogger killed and the
quest turned in), and the rest in [judge/README.md](../judge/README.md).

`summary.json` groups the results by task, by task × class and by cell: n, goals met, time to goal for the cells
that met it, and the judge's value and score as mean ± standard error.
