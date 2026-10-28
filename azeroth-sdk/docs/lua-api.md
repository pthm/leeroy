# Lua runtime and `bot` API

Agent scripts run inside the session (the `azsdk` daemon, or a bench session) on a 200 ms tick: define
`on_tick()` and it is called until you replace or clear it. Lua globals persist between calls.

There's no bundled AI: you write the targeting, combat rotation, resting, looting and death handling
yourself. With no `on_tick` loaded the character does nothing at all, not even fight back. Nothing moves
or turns for you either: when a melee swing fails, `bot.last_attack_error()` → `{reason, guid, time_ms}`
says why (`NOT_IN_RANGE`: move closer; `BAD_FACING`: `face_target`). Opening loot (`loot_all`, or
`bot.call("Loot", guid)`) takes everything and closes the window.

The Lua is **5.2** (`_VERSION`): numbers are doubles (no 64-bit integers, so keep GUIDs as strings; no
`//` operator; use `bit32` for bit operations), and `table.unpack` instead of `unpack`. String patterns
work as usual (`find`, `match`, `gmatch`, `gsub`).

## `bot` API

GUIDs are 64-bit and are returned as strings. Pass them back unchanged.

**State:** `get_position()` → x,y,z,o · `get_health()`/`get_power()` → cur,max (rage and runic power are
x10: 0-1000) · `get_xp()` → xp, xp needed for next level · `get_power_type()` (0 mana, 1 rage, 3 energy,
6 runic) · `get_level()` · `get_class()` · `get_faction()` · `get_own_guid()` · `get_stance()` ·
`in_combat()` (the server's combat flag) · `is_alive()` (false while dead or a ghost) · `is_ghost()` ·
`money()` (copper) · `get_target()` · `now_ms()`

**Spells:** `is_spell_ready(id)` → ready, ms until ready (cooldown, category cooldown and global cooldown) ·
`is_casting()` → spell id, ms left (casts and channels; nil when idle) · `last_cast_error()` →
`{spell, code, reason, time_ms}` for the latest failed cast (`reason` like `"not_ready"`, `"no_power"`,
`"out_of_range"`, `"line_of_sight"`) · `spell_info(id)` → `{name, rank, cooldown_ms, category_cooldown_ms,
gcd_ms, power_type, cost}`

**Death:** `corpse_position()` → x,y,z, seconds until you can reclaim (nil while alive; right after dying it
asks the server, so call it again a moment later) · `spirit_res(npc_guid)` resurrects at a spirit healer
(with resurrection sickness). Release with `bot.call("RepopRequest")`, then `move_to` the corpse and
`bot.call("ReclaimCorpse")` within ~30 yards. The reclaim delay grows if you die repeatedly.

**Inventory and quests:** `bag_items()` → `{bag, slot, guid, entry, count}` for everything you carry and
wear (bag 255 = your own slots: 0-18 equipped, 19-22 bags, 23-38 backpack; bag 19-22 = inside that bag) ·
`has_item(entry)` → count · `quest_log()` → `{id, complete, failed, counts}` per quest you have (`counts` =
kill/objective counters; for item objectives use `has_item`) · `turn_in(npc, quest, reward_index)` does
the whole hand-in (it silently does nothing if the quest isn't complete).

**Achievements:** `achievements()` → `{earned = {ids}, criteria = {{id, counter}}}`: what you've earned and
your progress on each achievement criterion (look both up in worlddb's `wowbench` tables, below). An
earned achievement also announces itself in chat (type 48) to players nearby.

**World:** `get_nearby_units(dist)` / `get_nearby_players(dist)` → array of units · `get_unit(guid)`.
Unit fields: `guid, entry, name, level, health, max_health, x, y, z, distance, is_alive, is_player,
faction, npc_flags, flags, dyn_flags, lootable, target` (`target` = guid the unit is targeting).
`get_attackers(dist)` → living units within dist that are targeting you. `name` is often empty, so use `entry` (creature template
id). `npc_flags` tells you what an NPC offers (bitmask: 0x1 gossip, 0x2 quest giver, 0x80 vendor,
0x1000 repair, 0x2000 flight master, 0x10 trainer).

**Act:** `ground_z(x,y)` → terrain height at x,y (for picking a `move_to` z) · `move_to(x,y,z)` (follows the navmesh,
so no slopes steeper than ~50°; returns false and doesn't move if there's no walkable path. Moves at your current
server speed: slower while swimming, which happens automatically in deep water, and not at all while rooted or
stunned) · `stop_moving()` · `is_moving()` · `set_target(guid)` ·
`attack(guid)` · `stop_attack()` · `face_target(guid)` · `set_facing(o)` · `cast_spell(id, guid)` ·
`is_spell_ready(id)` · `can_cast(id, guid)` · `has_aura_on(guid, id)` · `is_behind_target(guid)` ·
`loot_all(guid)` (takes all items and money; check with `has_item`) · `pet_attack(guid)` · `set_sheath(0)` · `consume_teleport()` (true once
after a teleport) · `log(msg)`

**Chat:** `send_chat(msg)` (say) · `whisper(name, msg)` · `party(msg)` · `channel(name, msg)` ·
`join_channel(name, id)` · `chat_pending()` → count of unread messages from other players ·
`read_chat()` drains received messages: `{type, from, guid, channel, text,
time_ms}`. Types: 0 system/server notices (e.g. "Inventory is full"), 1 say, 2 party, 7 whisper, 9 your own
whisper echoed, 17 channel, 0x0C+ NPC speech. You start in your zone's General channel (e.g.
`"General - Elwynn Forest"`). Zone channels need their id (1 General, 2 Trade, 22 LocalDefense) and are
not rejoined automatically when you change zone. Custom channels use id 0 and any name, and work
across zones.

**Everything else the client can do:** `bot.call("Method", args...)` calls any method of the underlying
client and returns its results (an `error` comes back as a string, or nil on success). Useful ones:

| Need | Call |
|---|---|
| Talk to a quest giver | `QuestgiverHello(npc)`, `QuestgiverAcceptQuest(npc, questId)` (confirm with `bot.quest_log()`) |
| Use an object (chest, herb, door) | `FindGameObjectByEntry(entry, dist)`, `ListGameObjectEntries(limit)`, `GameObjectUse(guid)` |
| Death | `RepopRequest()` (release spirit; you respawn as a ghost at a graveyard), `ReclaimCorpse()` |
| Items | `AutoEquipItem(bag, slot)` (bag 255 = backpack, slots 23-38), `EquippedSlot(entry)`, `LootItem(slot)`, `LootMoney()`, `LootRelease(guid)` |
| Group | `GroupInvite(name)`, `GroupAccept()`, `GroupLeave()`, `GroupMembers()` |
| Spells | `KnowsSpell(id)`, `CancelAura(id)`, `CastSpellAtPosition(id, x, y, z)` |
| Misc | `Money()`, `Position()`, `TargetGUID()`, `SelfAuras()`, `SendTextEmote(id, guid)`, `CreatureQuery(entry, guid)`, `FindUnitByEntry(entry, dist)` (0 if none) |

The full list is the exported methods of `WorldClient` in the client source, which you can read (path in
your task prompt). Read it when you need exact semantics.

**Raw protocol (build it yourself):** the client doesn't implement vendors, gossip menus or trainers.
You can add them with:
- `bot.watch(opcode, true)`: start capturing that server opcode.
- `bot.read_packets()`: drains captured packets as `{opcode, hex, time_ms}`.
- `bot.send_packet(opcode, "hex body")`: send a client packet. The header and encryption are handled for
  you.

Opcode numbers are in the client's `opcodes.go` and the server's `Server/Protocol/Opcodes.h`. Packet layouts
are in the server's `Handlers/*.cpp`, and enums (spell failure codes, flags) in the server's
`shared/SharedDefines.h`. These source trees are readable (paths in your task prompt). For example, a vendor: `CMSG_LIST_INVENTORY` (0x19E, body =
vendor guid as u64 LE), reply `SMSG_LIST_INVENTORY` (0x19F), buy with `CMSG_BUY_ITEM` (0x1A2). All
numbers are little-endian. Write parsers once and keep them in your lib.

**GM commands:** `send_command(".cmd")` works only on GM accounts. Scored runs use non-GM accounts.
