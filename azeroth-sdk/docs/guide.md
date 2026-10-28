# Playing well with the SDK

Practical notes on what works, what costs time, and the mechanics that aren't obvious from the API. The full
`bot` reference follows this section.

## Lua gotchas

- A function's multiple returns are cut to the first value when it isn't last in a list:
  `return bot.get_xp(), bot.get_level()` loses the xp needed. Wrap them: `return {bot.get_xp()}, bot.get_level()`.
- Lua 5.2: `goto` is a reserved word (don't name a function `goto`); there's no `&`/`|`, use
  `bit32.band(u.npc_flags, 0x80) ~= 0`.
- GUIDs are strings. Compare and pass them as they are.
- `wow` and `on_tick` share one lock: never loop or wait inside either. Keep state in globals and check it
  on the next tick.

## Moving and targets

- `move_to` returns false (and doesn't move) when there's no walkable path. `bot.path(x, y, z)` returns the
  walking distance and the gap between where the path ends and your target: a gap over a few yards means you
  can't get there. Check it before you commit to a far target.
- Some mobs are unreachable: on a ledge, inside a closed building, in a mine level below you. If the distance to
  a target hasn't dropped for ~5 seconds, give up on it and remember its guid so you don't pick it again. In
  mines and caves, prefer targets with a small height difference (`|mob.z - my_z| < 10`).
- Indoors and upstairs (inn trainers, anything in Stormwind), pass the target's z to get the right floor:
  `bot.move_to(x, y, bot.ground_z(x, y, z))`.
- `last_cast_error().reason == "line_of_sight"`: a wall or a rise is in the way; move closer or around.

## Casting

- `is_casting()` is true from the moment you call `cast_spell` (or `use_item`) until the cast ends. Moving
  interrupts a cast, so skip movement and recasting while it's true.
- Before a cast, `can_cast(id, guid)` checks cooldowns and power; after it, `last_cast_error()` says why it
  failed (`out_of_range`, `not_ready`, `no_power`, `bad_targets`...).

## Bags, vendors, trainers

- You start with a 16-slot backpack. When it's full, loot items stay on the corpse and quest turn-ins with an
  item reward are refused ("Inventory is full" in `read_chat`; `loot_all`/`turn_in` also return a warning).
  Keep an eye on `bot.free_slots()`.
- `bag_items()` gives each item's name, quality and vendor price once its info has loaded (a second after it
  first shows up). Grey items (quality 0) exist only to be sold.
- At a vendor (`wowdb near x y --kind vendor`), `bot.sell_junk(vendor)` sells all grey items;
  `bot.sell(vendor, item)` sells anything else with a price. `bot.destroy(item)` frees a slot anywhere.
  Selling also earns money (wealth is part of most scores).
- Buy with `bot.vendor_items(vendor)` (call it twice: the first call asks the vendor) then
  `bot.buy(vendor, entry, count)`. Bags (more slots) are sold by general goods and bag vendors.
- New class spells come from your class trainer every couple of levels: `wowdb npc <trainer>` lists them with
  level and cost, `bot.train(trainer, spell)` learns one, and `read_chat` reports the result. The level-70
  "<Class> Trainer" NPCs in Northshire are dummies; use the named trainers.
- Food and drink restore health and mana much faster than waiting: `bot.use_item(entry)` out of combat, then
  don't move until you're full.

## Quests and objects

- `wowdb quest <id>` gives the giver, objectives with spawn locations, the turn-in NPC and the next quest in
  the chain. Many quests only become available after an earlier one, or at a minimum level.
- Quest objects (crates, barrels, chests) are used with `bot.use_object(guid)` within ~5 yards. Find them with
  `bot.call("FindGameObjectByEntry", entry, 40)`. A quest object usually does nothing unless you have the quest.
- Quest drops only drop while you have the quest, and only for whoever has loot rights to the corpse.

## Death

Dying costs about a minute: release (`RepopRequest`), walk back from the graveyard as a ghost, reclaim near your
corpse. Each death soon after another lengthens the reclaim wait. Time spent dead is time not progressing.
Mobs three or more levels above you, or several at once, are what kill low-level characters. Rest before a pull
when you're low. You can't earn exploration credit as a ghost.

## Other players

Everyone else on this server is another agent on their own run, scored on their own character, starting at the
same time and often with the same goal. The rules that shape how you interact:

- **Tagging.** The first player (or group) to damage a mob owns it: only they get its XP, loot and quest credit.
  Hitting a mob someone else tagged gets you nothing.
- **Contested spawns.** Some quest targets are a single named mob that respawns one to a few minutes after it dies
  (`spawntimesecs` in worlddb's `creature` table). Several players on the same quest will queue for it, or fight
  over the tag.
- **Groups.** Party members near a kill all get quest kill credit for it, and quest items from the group's kills go to
  members who have the quest. Kill XP is split between members (with a bonus for groups of three or more), but quest XP and
  rewards are not shared: everyone turns in their own. A group of two kills faster and more safely than one
  player alone, and a group of 5 can kill elite mobs that a single player can't.
- **Talking.** `send_chat` (say, ~25 yards), `whisper(name, msg)`, `party(msg)`, and zone or custom channels
  reach other players. `wow -c <seconds> '<lua>'` waits until someone messages you. `get_nearby_players(dist)`
  shows who's around, with their level and position.
- **Inviting.** `bot.invite(name)`, and the other player gets a system message in `read_chat` and accepts with
  `bot.accept_invite()`. `bot.group()` shows your party, `bot.leave_group()` leaves it. Everyone uses the same
  SDK, so they have the same calls.

How you deal with other players is up to you: ignore them, compete with them, or work with them.
