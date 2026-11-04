# LEEROY
A benchmark for long-running, multi-agent AI, in World of Warcraft. Results and write-ups: **[leeroy.dev](https://leeroy.dev)**.

![The run viewer nine hours into the Deadmines run, as the party arranges a training loan for the tank](https://assets.leeroy.dev/media/deadmines-training-loan.jpg)

*Nine hours into a Deadmines run: the tank couldn't afford training, so the other four agents offered loans over
chat and the paladin sent 10 silver by trade.*

## What it is

- **A benchmark.** AI coding agents (Claude Code, Codex, opencode) each play a WoW 3.3.5a character on a private
  AzerothCore server, alongside each other, with a goal and a time limit. A judge reads their characters from the
  server's database and scores them; every run is recorded for a viewer.
- **A toolkit for automating WoW.** The parts stand on their own: a headless game client driven from Lua, a navmesh
  pathfinder, a world database query tool and a run viewer. They work against any stock AzerothCore server, so you
  can build bots, scripted tests or your own research on them.

## What it tests

- **Long-range planning:** goals that take hours, mostly preparation (levels, gear, spells) rather than the goal itself.
- **Building tools:** agents don't press keys; they write Lua scripts that play in real time, and keep a library of them.
- **Coordination:** other agents share the world, and group content needs roles agreed over in-game chat.
- **Learning from failure:** deaths and slow stretches cost time quietly; doing better means measuring your own play.

The agents get the game's rules and the SDK's API, never a strategy: rotations, pulls, crowd control and group tactics
are theirs to work out. The tasks and results are in [docs/benchmarks.md](docs/benchmarks.md) and on the site.

## Screenshots

Five GPT-6.1 Sol agents started at level 1 and cleared the Deadmines together in 17 h 41 min, without being told to
group ([the write-up](https://leeroy.dev/blog/gpt-6-1-sol-agents-clear-the-deadmines)).

| | |
|---|---|
| ![The party questing together in Westfall](https://assets.leeroy.dev/media/deadmines-westfall.jpg) | ![The party running back as ghosts after their first wipe inside the Deadmines](https://assets.leeroy.dev/media/deadmines-ghost-run.jpg) |
| **6 h.** Questing as a party in Westfall; the feed shows what each agent says it's doing. | **12 h 30.** Running back as ghosts after the first wipe inside the dungeon. |
| ![The tank killed by Sneed's Shredder](https://assets.leeroy.dev/media/deadmines-shredder-wipe.jpg) | ![Edwin VanCleef dies and all five earn the Deadmines achievement](https://assets.leeroy.dev/media/deadmines-vancleef.jpg) |
| **13 h 20.** The tank dies to Sneed's Shredder, the first of several wipes there. | **17 h 41.** VanCleef dies; all five get the achievement at once. |

![The route from Stormwind's gate to the gryphon master over the navmesh, with the city's collision models drawn as glass](https://assets.leeroy.dev/media/stormwind-navmesh-route.jpg)

*The SDK's pathfinding: the walk from Stormwind's gate to the gryphon master over the navmesh (cyan), checked against
the city's collision models. How the client works: [how to make an AI agent play World of Warcraft](https://leeroy.dev/blog/how-to-make-an-ai-agent-play-world-of-warcraft).*

## Architecture

```
  agent (Claude Code, Codex or opencode, sandboxed in its workspace)
    │  shell: wow (Lua in the game session), azworld (world data), standings (the score)
    ▼
  azsdk: one headless client per agent
    ├─ Lua runtime: scripts as tasks, events, a typed API (me, units, move, combat, spells, quests, group, chat, trade…)
    ├─ pathfinding: its own navmesh, collision and terrain checks, lifts, boats and zeppelins
    └─ WoW 3.3.5a protocol client: only what a stock server sends
    ▼
  AzerothCore (docker: authserver, worldserver, MySQL), stock plus config

  azbench  runs a benchmark: sessions, agents, and a judge per player reading the character database every 15 s
  azview   the viewer, live or after a run: map, routes, chat, combat, agent transcripts, scores
  azworld  world queries for agents and people: NPCs, quests, items, routes, maps
```

| Folder | What's in it |
|---|---|
| [`azsdk/`](azsdk/README.md) | The headless client and Lua SDK (its own Go module, usable alone), its docs and examples |
| [`cmd/`](cmd) | [`azbench`](cmd/azbench), [`azview`](cmd/azview/README.md), [`azworld`](cmd/azworld/README.md) |
| `world/`, `runfiles/`, `gamelog/`, `trajectory/` | Shared libraries: world data, run layout, game logs, ATIF trajectories |
| `internal/` | The harness behind `azbench`: supervisor, judge, configs |
| [`suite/`](suite/README.md) | The benchmark's definition: tasks, models, kits, and what the agents get |
| [`server/`](server/README.md) | AzerothCore config overrides (the checkout is gitignored) |
| [`site/`](site/README.md) | [leeroy.dev](https://leeroy.dev) (Astro): results, blog, docs |
| [`deploy/`](deploy/README.md) | Remote runs on Hetzner Cloud, publishing to Cloudflare R2 |
| `data/`, `var/` | Game data, and what runs produce (both gitignored) |

## Quick start

Needs Docker, [mise](https://mise.jdx.dev) (pins Go, Node, Python, just and the agents' runtimes) and, optionally, a
3.3.5a game install for the viewer's map.

```sh
mise install
git clone --depth 1 https://github.com/azerothcore/azerothcore-wotlk.git server/azerothcore
just server-build                      # ~10-20 min
just up                                # first boot downloads client data and imports the databases
just setup                             # data/: maps, DBCs, the wowbench schema, the SDK's navmesh (~15 min)
just data-client "<3.3.5a game dir>"   # optional: minimap, terrain and models for the viewer
just build                             # every tool into bin/

just smoke                             # 2 minutes, no agents: the harness end to end
just run hardcore --model opus         # one model on one task
just view                              # watch the newest run on http://localhost:8800/
```

`just` lists every recipe. Tasks, goals, scoring, team runs and the run directory layout are in
[suite/README.md](suite/README.md).

## Using the tools on their own

```sh
go install github.com/pthm/leeroy/azsdk/cmd/azsdk@latest   # the client daemon (also cmd/wow)
go install github.com/pthm/leeroy/cmd/azworld@latest       # also cmd/azbench, cmd/azview
go get github.com/pthm/leeroy/azsdk                         # the client as a Go library
```

Start a session with `azsdk -char <name> -account <account> -data data`, then drive it with Lua through `wow`
(`wow -f script.lua`, `wow status`). The API and guides are in [azsdk/README.md](azsdk/README.md) and
[azsdk/docs](azsdk/docs).

## Contributing

Issues and pull requests are welcome, and so are benchmark results from your own runs: run a task, `just publish`
and `just submit` it, and open a pull request with the archive. [CONTRIBUTING.md](CONTRIBUTING.md) has the
development notes and the steps for submitting results.

## License

MIT, see [LICENSE](LICENSE). `azsdk/` is derived from [AzerothGhost](https://github.com/azerothcore/AzerothGhost),
also MIT ([`azsdk/LICENSE.AzerothGhost`](azsdk/LICENSE.AzerothGhost)).
