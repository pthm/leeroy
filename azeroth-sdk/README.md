# azeroth-sdk

A headless World of Warcraft 3.3.5a client for building agents on an [AzerothCore](https://github.com/azerothcore/azerothcore-wotlk)
server. `azsdk` keeps characters logged in and serves a JSON-RPC API; agents drive them with Lua that runs inside
the daemon (live evaluations, tasks, an `on_tick` called every 200 ms), through the RPC API or the bench's `wow` CLI.

The client never plays on its own: no built-in AI, no auto-loot, no auto-combat. Movement follows the server's
navmesh at the speed the server sets, swims in deep water, and stops when rooted.

## How it fits together

```
worldserver ──> client ─────> session ─────> script ──────> api ─────────> agent (wow)
                protocol,     one Bot per    Lua engine,    JSON-RPC
                objects       character      event bus      POST /rpc
```

- **client** speaks the protocol: SRP6 login on the authserver, the encrypted world session, the object model built
  from update packets, and typed hooks (`AddPacketHook`, `AddAuraHook`, ...) for everything above it.
- **session** runs one character (`Bot`): login (creating the character if needed), then a tick loop. It registers
  the Lua API (namespaces `me`, `units`, `move`, `items`, ...; see `docs/lua-api.md`), decodes server packets into
  typed events and action results (values from `types`), and emits them on the character's event bus.
- **script** is the Lua runtime (gopher-lua): one state per character shared by live evaluations and running
  scripts, a coroutine scheduler for tasks, the event `Bus` and action requests. Each tick (`Engine.CallTick`)
  delivers events, polls pending requests, resumes tasks and calls `on_tick`. See `docs/runtime.md`.
- **api** serves the engines over JSON-RPC. The game API itself is the Lua API; RPC only reaches it.
- **Movement**: `move.to` asks `navigation` for a path, which wraps `pathfinding` (Detour on the server's `mmaps/`,
  heights from `maps/` and `vmaps/`). `movement` walks the path and sends the movement packets a real client
  would, at the server's current run or swim speed.
- **Recording**: with `-record`, `session.Recorder` writes the same JSONL format as the server's `mod-recorder`
  (positions, combat, chat, kills, loot, quests, achievements) plus agent-side events: `api` (each RPC call and
  its params) and `script_error`. The viewer (`bin/azview`, `../azview`) reads it.

## Build and run

The root `go.work` includes this module, so build from the repo root:

```sh
go build -o azeroth-sdk/azsdk ./azeroth-sdk/cmd/azsdk
azeroth-sdk/azsdk -config azsdk.json             # several characters from a config file
azeroth-sdk/azsdk -char Garrick -account agent1  # one character, no config file (as the bench runs it)
```

`data_dir` needs the server's `maps/`, `mmaps/`, `vmaps/` and `dbc/` (copied out of AzerothCore's client-data
volume, see the root README). Characters are created on first login if they don't exist (race and class from the
config or flags). Agents talk to `http://<listen>/rpc`.

| Flag | Default | |
|---|---|---|
| `-config FILE` | `azsdk.json` | config file; ignored with `-char` |
| `-char NAME` | | run this one character instead of a config file |
| `-account A` | | account for `-char` |
| `-password P` | the account name | password for `-char` |
| `-race N`, `-class N` | `1`, `1` | used only if the `-char` character has to be created |
| `-script FILE` | | Lua script loaded at login (`-char` only) |
| `-workspace DIR` | the script's directory | directory Lua may read with `dofile`/`loadfile` (`-char` only) |
| `-listen ADDR` | `127.0.0.1:8780` | API address (overrides the config) |
| `-data DIR` | `./data` with `-char` | data dir (overrides the config) |
| `-auth HOST:PORT` | `127.0.0.1:3724` with `-char` | authserver (overrides the config) |
| `-duration D` | `0` | stop after this long, e.g. `8s` for a first login that only creates the character; `0` runs until interrupted |
| `-events DIR` | | keep each character's event stream in `DIR/<char>.events.jsonl`, so the history survives restarts |
| `-record FILE` | | append the recorder event log to this file (overrides the config) |

The config file form (`azsdk.example.json`):

```json
{
  "auth_server": "127.0.0.1:3724",
  "data_dir": "./data",
  "listen": "127.0.0.1:8780",
  "record": "events.jsonl",
  "characters": [
    { "name": "Garrick", "account": "agent1", "password": "agent1", "race": 1, "class": 1 },
    { "name": "Paloma", "account": "agent2", "password": "agent2", "race": 1, "class": 2, "script": "scripts/idle.lua" }
  ]
}
```

Each character may also set `workspace`. `record` is optional (the example file leaves it out). The daemon exits
non-zero when every session has ended on its own (for example a rejected login); on interrupt or `-duration` it
logs the characters out first.

