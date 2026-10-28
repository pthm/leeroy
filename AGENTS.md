# Playing WoW as an agent

You control one World of Warcraft (3.3.5a) character on a local AzerothCore server. The character stays
logged in for your whole run, in a **session**. You can query and act on it live, and you load **Lua
scripts** into it that keep playing while you think. Your score is how long it takes you to reach the goal
you were given (e.g. `level:10`, or owning a rare item), so build up scripts that get you there and reuse
them.

## Session

```sh
WOW_PORT=8090 ACCOUNT=agent1 ./play.sh Agentone > sessions/agent1.log 2>&1 &   # start (creates the char if needed)
./wow 'return bot.get_level(), bot.get_health()'       # query -> {"result":[3,72,72]}
./wow 'return bot.get_nearby_units(40)'                # what's around you
./wow -f agents/agent1/grind.lua                       # load/replace a script; its on_tick() starts running
./wow 'on_tick = nil'                                  # stop the running script
tail -f sessions/agent1.log                            # bot.log() output, script errors, kills, level-ups
```

- `./wow` runs Lua in the session and prints the returned values as JSON (`{"error": ...}` on failure).
  It shares a lock with `on_tick`, so don't block in it. Return data rather than printing it.
- A loaded script stays active until you replace it, and Lua globals persist across calls. Keep shared
  state in globals if you want to inspect it later (`./wow 'return my_state'`).
- To create the character as a specific race/class, pass `--race N --class N` to `play.sh`, e.g.
  `./play.sh Agentone --race 2 --class 3` (race: 1 human, 2 orc, 3 dwarf, 4 night elf, 5 undead,
  6 tauren, 7 gnome, 8 troll, 10 blood elf, 11 draenei; class: 1 warrior, 2 paladin, 3 hunter, 4 rogue,
  5 priest, 7 shaman, 8 mage, 9 warlock, 11 druid).
- Paths in `dofile()` are relative to `ghost/`: use `dofile("../agents/agent1/lib/nav.lua")` for your
  own libraries and `dofile("scripts/lib/targeting.lua")` for the bundled ones.

## Your script library

Keep your scripts in `agents/<you>/`. They persist between tasks, so factor out reusable pieces into
`agents/<you>/lib/` (navigation, combat rotation, vendor handling, a quest helper, packet parsers...), with
a top-level script per activity. A script defines `on_tick()`, which is called every 200ms. Calls like
`move_to` don't block: they start an action, and you re-check the world on the next tick.

```lua
local targeting = dofile("scripts/lib/targeting.lua")
function on_tick()
  if bot.in_combat() then return end
  local mob = targeting.find_best_hostile({ max_dist = 80 })
  if mob then bot.move_to(mob.x, mob.y, mob.z) end
end
```

`agents/example.lua` is a complete fight-loot-repeat loop.

## `bot` API

GUIDs are 64-bit and are returned as strings. Pass them back unchanged.

**State:** `get_position()` → x,y,z,o · `get_health()`/`get_power()` → cur,max · `get_power_type()`
(0 mana, 1 rage, 3 energy, 6 runic) · `get_level()` · `get_class()` · `get_faction()` · `get_own_guid()` ·
`get_stance()` · `in_combat()` · `is_alive()` · `money()` (copper) · `get_target()` · `now_ms()`

**World:** `get_nearby_units(dist)` / `get_nearby_players(dist)` → array of units · `get_unit(guid)`.
Unit fields: `guid, entry, name, level, health, max_health, x, y, z, distance, is_alive, is_player,
faction, npc_flags, flags, dyn_flags, lootable`. `name` is often empty, so use `entry` (creature template
id). `npc_flags` tells you what an NPC offers (bitmask: 0x1 gossip, 0x2 quest giver, 0x80 vendor,
0x1000 repair, 0x2000 flight master, 0x10 trainer).

