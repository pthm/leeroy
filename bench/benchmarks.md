# Benchmark tasks

The tasks the benchmark is meant to grow into: what each measures, how it's scored, and what it still needs. For
people planning runs; agents never see this. Tasks are in [tasks/](tasks), the models in
[models.yaml](models.yaml); open tooling work is in [investigate.md](investigate.md).

What makes a good task: a fixed start (fresh characters, `azbench run --reset`), a score no one has to judge (met or
not, then time and cost), partial credit where it can be had so weaker models still spread out, and runs short and
cheap enough to repeat three or more times per model. Report score against cost, not score alone. Documentation
describes the game's systems and rules, never how to win: agents find their own strategies.

## Done

### Travel (smoke test)

`tasks/travel.yaml`: six new level-30 Human warriors in Northshire, one per model, goal: reach Darnassus
(`zone:1657`), 90 minutes. Two boats (Stormwind Harbor to Auberdine, Auberdine to Rut'theran) and a portal.
Scored on time to arrive (3600 / seconds). Three runs on 2026-11-01:

| Model | Arrived | Mean time | Cost per run |
|---|---|---|---|
| GPT-6.1 Terra | 3/3 | 14.8 min | $0.50 |
| GPT-6.1 Sol | 3/3 | 17.6 min | $0.60-0.90 |
| GPT-6 Luna | 3/3 | 20.6 min | $0.10-0.20 |
| Opus 5.5 | 3/3 | 21.1 min | $1.20-1.40 |
| Sonnet 5.5 | 3/3 | 22.5 min | $0.50-1.00 |
| Haiku 5.5 | 2/3 | 33.3 min | $0.10-0.20 |

Haiku's two slow or failed runs were mostly the Rut'theran portal bug (area triggers sent once; fixed): its one
clean run was 26.8 min. A run of all six costs about $4 and ends in about 30 minutes. More reps tighten the middle of
the table (Opus, Sonnet, Luna aren't separated yet).

## Ready

### Hardcore levelling

`tasks/hardcore.yaml`: a new level-1 Human Arms warrior, 4 hours, `hardcore: true`: the first
death ends the run. Score: XP earned over the full 4 hours, so an early death doesn't keep a high rate. Tests caution
apart from speed (the ghost-run shortcut agents found in the first travel run is ruled out). The judge's end on death
is tested against the database, not yet in a real run. Next: a matrix over classes and models; a group version where
any death ends it for everyone.

### Kill Hogger

`tasks/hogger.yaml` (three level-5 characters in Goldshire, Opus, goal: "Wanted: Hogger", quest 176) is the
start. The planned version: from level 1, so levelling, finding Hogger (an elite level 11) and a group fight are all
in it; scored on time and tokens, with partial credit (level 8 or so, Hogger found, engaged, killed). A solo "by any
means" version too. Worth a rerun on today's SDK first.

## Planned: some setup

### Swapping one seat in a fixed team

How to compare models in mixed teams without a judge: four seats stay on one reference model (Sonnet, say), the
fifth takes the model under test, in each role in turn (tank, healer, damage). The score is the team's result
against an all-reference team's: that model's contribution, cooperation included. Low variance (the other four don't
change). On Hogger once it's settled, then Deadmines. Diagnostics from the logs explain it (time near the group, mobs
on someone other than the tank, party deaths, answering teammates in chat, time others waited); a blind judge of the
chat can describe how a model cooperates, but not rank by it. A wider version mixes freely and fits each model's
contribution (TrueSkill or a regression), but needs many more runs.

### Pre-levelled Deadmines

A party of five at level 18-20 in quest-level gear, starting at Sentinel Hill: dungeon cooperation in about an hour
instead of a week from level 1 (deadmines.yaml stays the long flagship run). Scored on bosses killed, wipes, and time
to Edwin VanCleef (`achievement:628`). Needs: giving characters gear (level and start already exist).

### Time-limited levelling

A fixed clock from level 1, scored on XP (`score:` goal), one class for everyone or each class against itself (class
moves the result as much as the model does). A matrix over the existing goal.

### Professions

Skill gained in N hours (`skill:<id>:<n>`): gathering and crafting (mining and blacksmithing: routing, trainers,
vendors, bag space), or cooking or fishing as a cheap version.

## Planned: bigger pieces

### Gold in N hours

Only meaningful as a market: many agents, the auction house on. Scored on wealth (gold plus what everything held
sells for, the judge's `wealth_copper`), with a rule on trading between agents (same-model agents may collude).

### Head-to-head PvP

Model against model, a ranking with no ceiling: a duel tournament first (1v1, same class and gear), then Warsong
Gulch at level 19, one model's team per faction. Needs Horde characters and the battleground queue filling with the
teams.

### One pre-geared boss

A single dungeon or raid boss, scored on attempts to the first kill: with no strategy documented, it measures learning
from failure. Needs gear given out and the encounter reset on a wipe.

## What unblocks what

- Lifts and elevators (investigate.md): any Horde task (Undercity, Thunder Bluff), Northrend (Valgarde), levelling
  through Thousand Needles.
- Giving out gear: pre-levelled Deadmines, the boss.
- Agents that give up (`standings give-up`) and `azbench run --reset`: cheap repeats of any of these.
