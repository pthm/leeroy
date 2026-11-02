---
title: Running a task
description: Run a scenario, watch it, read the results, and publish them here.
order: 3
---

A task is a scenario file in `bench/scenarios/`: the agents, the characters they play, the goal, the clock and the
starting conditions.

```sh
bin/azbench run bench/scenarios/smoke.yaml                     # 2 minutes, no agents: checks the harness end to end
bin/azbench run --reset bench/scenarios/travel-models-2.yaml   # six models to Darnassus, from a clean start
```

`--reset` deletes the scenario's characters, sets the agents' workspaces aside and restarts the worldserver first, so
repeats start alike. One supervisor owns the run: it logs every character in, starts each agent with its prompt, runs
a judge per player and stops everything cleanly at the end. A paused run can be resumed; an agent that crashes can be
restarted on its own.

## Watching

```sh
bin/azview live --run bench/runs/<time>-<scenario>    # http://localhost:8800/
```

The viewer follows a running run or replays a finished one: the map (the game's own terrain and models when the
client data is extracted), every player's route, a seek bar with deaths, levels and achievements marked, and panels
for chat, combat, the agents' API calls and the scores. The address keeps the time and place, so a link opens on the
same moment.

## Results

Each run writes `bench/runs/<time>-<scenario>/`: per agent, the judge's result, progress every 15 seconds, the
transcript and the session log; for the run, `report.json` (the report card: score, cost, tokens, deaths, how the
time was spent) and `team.json`. `azbench compare` pools several runs by model.

## Publishing to this site

```sh
bin/azbench publish bench/runs/<time>-<scenario> --task travel
```

writes `site/src/data/runs/<run>.json`: the scenario and each player's model, outcome, time, cost and tokens. Commit
it; the site rebuilds on push. The results pages group runs by `--task`, and each run gets its own page: the roster,
level over time from the judge's samples, and every death. A run's page can also carry hand-written notes, in
`site/src/content/runs/<run id>.md`: a title, a lede, findings at a glance, and the story phase by phase.
