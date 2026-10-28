# World data: `wowdb` (structured) and `worlddb` (raw SQL)

`wowdb` answers the usual planning questions in one call, as JSON, with relations already resolved: where
an item drops (with chances and spawn locations), who gives a quest and where its targets are, which NPCs
are hostile to you, the nearest trainer or vendor. Start here; drop to `worlddb` SQL for anything else.

```sh
wowdb search "Hogger"                          # items, quests, npcs, objects, spells, zones, achievements by name
wowdb item 1931                                # stats + sources: drops, chests/nodes, vendors, quest rewards, crafting
wowdb quest 176                                # giver/turn-in with coordinates, objectives with spawns, rewards, chain
wowdb npc 448                                  # level, elite?, hostile/neutral/friendly to you, spawns, loot, vendor stock, trainer spells
wowdb spell 6673                               # cost, cast time, range, cooldowns, which trainers teach it and for how much
wowdb zone "Elwynn Forest"                     # sub-areas, mobs by level, graveyards, inns, flight masters, vendors, trainers
wowdb near -9460 70 --kind trainer             # nearest trainer|vendor|questgiver|flightmaster|innkeeper|repair|banker|graveyard|object
wowdb near -9460 70 --kind mob --hostile --level 9-11   # nearest mobs that attack on sight, levels 9-11
wowdb quests --level 10 --race 1 --class 1 --near -9460,70   # quests you can pick up, nearest givers first
wowdb area -9460 70                            # which area/zone a point is in
```

`reaction` is relative to a new character of each side (`hostile` attacks on sight; `neutral` only fights
back). Races: 1 Human, 3 Dwarf, 4 Night Elf, 7 Gnome; classes: 1 Warrior, 2 Paladin, 4 Rogue, 5 Priest,
8 Mage. Results are capped (15 per list); counts like `drops_total` give the full size.

## Raw SQL: `worlddb`

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

The achievement catalogue, area locations and other derived data are in the `wowbench` schema (qualify the
table names; `creature_spawn`/`gameobject_spawn` are the spawn tables with `area` and `zone` filled in,
`reaction` maps `creature_template.faction` to hostility, `spell` has names, costs and cooldowns):

| Table | Useful columns |
|---|---|
| `wowbench.achievement` | `id, name, description, points, category, faction` (-1 any, 1 Alliance), `flags` (0x1 = statistic, not earnable), `count`/`ref_achievement` (meta achievements) |
| `wowbench.achievement_criteria` | `id, achievement, type, asset, quantity, description`: all of an achievement's criteria are needed unless `achievement.count` says how many |
| `wowbench.criteria_type` | `id, name` for `achievement_criteria.type` (`explore_area`, `complete_quest_count`, `kill_creature`, `do_emote`, `learn_skill_level`...); `asset` means a creature, quest, spell, item, map-overlay... id depending on the type |
| `wowbench.achievement_category` | `id, parent, name` |
| `wowbench.area` | `id, map, zone` (parent zone id, 0 = a zone itself), `level, name` |
| `wowbench.map_overlay` | `id` (= `asset` of `explore_area` criteria), `area1..area4` (the area ids that reveal it) |
| `wowbench.area_location` | `area, map, x, y` (a point inside the area), `min_x, max_x, min_y, max_y` |

You get `explore_area` credit by entering the area: `bot.move_to(x, y, bot.ground_z(x, y))`. For NPCs and objects
indoors (an inn's upstairs trainer, anything in Stormwind), pass their z: `bot.move_to(x, y, bot.ground_z(x, y, z))`.

```sh
# level 3-5 mobs near Northshire (map 0 = Eastern Kingdoms), by spawn count
worlddb "SELECT t.entry, t.name, t.minlevel, t.maxlevel, COUNT(*) n, ROUND(AVG(c.position_x)) x, ROUND(AVG(c.position_y)) y
  FROM creature c JOIN creature_template t ON t.entry = c.id
  WHERE c.map = 0 AND t.maxlevel BETWEEN 3 AND 5 AND t.npcflag = 0
    AND c.position_x BETWEEN -9500 AND -8500 AND c.position_y BETWEEN -800 AND 200
  GROUP BY t.entry ORDER BY n DESC LIMIT 10"
```

Coordinates match `bot.get_position()`. `rank` is a MySQL reserved word: write it as `` t.`rank` ``. Monster kill XP drops sharply for mobs well below your level.
