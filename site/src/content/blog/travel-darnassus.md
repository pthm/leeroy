---
title: Travel from Northshire to Darnassus
description: Six models each took a new warrior from Northshire to Darnassus, three times. All but one attempt arrived. The differences were in time and cost.
date: 2026-11-01
---

The travel task is a basic test of getting around before harder tasks. A new level-30 Human warrior starts in
Northshire and has to reach Darnassus, the night elf capital on the other continent.

The agents aren't given a route. The trip involves walking to Stormwind's harbour, a boat to Auberdine in Darkshore, a
second boat to Rut'theran Village and a portal into Darnassus. The agents have the game's rules and tools for looking
up the world, such as NPC locations, boat schedules and roads.

Six models ran at the same time, each on its vendor's own harness. Haiku, Sonnet and Opus 5.5 ran on Claude Code, and
GPT-6 Luna, GPT-6.1 Terra and GPT-6.1 Sol on Codex. Every character had the same class, start, 90-minute limit and a
neutral name. We ran the task three times.

## Results

| Model | Arrived | Mean time | Cost per run |
|---|---|---|---|
| GPT-6.1 Terra | 3/3 | 14.8 min | $0.50 |
| GPT-6.1 Sol | 3/3 | 17.6 min | $0.60–0.90 |
| GPT-6 Luna | 3/3 | 20.6 min | $0.10–0.20 |
| Opus 5.5 | 3/3 | 21.1 min | $1.20–1.40 |
| Sonnet 5.5 | 3/3 | 22.5 min | $0.50–1.00 |
| Haiku 5.5 | 2/3 | 33.3 min | $0.10–0.20 |

The [results page](../../results/travel) has the current table and every run.

## Problems found

Most of what the test found were bugs in our own tools.

**Boats.** An earlier attempt ran before the SDK supported boats and isn't counted. Agents now board from where a
boat's crew stand on deck, and the SDK works out the boat's position the same way the server does.

**The Rut'theran portal.** Most of Haiku's slow and failed attempts came from one bug. The SDK sent the portal's area
trigger to the server once, but the server checks triggers against its own position for the player, which lags
slightly. Walking through the edge of the portal could miss it. The SDK now re-sends the trigger while the player is
inside it. In testing, edge walks went from one failure in four to ten successes in ten. Haiku's one attempt without
the bug took 26.8 minutes.

A run of all six models costs about $4 and takes about half an hour, so it is cheap to repeat. Three runs aren't
enough to separate Opus, Sonnet and Luna, which are close.
