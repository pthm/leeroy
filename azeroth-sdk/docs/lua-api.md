# Lua runtime and `bot` API

Agent scripts run inside the session (the `azsdk` daemon, or a bench session): tasks (`task.spawn`, see "The
runtime" above) and, for simple loops, `on_tick()`, called every 200 ms until you replace or clear it. Lua globals
persist between calls.

There's no bundled AI: you write the targeting, combat rotation, resting, looting and death handling
yourself. With no task or `on_tick` running the character does nothing at all, not even fight back. Nothing moves
or turns for you either: when a melee swing fails, `bot.last_attack_error()` → `{reason, guid, time_ms}`
says why (`NOT_IN_RANGE`: move closer; `BAD_FACING`: `face_target`). Opening loot (`loot_all`, or
`bot.call("Loot", guid)`) takes everything and closes the window.

The Lua is **5.1** (gopher-lua) with the 5.2 pieces scripts usually want: `goto`, `bit32`, `table.unpack` and
`load(string)`. Numbers are doubles (no 64-bit integers, so keep GUIDs as strings; no `//`, `&` or `|` operators: use
`bit32.band` etc.). String patterns work as usual (`find`, `match`, `gmatch`, `gsub`).

## `bot` API

GUIDs are 64-bit and are returned as strings. Pass them back unchanged. Calls marked **→ request** return a request
that resolves to `{ok, data}` or `{ok = false, error = {code, message}}`: `await` it in a task, or check
`bot.result(id)` later; the codes are in the runtime section's table.

**State:** `get_name()` → your character's name · `get_position()` → x,y,z,o · `get_health()`/`get_power()` → cur,max (rage and runic power are
x10: 0-1000) · `get_xp()` → xp, xp needed for next level · `get_power_type()` (0 mana, 1 rage, 3 energy,
6 runic) · `get_level()` · `get_class()` · `get_faction()` · `get_own_guid()` · `get_stance()` ·
`in_combat()` (the server's combat flag) · `is_alive()` (false while dead or a ghost) · `is_ghost()` · `is_resting()` (in an inn or city) ·
`money()` (copper) · `get_target()` · `now_ms()`

**Spells:** `is_spell_ready(id)` → ready, ms until ready (cooldown, category cooldown and global cooldown) ·
`is_casting()` → spell id, ms left (casts and channels, from the moment you send the cast; nil when idle.
Moving interrupts a cast) · `last_cast_error()` →
`{spell, code, reason, time_ms}` for the latest failed cast (the same reason is the `cast` request's error code
and a `cast_failed` event) · `spell_info(id)` → `{name, rank, cooldown_ms, category_cooldown_ms,
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
`use_item(item[, target])` **→ request**: food, drink, potions, quest items (`item` = entry or guid) ·
`destroy(item[, count])` **→ request**.
In these and the vendor calls, `item` is an item entry or an item guid from `bag_items`.

**Vendors and trainers:** `vendor_items(vendor)` → `{slot, entry, name, price, stock, buy_count}` (nil on
the first call: it asks the vendor) · `buy(vendor, entry[, count])` **→ request** · `sell(vendor, item[, count])`
**→ request** · `sell_junk(vendor)` → stacks sold (all grey items) · `train(trainer, spell)` **→ request** ·
`repair(npc)` **→ request**: repairs all your gear at an armorer/weaponsmith. Stand within ~5 yards of the NPC.

**Quests and objects:** `quest_log()` → `{id, complete, failed, counts}` per quest you have (`counts` =
kill/objective counters; for item objectives use `has_item`) · `turn_in(npc, quest, reward_index)` **→ request**:
the whole hand-in (the server says nothing when the quest isn't complete, so that's a `timeout`; full bags block
item rewards: `bags_full`) · `use_object(guid[, spell])` **→ request**: uses a game object within ~5 yards: doors,
quest objects, and chests or crates (opened with the Opening spell, loot taken automatically).

**Groups:** `invite(name)` · `accept_invite()` / `decline_invite()` (an invite is a `party_invite` event, and
`pending_invite()` → inviter name) · `leave_group()` · `group()` → nil when solo, else
`{leader, members = {{name, guid, online}}}` (the others, not you). Failed group commands ("already in a
group") are `error` events with `op = "party"`.

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
returns the outdoor terrain height, which lies under any building · `move_to(x,y,z)` **→ request** (follows the
navmesh, so no slopes steeper than ~50°; with no walkable path it walks a straight line and the request says
`no_path`. Moves at your current server speed: slower while swimming, which happens automatically in deep water,
and not at all while rooted or stunned) · `stop_moving()` · `is_moving()` · `set_target(guid)` ·
`attack(guid)` (true if sent; refused swings are `attack_failed` events) · `stop_attack()` · `face_target(guid)` ·
`set_facing(o)` · `cast_spell(id, guid)` **→ request** ·
`is_spell_ready(id)` · `can_cast(id, guid)` · `has_aura_on(guid, id)` · `is_behind_target(guid)` ·
`loot_all(guid)` **→ request** (takes all items and money; the result lists them) · `pet_attack(guid)` ·
`set_sheath(0)` · `consume_teleport()` (true once after a teleport) · `log(msg)` (a `log` event, tagged with the task)

**Chat** (the guide's "Chat" section explains each kind): `send_chat(msg)` (say) · `yell(msg)` · `whisper(name, msg)` · `party(msg)` · `channel(name, msg)` ·
`join_channel(name, id)`. Received messages are `chat` events (`wow events --types chat` for the history,
`events.on("chat", fn)` in scripts). The older inbox still works: `read_chat()` drains received messages as
`{type, from, guid, channel, text, time_ms, ago_s}` and `chat_pending()` counts unread ones. Types: 0 system/server notices (e.g. "Inventory is full", invites, trades), 1 say, 2 party, 6 yell, 7 whisper,
9 your own whisper echoed, 17 channel, 0x0C+ NPC speech. You start in your zone's General channel (e.g.
`"General - Elwynn Forest"`). Zone channels need their id (1 General, 2 Trade, 22 LocalDefense) and are
not rejoined automatically when you change zone. Custom channels use id 0 and any name, and work
across zones.

**Status:** `bot.status()` (or `wow status` from the shell) → level, xp, hp, power, money, position and
`where` (zone and area), alive/ghost/combat/resting/casting, free bag slots, broken gear, usable upgrades sitting in
your bags (`bag_upgrades`), quest progress, target, attackers, hostile mobs within 30 yd (`mobs_nearby`: count,
highest level, elites), your last minute of fighting (`last_minute`: dps, damage, healing, taken, kills), group,
nearby players with class, corpse, the last cast error, and your tasks.
`in_los(guid)` or `in_los(x, y, z)` → true if nothing solid (walls, rock, buildings) is between your eyes and the
target, the way the server checks spells (trees and terrain bumps don't count); nil without collision data ·
`location()` → `{zone, area, zone_id, area_id}` · `reaction(guid)` → "hostile" (attacks on sight), "neutral" or
"friendly" toward you.

**Talents** (from level 10, one point per level): `talents()` → `{points, trees = {{name, spent, talents = {{id,
name, rank, max_rank, row, requires, available, spell}}}}}` (row r needs 5 × r points in that tree) ·
`learn_talent(id)` spends a point on the next rank. `wowdb spell <spell>` describes what a talent does.

**Combat log, meters, inspect** (what a real client sees: fights within ~100 yd, any source and target, party
included): `combat_log([since_ms])` → `{t, kind, src, dst, spell, amount, over, crit}` events (default the last 30s;
`kind`: melee, spell, dot, heal, hot, kill; `src`/`dst` are `{guid, name, entry}`) · `meter([window_s = 60])` → per
unit over the window: `damage, healing, taken, healed, dps, hps, active_s, kills, crit_pct`, sorted by damage, with
`by_spell` for you (compare rotations, see what's hurting you, judge a party) · `inspect(guid)` → a player's `name,
class, race, level, health, gear = {{slot, entry, name, item_level}}, avg_item_level` (item names fill in a moment
after the first call).

**Trade** (with a player within ~10 yards): `trade(name_or_guid)` opens a trade with them; they get a system
`notice` event and open the window with `trade_open()` · `trade_offer{items = {entry or guid, ...}, money =
copper}` sets your side (up to 6 items; soulbound items can't be traded: `bag_items` marks them `soulbound`) ·
`trade_status()` → `{state, with, mine = {money, items}, theirs = {money, items}, they_accepted}` · `trade_accept()`
(the trade happens when both accept; any change un-accepts both) · `trade_cancel()`. Lower level: `trade_item(slot,
item)`, `trade_money(copper)`.

## `act`: built-in utilities

Plumbing, not strategy: there is no combat, targeting or rotation here. The calls in the first four rows wait until
they're done, so run them in a task. They return a result like a request's, `{ok, data}` or
`{ok = false, error = {code, message}}`; `act.must(r)` raises the error instead (ending the task with it).

| Kind | Calls |
|---|---|
| Movement | `go_to(x, y [, z], {tolerance, tries})` (retries after getting stuck) · `travel({{x, y [, z]}, ...})` · `approach(guid, range [, timeout_s])` · `follow{name, distance, ["until"] = fn}` |
| NPCs | `reach_npc{npc [, x, y, z]}` → npc unit · `accept{npc, quest [, x, y, z]}` · `turn_in{npc, quest [, reward, x, y, z]}` · `vendor{npc [, x, y, z, buy = {{entry, count}}]}` (sells greys, repairs if it can, buys: `data = {sold, repaired, bought}`) · `train{npc, spells}` (`data = {learned, failed}`) · `talk{npc, fn = function(npc) end}`. `npc` is the creature entry; with x, y, z it walks there first |
| Objects | `use_object{entry [, count, quest, x, y, z, radius]}` (crates, chests, quest objects) · `loot_nearby(dist)` → items looted |
| Recovery | `rest{hp, mana, max_s}` (eats/drinks if you carry food/water; fails with `attacked`) · `release()` · `corpse_run()` · `spirit_res()` |
| Helpers (don't wait) | `cast(id [, target])` (faces, checks ready; in a task it waits for the result) · `ready(id [, target])` · `buff(id [, target])` (unless it has the aura) · `best({rank1, rank2, ...})` (highest known) · `knows(id)` · `in_range(id, guid)` · `attackers(dist)` · `npc(entry [, dist])` · `player(name)` · `tapped_by_other(unit)` · `consume("Food" \| "Drink")` · `make_room()` · `hp()` · `mana()` · `dist(x, y [, z])` · `ok(data)` / `fail(code, msg)` / `must(r)` |

```lua
task.spawn("quest 62", function()
  act.must(act.accept{ npc = 240, quest = 62, x = -9464, y = 62, z = 56 })
  while not quest_done(62) do my_fight_step() end          -- yours
  local r = act.turn_in{ npc = 240, quest = 62 }
  if not r.ok then bot.log("turn in failed: " .. r.error.code) end
end)
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
