---
title: Running a task
description: Running a scenario, watching it, reading the results and publishing them to this site.
order: 3
---

A task is a scenario file in `bench/scenarios/`. It lists the agents, their characters, the goal, the time limit and
the starting conditions.

```sh
bin/azbench run bench/scenarios/smoke.yaml                     # 2 minutes, no agents; tests the harness
bin/azbench run --reset bench/scenarios/travel-models-2.yaml   # six models travel to Darnassus, from a clean start
```

`--reset` deletes the scenario's characters, moves the agents' directories aside and restarts the world server, so
repeated runs start from the same state. One supervisor process manages the run. It logs in every character, starts
each agent with its prompt, runs a judge for each player and stops everything at the end. Paused runs can be resumed,
and an agent that crashes can be restarted by itself.

## Watching

```sh
bin/azview live --run bench/runs/<time>-<scenario>    # http://localhost:8800/
```

The viewer follows a run live or replays a finished one. It shows the map, each player's route, a timeline with
deaths, level-ups and achievements marked, and panels for chat, combat, the agents' API calls and scores. With client
data extracted, the map uses the game's own terrain and models. The URL stores the time and position, so a link opens
at the same moment.

## Results

Each run writes `bench/runs/<time>-<scenario>/`. For each agent it holds the judge's result, progress every 15
seconds, the transcript and the session log. For the run it holds `report.json`, with scores, cost, tokens, deaths and
time use, and `team.json`. `azbench compare` combines several runs by model.

## Publishing to this site

```sh
bin/azbench publish bench/runs/<time>-<scenario> --task travel
```

writes `site/src/data/runs/<run>.json`. It contains the scenario and, for each player, the model, reasoning effort,
outcome, time, cost, tokens, progress from the judge's samples, route, deaths, time use and most frequent errors.
Killer and zone names come from the world database, so the server must be running. It also writes a map for each
area the run visited to `site/src/assets/runs/`, using the viewer's detail map in greyscale or a dungeon's floor plan.
Commit both and the site rebuilds on push.

The results pages group runs by `--task` and by benchmark version, and each run gets its own page.

## Versions

`bench/versions.yaml` lists the benchmark's versions. Add an entry when a change makes new results not comparable
with older ones, such as a new SDK capability, a navmesh or scoring change, or a large change to the agents' docs.
Each entry has a version label, the date it took effect and a short note. `azbench run` records the current version,
the last entry, in each run's `version.json` along with the SDK revision. `publish` reads it, or for older runs uses
the version in force on the run's start date. The results pages show the latest version by default, with a selector
for older versions and for all versions together. A task only appears for the versions it has runs in. A run page can also include written notes
in `site/src/content/runs/<run id>.md`, with a title, a summary, key findings and an account of the run.