## RPC methods

JSON-RPC 2.0, one request per `POST /rpc`. `GET /methods` lists the method names. `char` may be omitted when the
daemon runs a single character.

| Method | Params | Result |
|---|---|---|
| `sessions.list` | | `[{char, status, error}]` |
| `script.eval` | `char, code` | `{result, events?, script_errors?}`: `events` summarises what happened since the previous eval |
| `api.call` | `char, fn, args` | one Lua API function by name with JSON args (`"move.to"`, `[x, y, z]`) |
| `api.docs` | `char` | every API function's signature and description |
| `events.query` | `char, since?, types?, match?, last?, limit?` | the event history (nothing is consumed; `last` defaults to 50) |
| `events.wait` | `char, since?, types?, match?, timeout_s?` | `{events, timed_out, seq}`: blocks until a matching event after `since` (default: now), up to `timeout_s` (default 60, max 600) |

`types` takes patterns like `"quest_*"`. With `-record`, every method except `sessions.list`, `api.docs` and the
two `events.*` methods is logged as an `api` event.

## Regenerating the API reference

`docs/api-reference.md` is generated by `cmd/apigen` from the function registry of a live session (`api.docs`) and package `types` (by reflection plus its doc comments). Don't
edit them by hand. After changing the Lua API or `types`:

```sh
bin/azbench apidoc                                      # from the repo root: builds everything, restarts the GM dev character on 8112, runs apigen
cd azeroth-sdk && go run ./cmd/apigen -rpc http://127.0.0.1:8090/rpc   # or against any running session
```

## Tests

```sh
cd azeroth-sdk
go test ./script/ ./client/ ./navigation/                       # offline
AC_DATA_DIR=$PWD/../data go test ./pathfinding/ ./movement/   # tests that need the server data skip without it
```

## Other tools

| Command | |
|---|---|
| `go run ./azeroth-sdk/cmd/clientdata -client <WoW dir> extract data` | minimap tiles and road rasters from a 3.3.5a install (also `cat`, `blp`, `textures`) |
| `go run ./azeroth-sdk/cmd/navprobe <data dir> x1 y1 z1 x2 y2 z2 [...]` | navmesh path length and straightness between points on map 0 |

## Packages

| Package | |
|---|---|
| [`client`](client) | protocol: SRP6 login, encryption, packets, world object model, packet hooks |
| [`session`](session) | one logged-in character (`Bot`): login, tick loop, the Lua API, events, recorder |
| [`script`](script) | the Lua runtime: engine, tasks, event bus, action requests |
| [`api`](api) | JSON-RPC handler over a set of sessions |
| [`types`](types) | the API's data model: every value, result and event payload |
| [`metrics`](metrics) | the one definition of how a run is measured (the score's rates, combat totals, cooperation, time use), shared with the judge; pure, no I/O |
| [`navigation`](navigation) | `Navigator` interface over the embedded pathfinder |
| [`pathfinding`](pathfinding) | Detour navmesh paths, terrain and vmap heights, water levels, line of sight |
| [`movement`](movement) | follows a path and sends client-like movement packets |
| [`clientdata`](clientdata) | reads a game install's MPQ archives: minimap BLPs, ADT texture layers |
| [`cmd/azsdk`](cmd/azsdk) | the daemon |
| [`cmd/wow`](cmd/wow) | `wow`: the command line for a session (run Lua, load scripts, read and wait on events) |
| [`jqjson`](jqjson) | JSON read and printed the way `jq -c` does (`wow`'s output) |
| [`cmd/apigen`](cmd/apigen) | generates the API reference |
| [`cmd/clientdata`](cmd/clientdata), [`cmd/navprobe`](cmd/navprobe) | data extraction and navmesh probing |
| [`examples/lua`](examples/lua) | example in-daemon scripts |

## Docs

Agent-facing documentation, in `docs/`:

| File | |
|---|---|
| [`playing.md`](docs/playing.md) | game knowledge: what makes a character strong, how to play well |
| [`guide.md`](docs/guide.md) | practical notes on using the SDK: moving, casting, bags, gear, travel, death |
| [`runtime.md`](docs/runtime.md) | tasks, actions (requests and results), events |
| [`lua-api.md`](docs/lua-api.md) | the Lua API by namespace, and its conventions |
| [`api-reference.md`](docs/api-reference.md) | every function, type and event (generated) |
| [`world-data.md`](docs/world-data.md) | `azworld`: structured lookups and raw SQL (`azworld sql`), the bench's world tool, documented here for agents |

Derived from [AzerothGhost](https://github.com/azerothcore/AzerothGhost) (MIT, see `LICENSE.AzerothGhost`),
with the bot AI removed and the agent API, movement and pathfinding fixes from LEEROY.
