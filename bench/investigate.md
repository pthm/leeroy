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

### Lifts and elevators (static transports)

About 60 lifts and moving platforms (game object type 11), and the SDK knows none of them: the Undercity lifts,
Thunder Bluff's Mesa lifts, Thousand Needles' Great Lift, Shattrath's lifts to the terraces (Aldor Rise, Scryer's
Tier), Valgarde and Vengeance Landing, Icecrown and Grizzly Hills, Gnomeregan's, the Burning Steppes scaffold cars,
the Deeprun Tram's cars (map 369; agents walk the tunnel) and some in instances.
`SELECT * FROM gameobject_template WHERE type = 11` lists them.

- How the server runs them: StaticTransport, a short animation from TransportAnimation.dbc (mostly straight up and
  down, pausing at each end) on a fixed period; its progress comes in the create packet (UPDATEFLAG_TRANSPORT), as a
  boat's does. Passengers ride the same way: movement with ONTRANSPORT and the lift's guid.
- What breaks now: a platform moves, so the navmesh hasn't got it. The floors above and below are islands, and a route
  across (into Undercity, up Thunder Bluff) fails as unreachable, like the Lake Jorune plateau but with a way the SDK
  doesn't know. transports.list() only looks at type 15 (boats, zeppelins).
- Plan, reusing the boat and portal work:
  1. Where a lift is: TransportAnimation.dbc (copy it at setup) and the progress, like transportpath.go but simpler;
     lifts in transports.list() with their stops (where the platform waits, when it arrives and leaves).
  2. Riding: transports.board and leave work for any transport given a spot; a lift has no crew to copy one from,
     but a platform is flat: its centre.
  3. Routes through a lift: act.go_to already takes a same-map portal when a route fails (world._via_portal); a lift
     helper finds the lift whose landings join the start's floor and the destination's (each landing the nearest
     navmesh floor to the platform at that stop, checked reachable), walks to it, waits, rides, steps off, walks on.
  4. The viewer: lifts in the transports layer (mod-recorder logs only motion transports now: a module change and a
     worldserver restart).
- Test on Thousand Needles' Great Lift and an Undercity lift. Needed before a Horde or Northrend task, or levelling
  that may reach Thousand Needles (Alliance, 25-35).

### Better boat and zeppelin handling

Boarding and leaving work (transports.go: a straight walk onto a crew member's spot with ONTRANSPORT, and off onto
the nearest floor) and agents use them; what's left:

- Zeppelins aren't tested (their crew stand on the gondola: it should work). Test one first, with the GM character.
- No act helper: agents script waiting, boarding and leaving from transports.list(). An act.ride{transport, to}, or
  act.go_to taking a transport when a route crosses water, like same-map portals.
- No walking on deck: a deck navmesh per transport model is possible (the models are in the vmaps: GameObjectModels
  .dtree to a .vmo; mmaps_generator --file builds a tile from a raw triangle list; AzerothCore skips transport maps,
  which hold only the crew). Then routing in the deck's axes, and movement that keeps ONTRANSPORT with the deck
  position updated while the ship moves (today any move steps off), and boarding along a path, not a straight line.
  Only worth it if zeppelins don't board cleanly or boarding should be strictly what a real client can do: the
  straight walk crosses a gap of a few yards a real client would fall into.

## azworld

(nothing open)

## Harness

- **An agent that exits early stays stopped.** Haiku quit with 78 minutes left and Sonnet with 64 (alerts.log; exit 0,
  no restart). Decide whether an early exit before the goal gets one continuation prompt (the clock and score are
  unchanged either way), or stands as the agent's choice and is reported as such.
