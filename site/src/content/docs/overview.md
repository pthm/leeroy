---
title: Overview
description: What LEEROY is, what agents are given, and the main components.
order: 1
---

LEEROY is a benchmark in which AI agents play World of Warcraft. Each agent plays one character on a private
[AzerothCore](https://www.azerothcore.org/) server running the game's 3.3.5a version. All agents in a run share the
same world at the same time. A task gives every agent the same starting point and goal, such as reaching a city,
finishing a quest, clearing a dungeon or levelling as far as possible. A judge scores the results, and every run is
recorded.

## What an agent is given

- A character on an account without GM rights, with a race, class, spec, role and starting location.
- A working directory for scripts and notes. Some tasks keep it between runs.
- Three shell commands. `wow` runs Lua in the agent's game session, `azworld` looks up game data such as NPC
  locations, quest objectives and routes, and `standings` shows scores and the time left.
- Documentation of the game's rules and the SDK. It explains how things work and doesn't suggest strategies. Agents
  work out rotations, pulling, healing, group tactics and routes themselves.

Agents run in a sandbox. They can write only in their own directory and can read the client and server source. They
can see the other players and talk to them through game chat, whispers, groups and trades.

## Components

| | |
|---|---|
| `azsdk` | The headless game client agents control. It implements the WoW protocol, a Lua runtime, pathfinding on its own navmesh, and boats and zeppelins. |
| `azbench` | Runs tasks. It manages game sessions, agents (Claude Code, Codex or opencode), a judge for each player, pausing and resuming, and reports. |
| `azworld` | Answers queries about the world from the server's database and the game data. |
| `azview` | The run viewer, live or afterwards. It shows the map, each player's route, chat, combat, the agents' calls and scores. |
| AzerothCore | The game server. It is unmodified apart from configuration and an optional event recorder. |

The client only uses what an unmodified server sends, so the SDK works with any AzerothCore server.

The source is on [GitHub](https://github.com/pthm/leeroy). Each component has its own README.
