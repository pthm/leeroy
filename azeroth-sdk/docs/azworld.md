# World data: azworld

The world's static data from the server's database: where things are, what drops what, who gives which quest. The
rest of the SDK is in sdk.md.

`azworld` reads the game's static world database: what drops where, who gives which quest and where its targets
are, which NPCs are hostile to you. It prints JSON, with relations resolved:

```sh
azworld search "Hogger"                          # items, quests, npcs, objects, spells, zones, achievements by name
azworld item 1931                                # stats and sources: drops, chests and nodes, vendors, quest rewards, crafting
azworld quest 176                                # giver and turn-in with coordinates, objectives with spawns, rewards, chain
azworld npc 448                                  # level, elite, reaction to you, spawns, loot, vendor stock, trainer spells
azworld spell 6673                               # cost, cast time, range, cooldowns, which trainers teach it
azworld zone "Elwynn Forest"                     # sub-areas, mobs by level, graveyards, inns, flight masters, vendors, trainers
azworld near -9460 70 --kind trainer             # nearest trainer|vendor|questgiver|flightmaster|innkeeper|repair|banker|graveyard|object
azworld near -9460 70 --kind mob --hostile --level 9-11   # nearest hostile mobs, levels 9-11
azworld quests --level 10 --race 1 --class 1 --near -9460,70   # quests you can pick up, nearest givers first
azworld area -9460 70                            # which area and zone a point is in
azworld route -9460 70 -9660 690 --level 7       # unverified outdoor terrain proposal (2D endpoints)
azworld route -8873 -110 81 -8843 -110 81 0 --level 7  # x1 y1 z1 x2 y2 z2 map; preserves both elevations
azworld map -9460 70 --level 7 [--to -9660,690]  # writes map.png (roads, buildings, water, hostile mobs, the route): view it with Read
azworld roads -9460 70                           # guard patrol paths near a point
azworld upgrades --class priest --level 7 --near -9460,70 [--slot chest]   # gear you can use, by improvement, and where it comes from
```

`reaction` is relative to a new character of each side (`hostile` attacks on sight; `neutral` only fights back).
Races: 1 Human, 3 Dwarf, 4 Night Elf, 7 Gnome; classes: 1 Warrior, 2 Paladin, 4 Rogue, 5 Priest, 8 Mage. Lists are
capped at 15; counts like `drops_total` give the full size. Coordinates match `me.position()`.

Route planning returns a terrain proposal, with `status="unverified"`, `reason="navmesh_validation_required"`,
`source="terrain_grid"`, and `validation_required=true`. Both forms require `--level N` (integer 1..255);
`--horde` selects Horde-relative hostile spawns. The four-coordinate form accepts `--map N` (default 0).
The seven-coordinate form includes its map as the last positional argument and rejects an additional
`--map`. Unknown flags and malformed coordinates fail explicitly.

The result includes `map`, 3D `from` and `destination`, `waypoints` as `{x,y,z}` triples,
estimated `distance` in yards and `road_fraction` (0..1). Explicit endpoint elevations are preserved;
interior points use outdoor terrain height. `terrain_endpoint_gaps` reports the difference between each
explicit endpoint Z and outdoor terrain, without certifying its floor. With 2D inputs, terrain supplies
both endpoint heights. Buildings, bridges and caves are not represented by this planner.

Submit the returned waypoints and map to `await(world.route(points,{map=map}))`, or use
`act.travel{points=points,map=map}`, which validates by default. A valid terrain proposal can fail that
navmesh check. Preserve explicit Z values instead of replacing them with outdoor ground height.

Planning has one 15-second deadline for admission, data queries, terrain sampling and search. At most one
plan computes per process. Endpoints must be at most 4096 yards apart (3D for explicit Z), X/Y must lie in
-17066..17066, and the padded search rectangle is capped at 400,000 eight-yard cells. Data queries are
capped at 20,000 road waypoints and 20,000 hostile spawns. Output is capped at 64 waypoints so it can be
submitted directly to the session validator; complex or long routes may require shorter queries.
Cancellation reaches SQL, cache-lock waits, terrain reads and search steps. Terrain files are limited to
16 MiB and road tiles to their fixed 16 KiB format. An already-running filesystem operation must return
before cancellation takes effect; a stalled kernel read is not covered by a hard wall-clock guarantee.

