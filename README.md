# LEEROY

**L**ong-horizon **E**valuation of **E**mergent **R**easoning, **O**rganisation and **Y**ield

![The run viewer nine hours into the Deadmines run, as the party arranges a training loan for the tank](site/src/assets/media/deadmines-training-loan.jpg)

*Nine hours into a Deadmines run. The tank reached level 14 without enough money to train. The other four players
offered loans in whispers and in a chat channel they had set up, and the paladin sent 10 silver by trade.*

A benchmark for long-running and multi-agent AI. Each agent (Claude Code, Codex or opencode) plays a World of Warcraft
3.3.5a character on a private AzerothCore server, alongside the other agents, through a headless client it controls in
Lua. A judge scores the results, and every run is recorded for the viewer, live or afterwards. Results and write-ups
are at **[leeroy.dev](https://leeroy.dev)**.

## The problem

An MMO is a hard place for an agent, and a realistic one:

- **Open-ended.** There is no single path. Quests, grinding, dungeons, gear, professions, travel and the economy
  all interact, and the right mix depends on class, level, location and who else is around.
- **Real time.** Combat moves faster than a model can think. An agent can't play turn by turn: it has to write
  scripts that play for it, watch them run, and step in when something happens.
- **Partially observable.** The agent sees what a real client sees, from the packets the server sends: nearby
  units and objects, its own state, the combat log, chat. Nothing comes from the server's internals.
- **Slow feedback.** Poor gear, unlearned spells or a bad pull route cost time quietly, long before they kill you.
  Doing better means measuring your own play, not repeating what nearly worked.
- **Shared.** Other agents are in the same world: on the same quests, after the same mobs, able to help or get in
  the way. Some content is meant for groups, and roles (tank, healer, damage) only pay off when players coordinate.

## What we're testing

- **Problem solving over a long run:** planning, measuring results, finding out why something failed (a death,
  a slow stretch) and changing approach, as opposed to brute force.
- **Building tools:** each agent keeps a persistent workspace of its own Lua scripts and notes, and is expected to
  build and reuse a library: combat, navigation, quest and vendor helpers, event handlers.
- **Working with others:** finding partners, agreeing on roles and plans over chat, grouping, sharing kills,
  quests and loot, and staying together. Each player has an individual score and the faction has a combined
  score, so cooperating and competing are both open choices.
- **Different agents in one world:** runs can mix runtimes and models (for example five Claude Code and five
  Codex players), tracked per player and compared in the results.

## A run

Each agent gets a fresh character and a non-GM account, a race, class, spec and role, a time limit (90 minutes to
4 hours for most tasks), and a goal: reach a place, finish a quest or a dungeon, or progress as much as possible
(XP per hour, averaged with the best 15-minute stretch so trying a new strategy isn't punished). In a hardcore run
the first death ends it, and the score is the total XP earned by then. An agent that concludes it can't get further can give up; one that stops early without
giving up is told to carry on. Damage dealt, damage taken and healing done are shown
for each role but not scored. Agents can see the standings during the run. The tasks, and results so far, are in
[docs/benchmarks.md](docs/benchmarks.md).

The SDK gives agents utilities, not strategies: movement, targeting, casting, looting, quests, trading, grouping,
events and a typed view of the world. Rotations, pulling, assisting, healing priorities and group tactics are
theirs to discover and build. The docs they get describe how the game and the SDK work, not what to do.

### The Deadmines, in the viewer

Five Codex agents on GPT-6.1 Sol each started a new level-1 character in Northshire, with the goal of clearing the
Deadmines. They levelled together through Elwynn Forest and Westfall and entered the dungeon at levels 13 to 15, below
its intended 17 to 21. They killed Edwin VanCleef 17 h 41 min into the run, after 115 deaths and 269 quests. The
agents weren't given tactics. The party roles, pulls, crowd control and party-chat shorthand were their own.

| | |
|---|---|
| ![The party questing together in Westfall](site/src/assets/media/deadmines-westfall.jpg) | ![The party running back as ghosts after their first wipe inside the Deadmines](site/src/assets/media/deadmines-ghost-run.jpg) |
| **6 h.** Questing as a party in Westfall. The feed on the right shows what each agent reports it's doing. | **12 h 30.** After the first wipe inside the dungeon, all five run back as ghosts from the graveyard to the entrance in Moonbrook. |
| ![The tank killed by Sneed's Shredder](site/src/assets/media/deadmines-shredder-wipe.jpg) | ![Edwin VanCleef dies and all five earn the Deadmines achievement](site/src/assets/media/deadmines-vancleef.jpg) |
| **13 h 20.** The tank dies to Sneed's Shredder, the first of several wipes there. The others coordinate crowd control and targets in party chat. | **17 h 41.** VanCleef dies with all five alive and the healers out of mana. All five receive the achievement at once. |

## Architecture

```
  agent (Claude Code or Codex, in its workspace)
    │  shell: wow (the game: Lua in the session), azworld (the world's data), standings (the score)
    │  JSON-RPC: script.eval, events.query, events.wait, api.call
    ▼
  azsdk (one daemon per agent)
    ├─ Lua runtime: tasks (coroutines), requests and results, event handlers, shared state
    ├─ session API: me, units, objects, move, combat, spells, items, quests, group, chat, trade, flights...
    ├─ events from server packets → event log (JSONL), recording for the viewer
    ├─ pathfinding on its own navmesh (a player's 50° slopes), collision and terrain data; boats and zeppelins
    └─ WoW 3.3.5a protocol client (auth, world)
    │
    ▼
  AzerothCore (docker: authserver, worldserver, MySQL): stock, plus config
    ▲
  azbench: one supervisor per run: sessions, agents, and a judge per player that reads the character
           database every 15 s → score samples, standings, report card
  azview: the viewer (live or finished runs) from the recordings → map, routes, chat, combat, agent transcripts, scores
  azworld: world queries (NPCs, quests, items, routes, maps) from the world database and the game data
```

The client only uses what a stock server sends, so the same SDK works on any AzerothCore server. Agents run
sandboxed: they can write only in their workspace, read the docs and source, and use the tools above.


| Folder | What's in it |
|---|---|
| [`azsdk/`](azsdk/README.md) | `azsdk`, its own Go module: the headless game client and daemon the agents drive, the agents' `wow` command, the Lua API docs and example scripts. Only a game client: no database access |
| [`cmd/`](cmd) | The benchmark's binaries: [`azbench`](cmd/azbench) runs it (one supervisor per run: sessions, judges, agents; pause, resume, restart; the report card, `publish`, kits, accounts, the agents' `standings`), [`azview`](cmd/azview/README.md) is the viewer and movement audit, [`azworld`](cmd/azworld/README.md) answers agents' world queries (`azworld setup` builds the `wowbench` schema they read) |
| `world/`, `runfiles/`, `gamelog/`, `trajectory/` | Libraries the tools share and others can import: the world database and game data, a run directory's layout, reading game logs, transcripts as ATIF trajectories |
| `internal/` | The harness behind `azbench` (`internal/bench`: the supervisor, judge, configs, diagnostics, accounts) |
| [`suite/`](suite/README.md) | The benchmark's definition: tasks and models (YAML), specs, places, versions, starting kits, and what agents get (`agent/`: their instructions and tools) |
| [`deploy/`](deploy/README.md) | Remote runs on Hetzner Cloud (`just remote-*`) and publishing run downloads to Cloudflare R2 (`just upload`) |
| [`docs/`](docs) | Planning notes and specs, acceptance evidence, the e2e prompt, reference guides (not for agents) |
| [`server/`](server/README.md) | The AzerothCore server: upstream checkout (`azerothcore/`, gitignored) and config overrides (`override.yml`) |
| [`site/`](site/README.md) | The website (Astro): results from `azbench publish`, write-ups and setup docs, deployed to GitHub Pages |
| `data/` | Game data, shared by all of the above (gitignored): `maps/`, `vmaps/`, `dbc/` copied out of the server, the SDK's 50° navmesh `mmaps/`, and from a game install `client/` (minimap, terrain, models) and `roads/` |
| `var/` | What runs produce (gitignored): `runs/` (each run's directory) and `workspaces/` (each agent's) |

All Go code builds with `just build` (binaries in `bin/`; `go.work` ties the root module to `azsdk`); `just` lists every task, and `mise.toml` pins the tools. All
commands below run from the repo root.

## Setup (once)

```sh
# tools: Go, Node, Python, just and the agents' runtimes, at the versions in mise.toml (https://mise.jdx.dev)
mise install

# server
git clone --depth 1 https://github.com/azerothcore/azerothcore-wotlk.git server/azerothcore
just server-build   # ~10-20 min
just up             # the first boot downloads client data and imports the databases

# data/: the server's maps and DBCs, the wowbench schema, and the SDK's own navmesh. The server's navmesh is built
# for creatures (60° slopes); players walk 50° at most, so the SDK gets its own (~15 min on 12 cores)
just setup

# optional, from a 3.3.5a game install, into data/ (nothing reads the install afterwards): the game's minimap (viewer
# and `azworld map`), its road textures (`azworld route`) and the terrain and model files the viewer's zoomed-in map
# draws; ~5 GB for every continent, about 30 s (just data-client <dir> -maps 0 for Eastern Kingdoms only: ~1 GB)
just data-client "<your 3.3.5a game dir>"

# every tool, into bin/ (azbench run does this too)
just build
```

`just` lists every recipe: the server, data, the benchmark, the site, tests and releases. Docker isn't managed by
mise: install it yourself.

The compose project is pinned to `server` (`server/override.yml`), so the volumes are `server_ac-database` and
`server_ac-client-data`. The override also turns Warden off and saves characters every 15 s for the judge.

## Run a benchmark

```sh
bin/azbench run suite/checks/smoke.yaml                 # 2 minutes, no agents: checks the harness end to end
bin/azbench run suite/tasks/hardcore.yaml --model opus  # one model on one task
bin/azbench run --reset suite/tasks/travel.yaml         # every model to Darnassus, from the same start every time
```

`--reset` deletes the task's characters, sets its agents' workspaces aside and restarts the worldserver first, so
repeats start alike.

A task ([suite/tasks/README.md](suite/tasks/README.md)) sets the goal, the clock, the starting conditions and the
party, and lists the models that play it ([suite/models.yaml](suite/models.yaml)): each plays its own run, or all play
one together. Each agent gets a fresh non-GM account and runs as headless Claude Code (or Codex CLI) inside `var/workspaces/<name>/` (its
persistent script library). It can read and write only there, read the client and server source, and run `wow` (the game),
`azworld` (the world's data), `standings` (the score) and read-only shell commands. Results go to `var/runs/<time>-<task>-<model>/`:
- `run.json`: what the run was (task, players, versions) and how it went (start, end, status);
- `supervisor.log`;
- `agents/<agent>/`: the transcript, an [ATIF](https://github.com/harbor-framework/harbor/blob/main/rfcs/0001-trajectory-format.md) trajectory (`trajectory.json`, the same for every runtime), notes and usage;
- `game/<char>/`: the game log (the events its agent saw, its state each second, nearby creatures, API calls) and its client log;
- `judge/<char>/`: samples every 15 s and the final score;
- `diagnostics/`: the harness's and the sessions' diagnostics;
- `team.json` and `report.json` (the report card).

The layout is in [suite/README.md](suite/README.md#run-directories).

Goals: `level:<n>`, `item:<entry>`, `quality:<n>` (3 = rare), `money:<copper>`, `quests:<n>`, `quest:<id>`,
`achievement:<id>`, `achpoints:<n>`, `skill:<id>:<n>`, `zone:<id>` (arrive there), and `score:<n>`: progress per hour (`internal/bench/judge/score.json` sets which signals count; v6 counts XP
earned). For "as much as possible" goals pass a large n and read `value` at timeout. `hardcore: true` in a task
ends a character's run at its first death and scores the total earned by then; `standings give-up "<why>"` ends an agent's run unmet. Team runs, tasks, pausing and
resuming, and resetting characters are in [suite/README.md](suite/README.md); scoring in
[internal/bench/judge/README.md](internal/bench/judge/README.md).

## Replays and live view

Each character's session writes one game log, `game/<char>/events.jsonl` in the run dir (`azsdk -events <file>`): the
events its agent saw (damage, kills, chat, loot, quests...) and, beside them, its state once a second, nearby
creatures, boats and zeppelins, group changes and each API call ([azview](cmd/azview/README.md#game-log)). It is all
client side, so it works on any AzerothCore server.

Viewers ([`cmd/azview/`](cmd/azview/README.md)):
- `bin/azview live [log ...] [--run var/runs/<ts>-team] [--port 8800] [--since 60]`: the viewer at
  http://localhost:8800/, for a running or finished run (`--run` reads a run's game logs, judge samples and agent
  transcripts); the playhead follows the newest data until you seek back.
- `bin/azview movecheck [log]` audits recorded movement for speed, slopes and swimming.

The viewer shows the game's minimap (when client data is extracted, else a terrain map rendered from `data/maps`),
zooming into the game's own terrain and models, with layers for roads, the navmesh, heatmaps, transports and foliage
(a transparency slider); every player's route, a seek bar with key events marked,
and panels synced to the playhead: players, events, chat, combat, agent (API calls), scores (chart + leaderboard)
and groups (current parties, leader, how spread out they are; dashed lines on the map). Colours follow WoW's
conventions: class colours, health/power bar colours, chat-type colours and item quality colours. The URL keeps the
time, map and place, so a link opens on the same moment and a refresh keeps it.

## Run a task by hand

```sh
bin/azbench account agent1 agent1                                 # non-GM account
bin/azsdk -char Agentone -account agent1 -race 1 -class 1 -listen 127.0.0.1:8090 -data data > agent1.log 2>&1 &
bin/azbench judge Agentone level:10 --timeout 4h                  # prints JSON + elapsed_s when met
# ...give the agent the docs (see suite/README.md#the-agent) + the goal; it drives the session with suite/agent/bin/wow
```

## Modules and releases

Two Go modules: `github.com/pthm/leeroy/azsdk` (the client, usable on its own) and `github.com/pthm/leeroy` (the
benchmark tools and their shared packages). Each tool installs on its own, and the packages import with `go get`:

```sh
go install github.com/pthm/leeroy/azsdk/cmd/azsdk@latest   # the client daemon (also cmd/wow, cmd/clientdata)
go install github.com/pthm/leeroy/cmd/azbench@latest       # also cmd/azview, cmd/azworld
go get github.com/pthm/leeroy/azsdk                         # the client as a library
go get github.com/pthm/leeroy                               # world, runfiles, gamelog, trajectory
```

In the checkout, `go.work` (`use . ./azsdk`) builds the two together: the tools always compile against `azsdk/` as
it is on disk, whatever version the root `go.mod` names, so a change to the SDK is used at once, with no tag, push or
`go get`. The root `go.mod` names the SDK version everyone else gets; it moves only when the SDK should reach them:
tag `azsdk/vX.Y.Z` (or just push a commit), then `GOWORK=off go get github.com/pthm/leeroy/azsdk@<tag or commit>` in
the root, and tag the root `vX.Y.Z` when its tools should be installable at that version. Neither `go.mod` carries a
`replace` directive (`go install` refuses one). CI (`.github/workflows/go.yml`) builds both modules with
`GOWORK=off`, as an outside user would.

## License

MIT, see [LICENSE](LICENSE). `azsdk/` is derived from AzerothGhost, also MIT
([`azsdk/LICENSE.AzerothGhost`](azsdk/LICENSE.AzerothGhost)).
