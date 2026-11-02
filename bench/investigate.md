# To investigate

Things runs turned up that need a closer look (most from travel-models, 20261101-145156). Not agent-facing (the agents don't read this). Each has its evidence
and how to reproduce it; move it into a spec or fix it, then delete it here.

## Navigation

### Navmesh islands with no way off (Lake Jorune, Terokkar)

The Lake Jorune graveyard (1298, at -2434 4661 161, map 530) sits on a plateau the navmesh doesn't connect to
anything: a flood from it reaches 57,573 sq yd (x -2683..-2340, y 4411..4804), against 36 million from Shattrath's
graveyard. The terrain drops 66-83 degrees in every direction (24 bearings sampled), so it's real geometry, not tree
holes. A player jumps off and takes the fall; the navmesh has no drop links, so routes from it fail
(`terrain_unreachable`, or a partial path ending at the rim, -2367 4750 143). A death nearby resurrects there again, and so does a spirit healer res from a ghost run: the server picks the
graveyard by the corpse's map and position but the ghost's current zone (`GameGraveyard.cpp` GetClosestGraveyard
with nearCorpse), so with the corpse on another continent nothing matches and it falls back to a Terokkar
graveyard, this one, every time (Cosimo about 35 min, Evander about 25 min and a hearth back to redo the route).

- Seen: travel-models (20261101-145156), Cosimo (Opus) stuck on it for 20+ minutes.
- Reproduce: `NAVMAP=530 go run ./azeroth-sdk/cmd/navprobe data -2434 4661 161 -1793 4932 -22` (ends 602 yd short).
- Questions: how many graveyards (and other spots agents get put: quest teleports, summons) are on islands like
  this? Should routes plan survivable drops (fall height against health), as off-mesh links or a route option?

### Walking where a player can't: 60 degree navmesh slopes (Dun Morogh to the Wetlands over the mountains)

The navmesh is built for 60 degree slopes (AzerothCore's mmaps default, for creatures; `pathfinding/collision.go`
uses the same), but the 3.3.5 client stops a player walking up slopes past about 50 degrees, and the server doesn't
check. So an SDK character can climb what a player can't: Dorian (Luna) went from Ironforge straight over the
mountains to the Wetlands (up to z 445 at -4367 -2212, climbs of 55-58 degrees between samples, e.g. -4424 -2637
308 -> 318 over 6.6 yd) instead of through Loch Modan and Dun Algaz, and reached Menethil in about 20 minutes.

- Seen: travel-models (20261101-145156), a59.record.jsonl, from about 240 s after leaving the tram.
- The client's limit: 50 degrees. [WoWee](https://github.com/Kelsidavis/WoWee) (`include/rendering/movement_limits.hpp`)
  refuses ground whose normal's z is under cos 50 = 0.6428, the same for terrain, WMO and M2 surfaces, and steps over
  0.6 yd, as retail does. (Their terrain skipped the check at first, so any mountain was walkable: the same bug.)
- Plan: a second navmesh for the SDK only, at 50 degrees; the server keeps its 60 degree one, so creatures path as
  before.
  1. Build AzerothCore's mmaps_generator: the compose `ac-tools` service (`--profile tools`, target `tools`), from
     our server source; there's no arm64 image to pull. About 10-20 minutes.
  2. Generate from data/maps and data/vmaps into a new directory with `walkableSlopeAngle: 50` (mmaps-config.yaml),
     the rest unchanged. The generator takes a map and `--threads`: the continents first (0, 1, 530, 571). Time
     unmeasured; guess 1-2 hours on 12 cores.
  3. Swap it in as data/mmaps between runs (sessions load tiles as they go: never under a live run).
  4. `WalkingBody().MaxSlope` 60 -> 50 (`pathfinding/collision.go`).
  5. Check: Dorian's leg (Ironforge to Menethil) should go through Loch Modan; routes with stairs and ramps that work
     now still do (Northshire Abbey, Stormwind, Ironforge); look for places a player reaches that the stricter mesh
     cuts off (a player can jump up a short steep stretch).
- Don't run the generation during a benchmark run: it takes every core and would slow the agents.
- Step height, separately: WoWee says 0.6 yd, our body takes 1.4 (the navmesh climbs 1.6) because the Northshire
  Abbey stairs have 1.05 yd steps. Find out which is right before changing it.

## SDK

- **No boats or zeppelins.** The SDK drops ONTRANSPORT whenever the character moves (`client/selfmove.go`), so it
  can't ride one; the docs say so. Haiku and Sonnet worked that out at the Stormwind dock (The Bravery to Auberdine
  is the intended route to Darnassus) and quit. Needs: boarding, riding along the transport's path, getting off.

## azworld

(nothing open)

## Harness

- **An agent that exits early stays stopped.** Haiku quit with 78 minutes left and Sonnet with 64 (alerts.log; exit 0,
  no restart). Decide whether an early exit before the goal gets one continuation prompt (the clock and score are
  unchanged either way), or stands as the agent's choice and is reported as such.
