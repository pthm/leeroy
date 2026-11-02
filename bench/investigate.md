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
(The rule is now in death-and-travel.md; the island itself is still open.)

- Seen: travel-models (20261101-145156), Cosimo (Opus) stuck on it for 20+ minutes.
- Reproduce: `NAVMAP=530 go run ./azeroth-sdk/cmd/navprobe data -2434 4661 161 -1793 4932 -22` (ends 602 yd short).
- Questions: how many graveyards (and other spots agents get put: quest teleports, summons) are on islands like
  this? Should routes plan survivable drops (fall height against health), as off-mesh links or a route option?

### Step height

WoWee says the client steps up 0.6 yd; our body takes 1.4 (the navmesh climbs 1.6) because the Northshire Abbey
stairs have 1.05 yd steps. Find out which is right before changing it. (The slope limit is done: the SDK has its own
navmesh at the client's 50 degrees, README; at 50 the Dun Morogh to Wetlands mountain shortcut is gone, and the only
graveyards it cuts off are Outland's Twisting Nether corpse catchers and three flying-only plateaus.)

## SDK

(nothing open)

## azworld

(nothing open)

## Harness

- **An agent that exits early stays stopped.** Haiku quit with 78 minutes left and Sonnet with 64 (alerts.log; exit 0,
  no restart). Decide whether an early exit before the goal gets one continuation prompt (the clock and score are
  unchanged either way), or stands as the agent's choice and is reported as such.
