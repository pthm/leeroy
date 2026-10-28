# azeroth-sdk

A headless World of Warcraft 3.3.5a client for building agents on an [AzerothCore](https://github.com/azerothcore/azerothcore-wotlk)
server. `azsdk` keeps characters logged in and serves a JSON-RPC API; agents drive them from TypeScript
(`sdk/ts`) or by loading Lua scripts that run inside the daemon every 200 ms.

The client never plays on its own: no built-in AI, no auto-loot, no auto-combat. Movement follows the server's
navmesh at the speed the server sets, swims in deep water, and stops when rooted.

## Run

Lives in the wowbench monorepo; the root `go.work` includes this module, so build from the repo root:

```sh
go build -o azeroth-sdk/azsdk ./azeroth-sdk/cmd/azsdk
azeroth-sdk/azsdk -config azsdk.json        # see azeroth-sdk/azsdk.example.json
```

`data_dir` needs the server's `maps/`, `mmaps/`, `vmaps/` and `dbc/` (copy them out of AzerothCore's client-data
volume). Characters are created on first login if they don't exist (race/class from the config).

```ts
import { Azeroth } from "./azeroth-sdk/sdk/ts/src/index.ts";
const me = new Azeroth("http://127.0.0.1:8780").character("Garrick");
const s = await me.state();                       // health, power, position, nearby units, bags, quests...
await me.moveTo(s.position.x + 30, s.position.y); // false if there is no walkable path
await me.waitIdle();
await me.eval("function on_tick() ... end");      // in-daemon script for tight loops (combat, travel)
```

`GET /methods` lists the API; `sdk/ts/examples/smoke.ts` exercises it.

## Layout

| Package | |
|---|---|
| `client` | protocol: SRP6 login, encryption, packets, world object model |
| `pathfinding`, `navigation`, `movement` | navmesh paths (Detour), terrain and water from map files, movement packets |
| `session` | one logged-in character: login, tick loop, movement, the agent bindings |
| `script` | the Lua runtime for agent scripts (go-lua 5.2 + Lua patterns) |
| `api`, `cmd/azsdk` | JSON-RPC API and daemon |
| `sdk/ts` | TypeScript client |

Status and plan: phase 0 of the azeroth-sdk design. Methods are thin wrappers over the session's Lua
bindings; verified action results, an event stream (WebSocket) and vendor/trainer/gossip support come next.

Derived from [AzerothGhost](https://github.com/azerothcore/AzerothGhost) (MIT, see `LICENSE.AzerothGhost`),
with the bot AI removed and the agent API, movement and pathfinding fixes from wowbench.
