# judge

Scores one character by polling the character database (the server's record, not the client's). The server saves
characters every 15 s (`server/override.yml`), and the judge polls every 15 s by default.

| File | |
|---|---|
| `judge.ts` | the judge: samples, rates, score, board, exit when the goal is met or time runs out |
| `signals.ts` | every progress signal for a character; `bun bench/judge/signals.ts <Char>` prints them |
| `score.json` | the weights for `score:` goals, and their version |

## judge.ts

```sh
bun bench/judge/judge.ts <Character> <goal> [flags]
bun bench/judge/judge.ts Agentone level:10 --timeout 4h
```

| Goal | Value |
|---|---|
| `level:<n>` | character level |
| `money:<copper>` | money held |
| `quests:<n>` | quests turned in |
| `quest:<id>` | 1 once that quest is turned in |
| `item:<entry>` | 1 once the character owns that item |
| `quality:<0-5>` | 1 once it owns any item of that quality or better (3 = rare) |
| `achpoints:<n>` | achievement points earned since the judge started (needs `bun azeroth-sdk/worlddata/dbsetup.ts`) |
| `score:<n>` | weighted change in the signals since the judge started (weights in `score.json`) |

The goal is met when the value reaches n (1 for `quest`, `item`, `quality`). For "as much as possible", pass a large
n and read the value at timeout.

| Flag | |
|---|---|
| `--timeout 4h` | stop after this long (`s`, `m` or `h`); default 4h |
| `--every 15s` | poll interval; default 15s |
| `--samples <file>` | append each sample as a JSON line |
| `--record <file>` | append each sample as an `{"ev":"score"}` event to a recorder log, for the replay and live viewers |
| `--board <dir>` | write `<dir>/<Char>.json` with the latest result each poll (read by `standings`) |
| `--events <file>` | the character's event stream (`azsdk -events`); damage dealt, damage taken and healing done since the start go on the board |
| `--role <DPS\|Tank\|Healer>` | added to the board |
| `--runtime <name>` | added to the board and the result |
| `--resume <samples.jsonl>` | continue a paused run: same baseline and curve, and the clock resumes at the last sample |

On exit it prints one JSON line and exits 0 if the goal was met, 1 if it timed out.

## Rates and score

Computed for every goal from the value curve, in value per hour:

| Field | |
|---|---|
| `rate` | value / hours since the start (the first 10 minutes count as 10) |
| `recent_rate` | over the last 10 minutes |
| `best_window` | the best rate over any 15-minute window (before 15 minutes: value / max(elapsed, 10 min)) |
| `pace` | time-average of the value so far (area under the curve / time) |
| `score` | `(rate + best_window) / 2` |

`score` is the headline for `score:` goals. Time spent trying a new strategy lowers `rate` but not `best_window`;
deaths and stalls cost through the time they lose.

## score.json

Version 6: progress is `xp_earned` with weight 1, so `score` is in XP per hour. Gear, money and the other signals
are reported in each sample but not scored. Change the weights here to score on other signals.

## signals.ts

| Signal | |
|---|---|
| `level` | fractional: 10.5 is halfway to 11 |
| `xp_earned` | total XP to reach the current level plus XP into it |
| `achievement_points` | from `wowbench.achievement` |
| `avg_item_level` | mean over 17 gear slots (shirt and tabard excluded), empty = 0 |
| `wealth_copper` | money plus vendor value of everything carried, worn or banked |
| `money_copper` | money |
| `honor`, `arena_points`, `honorable_kills` | PvP |
| `quests` | quests turned in |
| `reputation` | sum of standings |
| `profession_skill` | sum of first aid, cooking, fishing and primary profession skills |
| `explored_areas`, `flight_paths` | bits set in the explored-zones and taxi masks |
| `deaths`, `creatures_killed` | from the achievement statistics |
| `played_s` | played time |

It also exports `sql` (a query on `acore_characters` via `docker exec ac-database`), `weights` and `composite`.

## Outputs

A sample (`--samples`, and the last one in the final result):

```
{elapsed_s, level, xp, money, value, rate, recent_rate, best_window, pace, score, signals}
```

The final line adds `char`, `goal`, `met`, `runtime` (if given) and `at` (ISO time). The board file has `char`,
`level` (fractional), `progress` (the value), the rates, `elapsed_s`, `money`, `avg_item_level`, and `role`,
`runtime` and `damage_dealt`, `damage_taken`, `healing_done` when those flags are given.