Failures return `status="failed"`, `reason`, and `error={code,message}`, with exit status 1:
`invalid_arguments`, `route_limit`, `route_timeout`, `cancelled`, `not_available`, or
`terrain_unreachable`. The latter means the terrain grid cannot connect the endpoints, not that the
session navmesh has proved the destination unreachable. Failed plans do not publish usable-looking
waypoints.

`exposure` is a static 2D estimate: `source="static_spawns_2d"`, `floor_checked=false`,
`los_checked=false`, and `passes_near` lists nearby spawns used in the cost calculation (level at least
your level minus two). Road preference and hostile costs are separate from terrain walkability; hostile
spawns increase cost without turning a walkable cell into a blocked one. Live danger checks remain
separate from this proposal.

**Raw SQL:** `azworld sql "SELECT ..."` runs read-only SQL against the world database (MySQL; TSV output, at most
300 rows):

| Table | Useful columns |
|---|---|
| `creature` (spawns) | `guid, id` (= template entry), `map, position_x, position_y, position_z, spawntimesecs` |
| `creature_template` | `entry, name, subname, minlevel, maxlevel, faction, npcflag, rank` |
| `creature_loot_template` | `Entry` (= creature entry), `Item, Chance` |
| `item_template` | `entry, name, Quality` (0 grey ... 4 epic), `ItemLevel, RequiredLevel, InventoryType, SellPrice` |
| `quest_template` | `ID, LogTitle, QuestLevel, MinLevel, AllowableRaces, ObjectiveText1, RequiredNpcOrGo1, RequiredNpcOrGoCount1, RequiredItemId1, RequiredItemCount1, RewardItem1` |
| `creature_queststarter` / `creature_questender` | `id` (creature entry), `quest` |
| `npc_vendor` | `entry` (vendor creature), `item` |
| `gameobject`, `gameobject_template`, `gameobject_loot_template` | spawns, names and loot of chests, herbs, ore |

The `wowbench` schema has derived data (qualify the table names): `creature_spawn` and `gameobject_spawn` (spawns
with `area` and `zone`), `reaction` (`creature_template.faction` to hostility), `spell` (names, costs,
cooldowns), and the achievements:

| Table | Useful columns |
|---|---|
| `wowbench.achievement` | `id, name, description, points, category, faction` (-1 any, 1 Alliance), `flags` (0x1 = a statistic, not earnable), `count`/`ref_achievement` (meta achievements) |
| `wowbench.achievement_criteria` | `id, achievement, type, asset, quantity, description`: all of an achievement's criteria count unless `achievement.count` says how many |
| `wowbench.criteria_type` | `id, name` (`explore_area`, `complete_quest_count`, `kill_creature`, `learn_skill_level`...); `asset` is a creature, quest, spell, item or map overlay id depending on the type |
| `wowbench.achievement_category` | `id, parent, name` |
| `wowbench.area` | `id, map, zone` (parent zone, 0 = a zone itself), `level, name` |
| `wowbench.map_overlay` | `id` (= `asset` of `explore_area` criteria), `area1..area4` (the areas that reveal it) |
| `wowbench.area_location` | `area, map, x, y` (a point inside the area), `min_x, max_x, min_y, max_y` |

```sh
# level 3-5 mobs near Northshire (map 0 = Eastern Kingdoms), by spawn count
azworld sql "SELECT t.entry, t.name, t.minlevel, t.maxlevel, COUNT(*) n, ROUND(AVG(c.position_x)) x, ROUND(AVG(c.position_y)) y
  FROM creature c JOIN creature_template t ON t.entry = c.id
  WHERE c.map = 0 AND t.maxlevel BETWEEN 3 AND 5 AND t.npcflag = 0
    AND c.position_x BETWEEN -9500 AND -8500 AND c.position_y BETWEEN -800 AND 200
  GROUP BY t.entry ORDER BY n DESC LIMIT 10"
```

`rank` is a MySQL reserved word: write it as `` t.`rank` ``.
