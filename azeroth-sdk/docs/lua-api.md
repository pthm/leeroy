# The Lua API

Your scripts and your `wow` calls use one API, organised by domain. `help()` lists every function from inside the
game (`wow 'return help("items")'`); the API reference below has every function, every type and every event.

| Namespace | What it's for |
|---|---|
| `me` | your character: level, xp, health, power, position, location, money, stats, state, target, `me.status()` |
| `units` | creatures and players around you: `units.nearby`, `units.players`, `units.get`, `units.attackers`, auras, line of sight, inspect |
| `objects` | game objects: chests, gathering nodes (ore, herbs), doors, quest objects (`objects.nearby`, `objects.find`, `objects.use`) |
| `world` | terrain: heights to stand at, walking paths, line of sight to a point |
| `move` | walking (`move.to`, `move.stop`) and facing (`move.face`, `move.turn`) |
| `combat` | targeting and auto-attack, your pet, the combat log and damage/healing meters |
| `spells` | casting (`spells.cast`, `spells.cast_at`), cooldowns, what a spell does |
| `items` | your bags and gear: list, info, compare, use, equip, destroy, loot |
| `vendor`, `trainer` | buying, selling, repairing; learning spells |
| `quests` | your quest log with objectives; accepting and turning in |
| `death` | your corpse, releasing, reclaiming, spirit healers |
| `chat`, `group`, `trade` | other players |
| `talents` | your talent trees and spending points |
| `task`, `events`, `wait`, `await`, `ask` | the runtime (previous section) |
| `act` | built-in utilities that wait for you: walking with retries, NPC errands, resting, corpse runs |
| `client` | the raw protocol, for what the API doesn't cover (outside the stable API) |

## Conventions

- **Queries** are nouns and return one value or one table: `me.level()`, `me.health()` → `{current, max}`,
  `units.nearby(40)` → a list of creatures, nearest first. A `?` on the result type (`me.casting() → Casting?`)
  means it's nil when there's nothing: out of sight, none, or not loaded yet. Names of creatures, items and quests
  fill in a moment after you first see them (the client asks the server, like the real one): call again.
- **Actions** are verbs and return a request: `{id, op, pending = true}` while it's in flight, then the result
  `{id, op, ok, data}` or `{ok = false, error = {code, message}}`. `op` is the function's name (`"spells.cast"`).
  In a task, `await(spells.cast(133, target))` waits for the result; anywhere, `result(id)` reads it. Error codes
  are stable strings (`out_of_range`, `not_known`, `bags_full`, `stuck`, `timeout`...). Every async result, and
  every failure, is also a `result` event.
- **One shape per thing.** A creature, a player, an item, a quest or a spell is the same table wherever it shows
  up: in `units.nearby`, in an event, in `me.status()`, in a result's data. The types are in the reference.
- **GUIDs are strings** (64-bit numbers don't fit in a Lua number). Pass them back unchanged. Functions that take
  an `item` accept an item entry (number) or the GUID of one you carry; `npc` and `guid` are GUIDs, `entry` is a
  template id.
- **Tables as options:** `act` functions and a few others take one table: `act.go_to{x = 1, y = 2}`.

There's no bundled AI: the targeting, fighting, resting, looting and death handling are yours. With no task or
`on_tick` running, the character does nothing at all, not even fight back. Nothing moves or turns for you either:
a refused swing is an `attack_failed` event (`not_in_range`: move closer; `bad_facing`: `move.face`).

The Lua is **5.1** (gopher-lua) with the 5.2 pieces scripts usually want: `goto`, `bit32`, `table.unpack` and
`load(string)`. Numbers are doubles (no `//`, `&` or `|`: use `bit32.band` and friends). String patterns work as
usual.

## The raw protocol

The client doesn't implement mail or the auction house. You can add them:
`client.watch(opcode)` captures a server opcode, `client.packets()` drains the captured packets as
`{opcode, hex, time_ms}`, `client.send(opcode, "hex body")` sends a client packet (header and encryption are
handled), and `client.call("Method", ...)` calls any exported method of the underlying `WorldClient`
(`client/world.go`). Opcode numbers are in the client's `opcodes.go` and the server's `Server/Protocol/Opcodes.h`;
packet layouts in the server's `Handlers/*.cpp`; enums in `shared/SharedDefines.h` (paths in your task prompt).
For example the mailbox: `CMSG_GET_MAIL_LIST` (0x23A, body = mailbox guid as u64 LE), reply
`SMSG_MAIL_LIST_RESULT` (0x23B). All numbers are little-endian. Write parsers once and keep them in your lib.

**GM commands:** `chat.command(".cmd")` only works on GM accounts. Scored runs use non-GM accounts.

