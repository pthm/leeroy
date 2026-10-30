# Moving and seeing what's around you

Where things are from where you stand, walking and its traps. The rest of the SDK is in sdk.md.

## Seeing what's around you

- Everything around you (`units.*`, `objects.*`, `me.status`) has its place measured from you too: `bearing`
  (degrees from where you face: 0 ahead, 90 right, -90 left, 180 behind), `clock` (12 ahead, 3 right, 6 behind) and
  `dz` (yards above you; negative: below, like a mob in the mine under you).
- A living hostile creature has `aggro_radius`, how close you can come before it attacks on sight (20 yards at your
  level, a yard more per level above you, a yard less per level below, at least 5), and `in_aggro` when you're
  inside it and in its sight.
- `wow look [radius]` (`world.look`) draws a top-down text map, north up: the ground (`^` higher, `v` lower, `#` no
  floor at your height), hostile creatures by capital letter with their aggro range shaded, other creatures,
  players and lootable corpses, then a line for each with level, distance, direction, height and aggro.
- `wow status` lists the `danger` around you: up to five hostile creatures whose aggro range you're in or within 15
  yards of, nearest edge first, each with its `margin` (yards to the edge of its range; negative: you're inside) and
  its `pack` (other hostiles within 10 yards of it: what comes along if you pull it). `mobs_nearby.in_aggro` counts
  the ones that have you in range now.

## Moving

- With a navigator, `move.to` permits a direct-line fallback only for a short hop of at most 5 yards; longer
  missing paths do not move. With no navigator it can walk directly. `data.no_path` records a failed navmesh
  lookup, and a walk that stops short returns `stuck`.
  Inside a task, `local r = await(world.path(x, y, z))` checks a single leg without moving. A successful
  `r.data` is a `PathCheck`: `status="verified"`, `reachable=true`, map, 3D waypoints, walking distance,
  start/endpoint gaps, vertical gap and floor validity. Both 3D endpoint gaps must be at most 3 yards.
  A disconnected endpoint returns `route_unreachable`, with a reachable prefix when available
  (`status="partial"`); the caller chooses whether to use it. Missing navigation data returns
  `not_available`. Unknown gaps and floors are omitted, never certified.
- Route checks have a 15-second wall deadline including queueing (`route_timeout`). Legs are limited to
  4096 yards in 3D and a 1024-tile search rectangle (`route_limit`). A session admits at most 16 pending
  checks (`route_busy`) and runs one search at a time, shared with movement and nearby-unit reachability.
  Up to 128 exact map/start/destination searches are cached; failures and cancelled searches are not cached.
  Awaiting with a shorter timeout only stops the wait. Task pause/stop/failure/return or session close
  cancels owned checks; recovery expiry does too.
- Cancellation reaches tile loading, lock waits, sliced polygon search, and smoothing. A cancelled planner
  cannot publish a late movement path. Cancellation is cooperative: a local file read already in the kernel
  or one bounded Detour operation must return first. Floor/LOS helper queries remain synchronous.
  Movement-lease preemption or expiry also cancels that task's pending route checks, freeing the search
  slot for the next owner. Shared danger estimates and route-specific diagnostic events remain outstanding.
- `await(world.route(points, {map=...}))` checks an ordered route from your captured current position.
  Supply 1..64 3D waypoints as `{x,y,z}` tuples or `{x=...,y=...,z=...,map=...}` records; options accept
  only `map`, defaulting to the current map. Every waypoint must be on that map. One 15-second deadline
  covers all legs, and output is capped at 8192 navmesh points. The result is a `RouteCheck`: connected
  `waypoints`, total `distance`, `completed_legs`, and `legs` with each endpoint/floor check. Failure names
  the first `blocked_leg` (1-based). Timeout and cancellation retain any previously completed prefix.
  Each leg starts at the preceding check's actual endpoint.
- `azworld route` supplies an unverified terrain proposal. Its explicit form is
  `azworld route x1 y1 z1 x2 y2 z2 map --level N`; it preserves both requested elevations.
  Feed its `waypoints` and `map` to the session validator or `act.travel`. A terrain proposal is not
  evidence that the character can walk through a building, bridge or cave; see azworld.md for its limits.
- `act.travel{points=...}` validates the whole route before moving and holds its movement lease throughout.
  It requires explicit Z values by default. A blocked route fails before walking; `allow_partial=true`
  permits walking a known `route_unreachable` prefix, but the result still has `ok=false` because the
  requested destination was not reached. It never authorizes a timeout prefix. Results include
  `completed_waypoints`, the failed `waypoint`, and `validation` evidence; an authorized prefix walk sets
  `partial_walked=true`. `validate=false` permits unverified 2D planning and labels `data.status="unverified"`;
  it cannot be combined with `allow_partial`. A position change during validation returns `route_stale`.
  These checks establish walkability; hostile exposure is not yet assessed.
- `units.nearby` gives each living creature `in_sight`, and for the nearest `path_distance` (the walk) and
  `reachable`; `distance` is a straight line. Their shared 60 ms route budget includes waiting and search;
  an expired check stays unknown rather than being cached as unreachable. A mob in a mine below you can be 30 yards away and a 300-yard walk.
- `act.go_to{x=...,y=...,z=...}` preserves the requested elevation. Success requires horizontal tolerance
  (default 4 yards) and `vertical_tolerance` (default 3 yards) on the requested/current map.
  `wrong_floor`, `wrong_map`, and `unreachable` include the observed position, destination and gaps.
  Without Z it selects a floor using your height as a hint for points within 80 yards, or outdoor ground
  farther away; unknown ground fails rather than certifying arrival. Supply Z for caves, bridges and
  buildings. `act.travel` uses these same arrival checks for every waypoint.
- Walking doesn't fight: `move.to`, `act.go_to` and `act.travel` keep walking while something attacks you, and drag
  it into your next fight. Give `act.go_to` and `act.travel` an `on_attacked = function(attackers) ... end` (your
  fighting) and they stop for whoever attacks you on the way, then carry on.
- Raw `move.to` requests have one owner per session. Equal-priority callers queue FIFO for at most 5 seconds, then
  fail with `movement_busy` and the current owner when present. A nearby destination from another task is a
  separate request. Replacing your own task's destination fails the old request with `replaced_by_owner`.
- `move.owner()` exposes the active task, call, lease, request, destination, priority and phase; nil means idle.
  Request and destination are absent while a lease is held without a raw walk.
  `me.movement()` also exposes the queue, and `wow tasks` / `task.list()` show each task's movement.
- `move.stop()` cancels the calling task's walk (`cancelled`); it refuses another caller's walk with
  `movement_busy`. `move.cancel(request_id)` explicitly cancels an active or queued walk, including another
  task's. Stopping a raw walk retains its enclosing lease. An immediate `wow` evaluation has no persistent task
  identity: retain its request ID for cancellation.
- Pause, stop, failure and normal task return cancel that task's active and queued walks. Session close clears
  all ownership. Cancellation never counts as arrival. Start a task and await a walk if it must finish.
- Planning waits at most 15 seconds (`route_timeout`), and walking at most 600 seconds (`timeout`). There is
  one planning worker per session. Cancellation reaches the underlying search and prevents late publication;
  the next owner can plan after the cancelled computation exits its current bounded step.
- Movement-producing `act` helpers hold a lease across nested calls, waypoints and callbacks, including
  `on_attacked`. Their default priority is `normal`; `recover`, `corpse_run` and `spirit_res` use `recovery`.
  `move.face`, `move.turn` and `move.jump` refuse another caller's ownership with `movement_busy`.
- `move.with_control{fn=function() ... end, priority="urgent", queue_timeout_s=5, max_s=600}` provides an
  explicit scope. `fn` must return a completed action result. Priorities are `recovery > urgent > normal`;
  higher priority preempts immediately, equal priority queues FIFO, and queued higher priorities run first.
  Queue waits accept 0..60 seconds; lease duration is greater than 0 and at most 600 seconds.
- Nested scopes share the outer lease and deadline. A higher nested priority promotes the lease for its
  remaining lifetime. Preempted scopes fail with `preempted` and identify the new owner; expired scopes fail
  with `lease_timeout`. A callback cannot silently reacquire movement within that failed scope, even after
  the new owner releases. Callback errors also release ownership.
- Expiry releases movement even while a callback sleeps. It does not interrupt arbitrary Lua callback code:
  the scope returns its failure when that callback unwinds. Callback code still needs its own stopping
  condition. `act.recover` additionally coordinates duplicate callers and bounds its waits and attempts;
  see [Death and travel](death-and-travel.md) for its policies and route-query limitation.
- `act.approach` retains its pending walk until the target moves and checks stopping/facing before success.
  `act.follow` requires `until` or `timeout_s`, has a maximum 600-second duration, and returns
  `player_not_visible` after 5 seconds of absence. Its stopping callback is checked each pass.
- Raw `move.unstuck` arbitration, teleport invalidation, movement-owner diagnostic events, and interruption
  of native path queries remain pending. A lease by itself does not establish route safety or recovery safety.
- `units.nearby` leaves out units nobody can select: the invisible triggers the server uses for events and scripts.
- Entering a place triggers it as in the game: exploration objectives complete, inns and cities make you rest.
- While the server moves you (a flight, a teleport, fear, confusion) the client sends no movement of its own:
  `move.to` is refused with the reason (`in_flight`, `feared`...), `me.movement()` says who moves you, and
  `movement_changed`, `flight_start` and `flight_end` events mark the changes.
- Teleports are `teleported` events (`far = true`: a different map, and the world around you reloads). Portals are
  objects (`type = "portal"`): `objects.use(portal)`. Boats and zeppelins aren't supported.
