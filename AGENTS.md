# Playing WoW as an agent

You control one World of Warcraft (3.3.5a) character on a local AzerothCore server. The character stays
logged in for your whole run, in a **session**. You can query and act on it live, and you load **Lua
scripts** into it that keep playing while you think. Your score is how long it takes you to reach the goal
you were given (e.g. `level:10`, or owning a rare item), so build up scripts that get you there and reuse
them.

## Your workspace and session

Your working directory is your **script library**. It persists between tasks (anything from previous runs
is still there), and it's the only place you can write. `session.log` in it is the live log of your
character: `bot.log()` output, script errors, kills, level-ups. `example.lua` is a complete
fight-loot-repeat loop to start from.

```sh
wow 'return bot.get_level(), bot.get_health()'   # query -> {"result":[3,72,72]}
wow 'return bot.get_nearby_units(40)'            # what's around you
wow -f grind.lua                                 # load/replace a script; its on_tick() starts running
wow 'on_tick = nil'                              # stop the running script
wow -w 60 'return bot.get_level(), bot.get_xp()' # wait 60s (max 600), then query: use this to poll
wow -c 60 'return bot.read_chat()'               # same, but returns as soon as another player messages you
tail -20 session.log
```

- `wow` runs Lua in the session and prints the returned values as JSON (`{"error": ...}` on failure).
  It shares a lock with `on_tick`, so don't block in it. Return data rather than printing it. If your
  running script raised errors since the last call, the reply also has
  `"script_errors": {"count": n, "last": "..."}`.
- `-w`/`-c` take whole seconds. Create and edit files with your Edit/Write tools: shell redirects,
  heredocs, `sed -i` and `python` are blocked.
- A loaded script stays active until you replace it, and Lua globals persist across calls. Keep shared
  state in globals if you want to inspect it later (`wow 'return my_state'`).
- Inside Lua, the global `LIB` is the absolute path of your workspace (ending in `/`). Load your own
  modules with `dofile(LIB .. "lib/combat.lua")`.

Factor reusable pieces into `lib/` (combat rotation, death handling, navigation, quest and vendor helpers,
packet parsers...), with a top-level script per activity. A script defines `on_tick()`, which is called
every 200ms. Calls like `move_to` don't block: they start an action, and you re-check the world on the next
tick.

```lua
local combat = dofile(LIB .. "lib/combat.lua")
function on_tick()
  if bot.in_combat() then return combat.tick() end
  local mob = combat.pick_target(80)
  if mob then bot.move_to(mob.x, mob.y, mob.z) end
end
```

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

**Act:** `ground_z(x,y)` → terrain height at x,y (for picking a `move_to` z) · `move_to(x,y,z)` (pathfinds on the navmesh; returns false if there's no path, and then walks a
straight line) · `stop_moving()` · `is_moving()` · `set_target(guid)` ·
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

## World database: `worlddb`

`worlddb "SELECT ..."` runs read-only SQL against the game's static world data (MySQL; TSV output, max
300 rows). Use it to plan: where to go, what to kill, who gives which quest, what drops an item.

| Table | Useful columns |
|---|---|
| `creature` (spawns) | `guid, id` (= template entry), `map, position_x, position_y, position_z` |
| `creature_template` | `entry, name, subname, minlevel, maxlevel, faction, npcflag, rank` |
| `creature_loot_template` | `Entry` (= creature entry), `Item, Chance` |
| `item_template` | `entry, name, Quality` (0 grey … 3 rare, 4 epic), `ItemLevel, RequiredLevel, InventoryType, SellPrice` |
| `quest_template` | `ID, LogTitle, QuestLevel, MinLevel, AllowableRaces, ObjectiveText1, RequiredNpcOrGo1, RequiredNpcOrGoCount1, RequiredItemId1, RequiredItemCount1, RewardItem1` |
| `creature_queststarter` / `creature_questender` | `id` (creature entry), `quest` |
| `npc_vendor` | `entry` (vendor creature), `item` |
| `gameobject`, `gameobject_template`, `gameobject_loot_template` | spawns, names and loot of chests, herbs, ore |

The achievement catalogue and area locations are in the `wowbench` schema (qualify the table names):

| Table | Useful columns |
|---|---|
| `wowbench.achievement` | `id, name, description, points, category, faction` (-1 any, 1 Alliance), `flags` (0x1 = statistic, not earnable), `count`/`ref_achievement` (meta achievements) |
| `wowbench.achievement_criteria` | `id, achievement, type, asset, quantity, description`: all of an achievement's criteria are needed unless `achievement.count` says how many |
| `wowbench.criteria_type` | `id, name` for `achievement_criteria.type` (`explore_area`, `complete_quest_count`, `kill_creature`, `do_emote`, `learn_skill_level`...); `asset` means a creature, quest, spell, item, map-overlay... id depending on the type |
| `wowbench.achievement_category` | `id, parent, name` |
| `wowbench.area` | `id, map, zone` (parent zone id, 0 = a zone itself), `level, name` |
| `wowbench.map_overlay` | `id` (= `asset` of `explore_area` criteria), `area1..area4` (the area ids that reveal it) |
| `wowbench.area_location` | `area, map, x, y` (a point inside the area), `min_x, max_x, min_y, max_y` |

You get `explore_area` credit by entering the area: `bot.move_to(x, y, bot.ground_z(x, y))`.

```sh
# level 3-5 mobs near Northshire (map 0 = Eastern Kingdoms), by spawn count
worlddb "SELECT t.entry, t.name, t.minlevel, t.maxlevel, COUNT(*) n, ROUND(AVG(c.position_x)) x, ROUND(AVG(c.position_y)) y
  FROM creature c JOIN creature_template t ON t.entry = c.id
  WHERE c.map = 0 AND t.maxlevel BETWEEN 3 AND 5 AND t.npcflag = 0
    AND c.position_x BETWEEN -9500 AND -8500 AND c.position_y BETWEEN -800 AND 200
  GROUP BY t.entry ORDER BY n DESC LIMIT 10"
```

Coordinates match `bot.get_position()`. `rank` is a MySQL reserved word: write it as `` t.`rank` ``. Monster kill XP drops sharply for mobs well below your level.
