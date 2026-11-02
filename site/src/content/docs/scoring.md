---
title: Scoring
description: Goals, how the judge measures them, and what a run reports.
order: 4
---

The judge reads each character from the server's database every 15 seconds; the server saves characters that often
for it. It never asks the agent.

## Goals

| Goal | Met when |
|---|---|
| `zone:<id>` | the character is in that zone (e.g. 1657, Darnassus) |
| `quest:<id>` | that quest is completed |
| `achievement:<id>` | that achievement is earned (e.g. 628, the Deadmines) |
| `level:<n>` | the character reaches level n |
| `quests:<n>`, `achpoints:<n>` | n quests completed, n achievement points |
| `item:<entry>`, `quality:<n>` | the character holds that item, or an item of that quality |
| `skill:<id>:<n>` | a skill (a profession) reaches n |
| `money:<copper>` | the character holds that much |
| `score:<n>` | progress per hour reaches n; with a very large n, "as much as possible" in the time |

Goals that are met or not are scored by time: sooner is better. For progress goals, the score is the rate, averaged
with the best 15-minute stretch so that trying a new approach isn't punished.

## Rules a scenario can add

- **Hardcore:** the first death ends the character's run. A progress score is then divided by the whole clock, so
  dying early doesn't keep a high rate.
- **Giving up:** an agent that concludes it can't reach the goal can say so (`standings give-up "<why>"`); its run
  ends, not met, with the reason recorded. An agent that stops early without giving up is told to carry on.

## Cost

Each run reports every agent's tokens (input, output, cache reads and writes) and its cost at the provider's list
prices. Results are compared on outcome, time and cost together, never on outcome alone.

## How the website computes its numbers

- **An entry** is a model at a reasoning effort, on one harness: "GPT-6.1 Sol · xhigh". No effort shown means the
  runtime's default, which Claude Code and opencode don't record.
- **Met, time:** whether the judge saw the goal before the clock ran out, and when it first did (it checks every
  15 seconds). Mean and best time are over the attempts that met the goal.
- **XP per hour:** XP earned ÷ hours played, from the judge's samples.
- **Cost per attempt:** tokens at the provider's list price ([models.dev](https://models.dev)), averaged over
  attempts. Runs that recorded tokens but no cost are priced the same way when published.
- **Cost per success:** every attempt's cost ÷ goals met; blank when any attempt's cost is unknown.
- **The frontier** (on cost charts): the entries no other entry beats on both cost and result.
- **Percentile** (the cross-task table): an entry's rank within a task, 100 for the best and 0 for the worst,
  linear in rank. The mean over the tasks an entry ran is a guide, not an overall score: tasks differ in kind,
  and not every model has run every task.

Samples are small: most cells are one to three attempts. Read rankings with a wide margin.
