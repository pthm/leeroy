---
title: 'A smoke test: Northshire to Darnassus'
description: Six models, six new warriors, one trip across two continents. Every model arrives; how fast, and for how much, separates them.
date: 2026-11-01
---

Before asking agents to clear dungeons, it's worth knowing whether they can get around. The travel task is our smoke
test: a new level-30 Human warrior in Northshire must reach Darnassus, the night elf capital, on the other continent.

The way there isn't given. It takes a walk to Stormwind's harbour, a boat to Auberdine in Darkshore, a second boat to
Rut'theran Village, and the portal up into Darnassus. The agents get the game's rules and the tools to read the
world (where NPCs stand, what the boats' schedules are, the roads), not a route.

Six models ran it at the same time, each on its own vendor's harness: Claude Code with Haiku, Sonnet and Opus 5.5, and
Codex with GPT-6 Luna, GPT-6.1 Terra and GPT-6.1 Sol. Same class, same start, same 90-minute clock, neutral character
names. We ran it three times.

## Results

| Model | Arrived | Mean time | Cost per run |
|---|---|---|---|
| GPT-6.1 Terra | 3/3 | 14.8 min | $0.50 |
| GPT-6.1 Sol | 3/3 | 17.6 min | $0.60–0.90 |
| GPT-6 Luna | 3/3 | 20.6 min | $0.10–0.20 |
| Opus 5.5 | 3/3 | 21.1 min | $1.20–1.40 |
| Sonnet 5.5 | 3/3 | 22.5 min | $0.50–1.00 |
| Haiku 5.5 | 2/3 | 33.3 min | $0.10–0.20 |

The live table, with every run, is on the [results page](../../results/travel).

## What it found

Mostly bugs in our own tooling, which is what a smoke test is for:

- **Boats.** The first attempt predates boat support in the SDK, and isn't counted. Boarding now works from where a
  transport's crew stand on its deck, with the ship's position worked out the same way the server does.
- **The Rut'theran portal.** Haiku's slow and failed runs were mostly one bug: an area trigger (the portal) was sent
  to the server once, but the server checks it against its own, slightly lagging, idea of where you are. Walking
  through the edge of the portal could miss. The SDK now re-sends it while you stand inside; edge walks went from one
  failure in four to ten passes in ten. Haiku's one clean run took 26.8 minutes.

A run of all six costs about $4 and ends in about half an hour, so it's cheap to repeat. Three runs don't separate
the middle of the table yet (Opus, Sonnet and Luna); more will.
