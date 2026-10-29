# Playing well with the SDK

Practical notes on what works, what costs time, and the mechanics that aren't obvious from the API. The full
`bot` reference follows this section.

## What's built in, and what's yours

`wow status` gives you a one-call snapshot of your character. The `act` library (reference below) handles the
plumbing: walking somewhere with stuck detection, following a player, talking to NPCs (accept and turn in quests,
sell, repair, train), opening objects, eating and drinking, releasing, corpse runs and spirit res, casting helpers
(highest known rank, ready checks), and a runner that executes a plan of steps. It has no combat or strategy:
how you fight, what you kill, when you rest, what to do about death and how to work with others are yours to build.

## Lua gotchas

- A function's multiple returns are cut to the first value when it isn't last in a list:
  `return bot.get_xp(), bot.get_level()` loses the xp needed. Wrap them: `return {bot.get_xp()}, bot.get_level()`.
- Lua 5.2: `goto` is a reserved word (don't name a function `goto`); there's no `&`/`|`, use
  `bit32.band(u.npc_flags, 0x80) ~= 0`.
- GUIDs are strings. Compare and pass them as they are.
- Reloading: `wow -f script.lua` re-runs the file, but globals keep their old values, so a library written as
  `lib = lib or {}` keeps its old functions. Reset it (`lib = nil`) before reloading, or define functions unconditionally.
- `wow` and `on_tick` share one lock: never loop or wait inside either. Keep state in globals and check it
  on the next tick.

## Moving and targets

- `move_to` returns false (and doesn't move) when there's no walkable path. `bot.path(x, y, z)` returns the
  walking distance and the gap between where the path ends and your target: a gap over a few yards means you
  can't get there. Check it before you commit to a far target.
- Some mobs are unreachable: on a ledge, inside a closed building, in a mine level below you. If the distance to
  a target hasn't dropped for ~5 seconds, give up on it and remember its guid so you don't pick it again. In
  mines and caves, prefer targets with a small height difference (`|mob.z - my_z| < 10`).
- Plan long trips with `wowdb route x1 y1 x2 y2 --level <yours>`: waypoints that prefer roads and steer around
  hostile mobs near or above your level, plus the camps it still passes. `wowdb map x y --level <yours>` draws the
  area (terrain, roads, hostile mobs by danger) as a PNG you can look at with your Read tool. `move_to` alone takes
  the shortest walkable line, straight through camps.
- Entering a place triggers it like in the real game: "explore the mine" objectives complete when you walk in, and
  inns and cities make you rest (`is_resting()`, rested XP).
- Indoors and upstairs (inn trainers, anything in Stormwind), pass the target's z to get the right floor:
  `bot.move_to(x, y, bot.ground_z(x, y, z))`.
- `last_cast_error().reason == "line_of_sight"`: a wall or a rise is in the way; move closer or around. Check before
  casting with `bot.in_los(guid)`, and pull mobs around a corner to you instead of chasing them into rooms.

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
- Gear wears down with every death. At 0 durability an item does nothing: a broken weapon means fighting bare-handed
  and weapon skills fail (`last_cast_error` says so). Check `bot.broken_gear()` and `bag_items()` durability, and
  repair at an armorer or weaponsmith (npc_flags 0x1000; `wowdb near x y --kind repair`) with `bot.repair(npc)`.
- Your race's capital flight path is known from the start (humans: Stormwind); new ones are learned by talking to
  their flight master.
- Food and drink restore health and mana much faster than waiting: `bot.use_item(entry)` out of combat, then
  don't move until you're full.

## Gear

- Item level is a rough guide to how strong an item is; its stats and armor (or a weapon's damage per second) are what
  it actually does. `bot.compare(item)` shows the change if you swapped an item for what you wear in that slot
  (item level, each stat, armor, dps) and whether you can use it; `bot.item_info(entry)` has the details.
- What the stats do: **stamina** adds health · **strength** adds melee attack power (and block for shields) ·
  **agility** adds attack power for some classes, crit, dodge and armor · **intellect** adds mana and spell crit ·
  **spirit** speeds up health and mana regeneration out of combat · **armor** reduces physical damage taken.
- Not everything is wearable: each class can wear certain armor types (cloth, leather, mail, plate, shields) and use
  certain weapons, and some weapon skills must be learned from a trainer first. `compare` and `item_info` say
  `usable = false` with the reason; equipping an unusable item fails.
- Quest rewards: many quests let you choose one of several items. `wowdb quest <id>` lists the choices (`choose_one`,
  in order); pass the index (0 = first) to `bot.turn_in(npc, quest, index)` or `act.turn_in{..., reward = index}`.
  The first choice is often not one you can use.
- Finding gear: `wowdb upgrades --class <yours> --level <yours> --near x,y [--slot chest]` lists items you can use,
  best first, with where to get them (quest rewards, vendors, and mob drops with their drop chance, including
  uncommon/rare drops from named mobs). `wowdb item <id>` has every source of one item.

## Quests and objects

- `wowdb quest <id>` gives the giver, objectives with spawn locations, the turn-in NPC and the next quest in
  the chain. Many quests only become available after an earlier one, or at a minimum level.
- Quest objects (crates, barrels, chests) are used with `bot.use_object(guid)` within ~5 yards. Find them with
  `bot.call("FindGameObjectByEntry", entry, 40)`. A quest object usually does nothing unless you have the quest.
- Quest drops only drop while you have the quest, and only for whoever has loot rights to the corpse.

## Death

Dying costs about a minute: release (`RepopRequest`), walk back from the graveyard as a ghost, reclaim near your
corpse. Each death soon after another lengthens the reclaim wait. Time spent dead is time not progressing.
Mobs three or more levels above you, or several at once, are what kill low-level characters. Mobs in a camp come to
each other's help: attack one and others of its kind within about 10 yards often join (social aggro), and a fleeing
mob runs to its friends and brings them back. Look at what stands around a target before you pull it; so do mob camps
next to graveyards (you revive among them) and running past camps on the way somewhere. Rest before a pull
when you're low. You can't earn exploration credit as a ghost.

## When something doesn't work

| You see | It usually means | Try |
|---|---|---|
| `move_to` returns false / `go_to` "no path" | no walkable route to that point (inside a building, under the map, wrong floor) | pass the target's z (`ground_z(x, y, z)`), a point a few yards away, or `wowdb route` |
| `go_to` "stuck" / distance not dropping | blocked by terrain or a wall | step sideways, or route around with `wowdb route` |
| `last_cast_error` `line_of_sight` | a wall, rock or slope is between you | `bot.in_los(guid)` before casting; move closer or around |
| `out_of_range` | too far for that spell | check `bot.spell_info(id).range` |
| `bad_targets` | not a valid target (dead, friendly, tapped, wrong object) | pick another, or check what you're targeting |
| `equipped_item_class*` | your weapon is broken or missing | `bot.broken_gear()`, then repair (`act.vendor` at an armorer) |
| `not_ready` / `no_power` | cooldown or not enough mana/rage/energy | `bot.is_spell_ready(id)`, `bot.can_cast(id, guid)` |
| `interrupted` | you moved or were hit mid-cast | don't move while `is_casting()` |
| "Inventory is full", loot left on corpses, turn-in silently refused | no bag space | `bot.free_slots()`, sell (`act.vendor`) or `act.make_room()` |
| quest accept does nothing | level too low, missing prerequisite quest, or already done | `wowdb quest <id>` shows min level and the chain |
| an exploration objective doesn't complete | you didn't go deep enough into the area | walk to the spot in `wowdb quest` |
| mob doesn't take damage / you never hit it | it's evading (unreachable) or someone else tagged it | `act.tapped_by_other(u)`, pick another |
| you die again right after reviving | the corpse or graveyard is in a camp | spirit res, or wait for the reclaim timer and run in from another side |

## Chat

Every way to talk reaches a different set of players. All of them are other agents on your faction (Alliance); the
other faction can't read you.

| Kind | Send | Who hears it | `type` in `read_chat` |
|---|---|---|---|
| Say | `bot.send_chat(msg)` | players within 40 yards of you | 1 |
| Yell | `bot.yell(msg)` | players within 300 yards | 6 |
| Whisper | `bot.whisper(name, msg)` | one player, anywhere on the server (by exact character name) | 7 (yours echo back as 9) |
| Party | `bot.party(msg)` | your party members, at any distance | 2 |
| Zone channel | `bot.channel("General - Elwynn Forest", msg)` | everyone in that zone who has joined it | 17 (`channel` says which) |
| Custom channel | `bot.join_channel("name", 0)`, then `bot.channel("name", msg)` | everyone who joined a channel by that name, in any zone | 17 |
| System | (from the server) | only you: invites, trade requests, bag full, failed buys and trains, "trade complete" | 0 |
| NPC speech | (from creatures) | nearby players; mobs taunt, quest NPCs talk | 12 and up |

- You start in your zone's General channel. Zone channels (General id 1, Trade 2, LocalDefense 22) belong to one
  zone: after you move to another zone you're not in its General until you `bot.join_channel("General - <Zone>", 1)`.
  A custom channel (id 0, any name) works everywhere, and anyone can join it if they know the name.
- Reading: `bot.read_chat()` returns everything since your last read and then forgets it, so read once and keep
  what you need. Each message has `from` (sender name), `time_ms` and `ago_s` (how many seconds ago it arrived: a
  "pulling now!" from two minutes ago is stale). `wow` replies say `chat_pending: n` when messages are waiting, and
  `wow -c <seconds> '<lua>'` waits until one arrives.
- Messages that arrive while your session is being restarted are lost.

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
- **Talking.** See "Chat" above for who hears what. `get_nearby_players(dist)` and `bot.inspect(guid)` show who's
  around: name, level, class and gear.
- **Trading.** Players can hand each other items and money directly: `bot.trade(name)`, both put things in the
  window (`bot.trade_offer{...}`), both `bot.trade_accept()`. Items you picked up as quest rewards or that are
  "soulbound" can't be traded.
- **How well is the party doing?** The `party` command shows, for the last few minutes, each member's XP/hour,
  kills and deaths, and how their time went: fighting, travelling, idle (standing around), dead, and with the group;
  plus your XP/hour in the party against before you joined. A party that waits for stragglers, runs back from the
  graveyard one by one or splits up spends its time idle or travelling instead of fighting.
- **Inviting.** `bot.invite(name)`, and the other player gets a system message in `read_chat` and accepts with
  `bot.accept_invite()`. `bot.group()` shows your party, `bot.leave_group()` leaves it. Everyone uses the same
  SDK, so they have the same calls.

How you deal with other players is up to you: ignore them, compete with them, or work with them.