**Act:** `move_to(x,y,z)` (pathfinds) · `stop_moving()` · `is_moving()` · `set_target(guid)` ·
`attack(guid)` · `stop_attack()` · `face_target(guid)` · `set_facing(o)` · `cast_spell(id, guid)` ·
`is_spell_ready(id)` · `can_cast(id, guid)` · `has_aura_on(guid, id)` · `is_behind_target(guid)` ·
`loot(guid)` / `loot_all(guid)` · `pet_attack(guid)` · `set_sheath(0)` · `consume_teleport()` (true once
after a teleport) · `log(msg)`

**Chat:** `send_chat(msg)` (say) · `whisper(name, msg)` · `party(msg)` · `read_chat()` drains received
messages: `{type, from, guid, channel, text, time_ms}`. Types: 0 system/server notices (e.g. "Inventory is
full"), 1 say, 2 party, 7 whisper, 9 your own whisper echoed, 0x0C+ NPC speech. Other players may be
agents too.

**Everything else the client can do:** `bot.call("Method", args...)` calls any method of the underlying
client and returns its results (an `error` comes back as a string, or nil on success). Useful ones:

| Need | Call |
|---|---|
| Talk to a quest giver | `QuestgiverHello(npc)`, `QuestgiverAcceptQuest(npc, questId)`, `QuestgiverCompleteQuest(npc, questId)`, `QuestgiverChooseReward(npc, questId, rewardIdx)` |
| Use an object (chest, herb, door) | `FindGameObjectByEntry(entry, dist)`, `ListGameObjectEntries(limit)`, `GameObjectUse(guid)` |
| Death | `RepopRequest()` (release spirit), walk to your corpse, `ReclaimCorpse()` |
| Items | `AutoEquipItem(bag, slot)` (bag 255 = backpack, slots 23-38), `EquippedSlot(entry)`, `LootItem(slot)`, `LootMoney()`, `LootRelease(guid)` |
| Group | `GroupInvite(name)`, `GroupAccept()`, `GroupLeave()`, `GroupMembers()` |
| Spells | `KnowsSpell(id)`, `CancelAura(id)`, `CastSpellAtPosition(id, x, y, z)` |
| Misc | `Money()`, `Position()`, `TargetGUID()`, `SelfAuras()`, `SendTextEmote(id, guid)`, `CreatureQuery(entry, guid)` |

The full list is the exported methods of `WorldClient` in `ghost/client/*.go`. Read the source when you
need exact semantics.

**Raw protocol (build it yourself):** the client doesn't implement vendors, gossip menus, trainers, the
quest log or bag contents. You can add them with:
- `bot.watch(opcode, true)`: start capturing that server opcode.
- `bot.read_packets()`: drains captured packets as `{opcode, hex, time_ms}`.
- `bot.send_packet(opcode, "hex body")`: send a client packet. The header and encryption are handled for
  you.

Opcode numbers are in `ghost/client/opcodes.go` and in the server source
(`server/src/server/game/Server/Protocol/Opcodes.h`). Packet layouts are in the server's handlers
(`server/src/server/game/Handlers/*.cpp`). For example, a vendor: `CMSG_LIST_INVENTORY` (0x19E, body =
vendor guid as u64 LE), reply `SMSG_LIST_INVENTORY` (0x19F), buy with `CMSG_BUY_ITEM` (0x1A2). All
numbers are little-endian. Write parsers once and keep them in your lib.

**GM commands:** `send_command(".cmd")` works only on GM accounts. Scored runs use non-GM accounts.

## Bundled libraries (`ghost/scripts/`)

- `lib/targeting.lua`: `find_best_hostile({max_dist=})` skips guards, critters and quest NPCs.
- `lib/movement.lua` (chase, wander), `lib/combat.lua` (caster rotation), `lib/util.lua` (guid helpers,
  hp%).
- `ai/init.lua` with `ai/class/*`: a complete grinding AI with class rotations; `scripts/grind.lua` shows
  how to use it. It's a strong baseline you're free to load or borrow from.
