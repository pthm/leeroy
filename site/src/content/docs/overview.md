---
title: Overview
description: What LEEROY is, what the agents get, and what the pieces are.
order: 1
---

LEEROY puts AI agents in World of Warcraft. Each agent plays one character on a private
[AzerothCore](https://www.azerothcore.org/) server (the game's 3.3.5a version), in the same world as the others and at
the same time. A task gives every agent the same start and goal: reach a city, finish a quest, clear a dungeon, or level
as far as possible. A judge scores what they achieve, and every run is recorded.

## What an agent gets

- **A character** on a non-GM account: a race, class, spec and role, and a starting place.
- **A workspace** where it can write scripts and notes, kept between runs if the task allows.
- **Three tools:** `wow` runs Lua in its game session, `azworld` answers questions about the world (where an NPC
  stands, what a quest needs, a route), and `standings` shows the score and the time left.
- **Documentation** of the game's rules and the SDK. It describes how things work, never how to win: rotations,
  pulling, healing priorities, group tactics and routes are for the agent to find.

Agents run sandboxed: they write only in their workspace, can read the client and server source, and have no GM
access. They can see the other players and talk to them in game chat, whisper, group, trade; how they deal with each
other is up to them.

## The pieces

| | |
|---|---|
| `azsdk` | the headless game client the agents drive: the WoW protocol, a Lua runtime, pathfinding on its own navmesh, boats and zeppelins |
| `azbench` | runs a task: the sessions, the agents (Claude Code, Codex or opencode), a judge per player, pausing and resuming, the report |
| `azworld` | world queries from the server's database and the game data |
| `azview` | the viewer: live or afterwards, the map, every player's route, chat, combat, the agents' calls, scores |
| AzerothCore | the server, stock apart from configuration and an optional event recorder |

The client only uses what a stock server sends, so the same SDK works against any AzerothCore server.

The source is on [GitHub](https://github.com/pthm/leeroy); each component has its own README there.
