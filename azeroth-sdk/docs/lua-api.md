# Lua runtime and `bot` API

Agent scripts run inside the session (the `azsdk` daemon, or a bench session) on a 200 ms tick: define
`on_tick()` and it is called until you replace or clear it. Lua globals persist between calls.

There's no bundled AI: you write the targeting, combat rotation, resting, looting and death handling
yourself. With no `on_tick` loaded the character does nothing at all, not even fight back. Nothing moves
or turns for you either: when a melee swing fails, `bot.last_attack_error()` → `{reason, guid, time_ms}`
says why (`NOT_IN_RANGE`: move closer; `BAD_FACING`: `face_target`). Opening loot (`loot_all`, or
`bot.call("Loot", guid)`) takes everything and closes the window.

The Lua is **5.2** (`_VERSION`): numbers are doubles (no 64-bit integers, so keep GUIDs as strings; no
`//`, `&` or `|` operators; use `bit32` for bit operations; `goto` is a reserved word), and `table.unpack` instead of `unpack`. String patterns
work as usual (`find`, `match`, `gmatch`, `gsub`).

## `bot` API

GUIDs are 64-bit and are returned as strings. Pass them back unchanged.

**State:** `get_position()` → x,y,z,o · `get_health()`/`get_power()` → cur,max (rage and runic power are
x10: 0-1000) · `get_xp()` → xp, xp needed for next level · `get_power_type()` (0 mana, 1 rage, 3 energy,
6 runic) · `get_level()` · `get_class()` · `get_faction()` · `get_own_guid()` · `get_stance()` ·
`in_combat()` (the server's combat flag) · `is_alive()` (false while dead or a ghost) · `is_ghost()` · `is_resting()` (in an inn or city) ·
`money()` (copper) · `get_target()` · `now_ms()`

**Spells:** `is_spell_ready(id)` → ready, ms until ready (cooldown, category cooldown and global cooldown) ·
`is_casting()` → spell id, ms left (casts and channels, from the moment you send the cast; nil when idle.
Moving interrupts a cast) · `last_cast_error()` →
`{spell, code, reason, time_ms}` for the latest failed cast (`reason` like `"not_ready"`, `"no_power"`,
`"out_of_range"`, `"line_of_sight"`) · `spell_info(id)` → `{name, rank, cooldown_ms, category_cooldown_ms,
gcd_ms, power_type, cost}`

**Death:** `corpse_position()` → x,y,z, seconds until you can reclaim (nil while alive; right after dying it
asks the server, so call it again a moment later) · `spirit_res(npc_guid)` resurrects at a spirit healer
(with resurrection sickness). Release with `bot.call("RepopRequest")`, then `move_to` the corpse and
`bot.call("ReclaimCorpse")` within ~30 yards. The reclaim delay grows if you die repeatedly.

**Inventory:** `bag_items()` → `{bag, slot, guid, entry, count, name, quality, sell_price, inv_type,
use_spell}` for everything you carry and wear (bag 255 = your own slots: 0-18 equipped, 19-22 bags, 23-38
backpack; bag 19-22 = inside that bag; the item fields appear once the item's info has loaded, a moment after
you first see it) · `has_item(entry)` → count · `free_slots()` → empty bag slots · `item_info(entry)` →
`{name, quality, class, subclass, slot, inv_type, item_level, required_level, sell_price, buy_price, armor, dps,
stats = {strength = 3, ...}, usable, why_not, use_spell, start_quest, max_stack}` (nil the first time: it asks the server; call again a moment later) ·
`compare(item)` → the change if you swapped this item (entry or bag guid) for what you wear in its slot:
`{usable, why_not, slot, item_level, stats, armor, dps, replaces}` (nil until item info has loaded; call again) ·
`broken_gear()` → equipped items at 0 durability (`bag_items` has `durability`, `max_durability`) ·
`use_item(item[, target])` uses food, drink, potions, quest items (`item` = entry or guid) · `destroy(item[, count])`.
In these and the vendor calls, `item` is an item entry or an item guid from `bag_items`.

**Vendors and trainers:** `vendor_items(vendor)` → `{slot, entry, name, price, stock, buy_count}` (nil on
the first call: it asks the vendor) · `buy(vendor, entry[, count])` · `sell(vendor, item[, count])` ·
`sell_junk(vendor)` → stacks sold (all grey items) · `train(trainer, spell)` learns a spell · `repair(npc)` repairs all your gear at an armorer/weaponsmith. Failures
(not enough money, bags full, too far) arrive as system messages in `read_chat`. Stand within ~5 yards of the NPC.

**Quests and objects:** `quest_log()` → `{id, complete, failed, counts}` per quest you have (`counts` =
kill/objective counters; for item objectives use `has_item`) · `turn_in(npc, quest, reward_index)` does
the whole hand-in (it silently does nothing if the quest isn't complete; returns a warning if your bags are
full, which blocks item rewards) · `use_object(guid[, spell])` uses a game object within ~5 yards: doors,
quest objects, and chests or crates (opened with the Opening spell, loot taken automatically).

**Groups:** `invite(name)` · `accept_invite()` / `decline_invite()` (an invite arrives as a system message in
`read_chat`, and `pending_invite()` → inviter name) · `leave_group()` · `group()` → nil when solo, else
`{leader, members = {{name, guid, online}}}` (the others, not you). Failed group commands ("already in a
group") show up in `read_chat`.

**Achievements:** `achievements()` → `{earned = {ids}, criteria = {{id, counter}}}`: what you've earned and
your progress on each achievement criterion (look both up in worlddb's `wowbench` tables, below). An
earned achievement also announces itself in chat (type 48) to players nearby.

**World:** `get_nearby_units(dist)` / `get_nearby_players(dist)` → array of units · `get_unit(guid)`.
Unit fields: `guid, entry, name, level, health, max_health, x, y, z, distance, is_alive, is_player,
faction, npc_flags, flags, dyn_flags, lootable, target` (`target` = guid the unit is targeting).
`get_attackers(dist)` → living units within dist that are targeting you. `name` is often empty, so use `entry` (creature template
id). `npc_flags` tells you what an NPC offers (bitmask: 0x1 gossip, 0x2 quest giver, 0x80 vendor,
0x1000 repair, 0x2000 flight master, 0x10 trainer).

**Act:** `path(x,y,z)` → walking distance, gap (how far the best path ends from the target: over a few yards
means unreachable; nil if no path at all) · `ground_z(x,y[,z])` → the height to stand at x,y, for a `move_to` z. Pass an approximate z (an NPC's
spawn z from `wowdb`) to get the floor nearest it: upstairs rooms, buildings, Stormwind's streets. Without one it
returns the outdoor terrain height, which lies under any building · `move_to(x,y,z)` (follows the navmesh,
so no slopes steeper than ~50°; returns false and doesn't move if there's no walkable path. Moves at your current
server speed: slower while swimming, which happens automatically in deep water, and not at all while rooted or
stunned) · `stop_moving()` · `is_moving()` · `set_target(guid)` ·
`attack(guid)` · `stop_attack()` · `face_target(guid)` · `set_facing(o)` · `cast_spell(id, guid)` ·
`is_spell_ready(id)` · `can_cast(id, guid)` · `has_aura_on(guid, id)` · `is_behind_target(guid)` ·
`loot_all(guid)` (takes all items and money; returns a warning when your bags are full) · `pet_attack(guid)` · `set_sheath(0)` · `consume_teleport()` (true once
after a teleport) · `log(msg)`

**Chat:** `send_chat(msg)` (say) · `whisper(name, msg)` · `party(msg)` · `channel(name, msg)` ·
`join_channel(name, id)` · `chat_pending()` → count of unread messages from other players ·
`read_chat()` drains received messages: `{type, from, guid, channel, text,
time_ms}`. Types: 0 system/server notices (e.g. "Inventory is full"), 1 say, 2 party, 7 whisper, 9 your own
whisper echoed, 17 channel, 0x0C+ NPC speech. You start in your zone's General channel (e.g.
`"General - Elwynn Forest"`). Zone channels need their id (1 General, 2 Trade, 22 LocalDefense) and are
not rejoined automatically when you change zone. Custom channels use id 0 and any name, and work
across zones.

**Status:** `bot.status()` (or `wow status` from the shell) → level, xp, hp, power, money, position and
`where` (zone and area), alive/ghost/combat/resting/casting, free bag slots, broken gear, usable upgrades sitting in
your bags (`bag_upgrades`), quest progress, target, attackers, hostile mobs within 30 yd (`mobs_nearby`: count,
highest level, elites), your last minute of fighting (`last_minute`: dps, damage, healing, taken, kills), group,
nearby players with class, corpse, the last cast error, unread chat and what `act` is doing.
`in_los(guid)` or `in_los(x, y, z)` → true if nothing solid (walls, rock, buildings) is between your eyes and the
target, the way the server checks spells (trees and terrain bumps don't count); nil without collision data ·
`location()` → `{zone, area, zone_id, area_id}` · `reaction(guid)` → "hostile" (attacks on sight), "neutral" or
"friendly" toward you.

**Combat log, meters, inspect** (what a real client sees: fights within ~100 yd, any source and target, party
included): `combat_log([since_ms])` → `{t, kind, src, dst, spell, amount, over, crit}` events (default the last 30s;
`kind`: melee, spell, dot, heal, hot, kill; `src`/`dst` are `{guid, name, entry}`) · `meter([window_s = 60])` → per
unit over the window: `damage, healing, taken, healed, dps, hps, active_s, kills, crit_pct`, sorted by damage, with
`by_spell` for you (compare rotations, see what's hurting you, judge a party) · `inspect(guid)` → a player's `name,
class, race, level, health, gear = {{slot, entry, name, item_level}}, avg_item_level` (item names fill in a moment
after the first call).

**Trade** (with a player within ~10 yards): `trade(name_or_guid)` opens a trade with them; they get a system
message in `read_chat` and open the window with `trade_open()` · `trade_offer{items = {entry or guid, ...}, money =
copper}` sets your side (up to 6 items; soulbound items can't be traded: `bag_items` marks them `soulbound`) ·
`trade_status()` → `{state, with, mine = {money, items}, theirs = {money, items}, they_accepted}` · `trade_accept()`
(the trade happens when both accept; any change un-accepts both) · `trade_cancel()`. Lower level: `trade_item(slot,
item)`, `trade_money(copper)`.

## `act`: built-in utilities

Jobs are tables with `tick(self)` returning `"running"`, `"done"` or `"failed", message`. `act.run{job, ...}` runs
them in order from `on_tick` (failed steps are logged and skipped unless `{on_fail = "stop"}`; pass a function
returning the plan and `{loop = true}` to repeat it); `act.status()` says where it is; `act.stop()` stops. Put your
own logic in steps with `act.fn(function(self) ... return true when done end)`, or call `act.tick()` from your own
`on_tick`. The runner does nothing about combat or death on its own.

| Kind | Calls |
|---|---|
| Movement | `go_to(x, y [, z], {tolerance})` · `travel({{x, y [, z]}, ...})` · `approach(guid, range)` · `follow{name, distance, ["until"] = fn}` |
| NPCs | `accept{npc, quest [, x, y, z]}` · `turn_in{npc, quest [, reward, x, y, z]}` · `vendor{npc [, x, y, z, buy = {{entry, count}}]}` (sells greys, repairs if it can) · `train{npc, spells}` · `talk{npc, fn = function(npc) end}`. `npc` is the creature entry; with x, y, z it walks there first |
| Objects | `use_object{entry [, count, quest, x, y, z, radius]}` (crates, chests, quest objects) |
| Recovery | `rest{hp, mana, max_s}` (eats/drinks if you carry food/water) · `release()` · `corpse_run()` · `spirit_res()` |
| Flow | `seq(jobs)` · `wait(seconds)` · `fn(f)` · `try(job)` |
| Helpers (plain functions) | `cast(id [, target])` (faces, checks ready) · `ready(id [, target])` · `buff(id [, target])` (unless it has the aura) · `best({rank1, rank2, ...})` (highest known) · `knows(id)` · `in_range(id, guid)` · `attackers(dist)` · `npc(entry [, dist])` · `player(name)` · `tapped_by_other(unit)` · `loot_nearby(dist)` · `consume("Food" \| "Drink")` · `make_room()` · `hp()` · `mana()` · `dist(x, y [, z])` |

```lua
act.run{ act.go_to(-9464, 62), act.accept{ npc = 240, quest = 62 },
         act.fn(function() return my_combat_step() end, "kill kobolds"),
         act.vendor{ npc = 2046 }, act.turn_in{ npc = 240, quest = 62 } }
```

**Everything else the client can do:** `bot.call("Method", args...)` calls any method of the underlying
client and returns its results (an `error` comes back as a string, or nil on success). Useful ones:

| Need | Call |
|---|---|
| Talk to a quest giver | `QuestgiverHello(npc)`, `QuestgiverAcceptQuest(npc, questId)` (confirm with `bot.quest_log()`) |
| Find an object (chest, herb, door) | `FindGameObjectByEntry(entry, dist)`, `ListGameObjectEntries(limit)`, `GetObject(guid)` (PosX/PosY/PosZ); use it with `bot.use_object` |
| Death | `RepopRequest()` (release spirit; you respawn as a ghost at a graveyard), `ReclaimCorpse()` |
| Items | `AutoEquipItem(bag, slot)` (bag 255 = backpack, slots 23-38), `EquippedSlot(entry)`, `LootItem(slot)`, `LootMoney()`, `LootRelease(guid)` |
| Spells | `KnowsSpell(id)`, `CancelAura(id)`, `CastSpellAtPosition(id, x, y, z)` |
| Misc | `Money()`, `Position()`, `TargetGUID()`, `SelfAuras()`, `SendTextEmote(id, guid)`, `CreatureQuery(entry, guid)`, `FindUnitByEntry(entry, dist)` (0 if none) |

The full list is the exported methods of `WorldClient` in the client source, which you can read (path in
your task prompt). Read it when you need exact semantics.

**Raw protocol (build it yourself):** the client doesn't implement gossip menus, trainer lists, mail, trade
or the auction house. You can add them with:
- `bot.watch(opcode, true)`: start capturing that server opcode.
- `bot.read_packets()`: drains captured packets as `{opcode, hex, time_ms}`.
- `bot.send_packet(opcode, "hex body")`: send a client packet. The header and encryption are handled for
  you.

Opcode numbers are in the client's `opcodes.go` and the server's `Server/Protocol/Opcodes.h`. Packet layouts
are in the server's `Handlers/*.cpp`, and enums (spell failure codes, flags) in the server's
`shared/SharedDefines.h`. These source trees are readable (paths in your task prompt). For example, a trainer's list: `CMSG_TRAINER_LIST` (0x1B0, body =
trainer guid as u64 LE), reply `SMSG_TRAINER_LIST` (0x1B1). All numbers are little-endian. Write parsers once and keep them in your lib.

**GM commands:** `send_command(".cmd")` works only on GM accounts. Scored runs use non-GM accounts.
