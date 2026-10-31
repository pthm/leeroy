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

- `move.to` requires a complete navmesh route with physical support and static body clearance.
  Missing routes return `route_unreachable`; blocked geometry returns `route_blocked`; missing collision
  evidence returns `collision_unavailable`. A changed starting pose during planning returns `route_stale`.
  A walk that stops short returns `stuck`. Arrival requires less than 2.5 yards total gap and at most
  1.5 yards vertical gap. A nearby destination is subject to the same checks.
  Inside a task, `local r = await(world.path(x, y, z))` checks a single leg without moving. A successful
  `r.data` is a `PathCheck`: `status="verified"`, `reachable=true`, map, 3D waypoints, walking distance,
  endpoint gaps, floor validity, and `collision` evidence. Evidence includes `state`, `reason`, blocked
  `segment`, `position`, `normal`, `model`, and whether a bounded replan succeeded (`replanned`).
  Its `scope="static"`: moving doors, lifts and other dynamic objects are outside this check.
  A disconnected or truncated route returns `route_unreachable`, with a prefix when available
  (`status="partial"`). A prefix still needs its own successful validation before movement.
  Missing navigation data returns `not_available`. Unknown gaps and floors are omitted.
- Detour polygon references are retained through the funnel, caches and preparation. Samples follow
  the selected polygon's detail surface, including every portal crossing. Projection is bounded to
  0.75 yards horizontally and 1.5 yards vertically. Support is sampled at most every 0.25 yards;
  physical heights come from nearby terrain and movement-mask WMO/M2 rays. Overhead terrain does
  not replace a cave floor. Final body segments use continuous capsule sweeps against indexed triangles.
  The default standing profile is radius 0.3, height 2.03128, step 1.4 yards and maximum slope 60 degrees:
  it climbs what the server's navmesh climbs (1.6 yards, 60 degrees) as far as a body that tall allows.
  This is a fixed walking profile; mount/shape-specific dimensions and dynamic collision are not modeled.
- Routes jump what the navmesh climbs but a step can't (a 1.5 yard fence rail): when walking and the bounded
  repairs below fail, preparation looks for a jump from up to 4 yards back, on the client's own jump (1.64 yards
  up at its peak, about 6 yards at a run), with the whole body swept along the arc and a landing a body can stand
  on beside the route. Walking routes are preferred to jumping ones. The jump goes out as the client sends one
  (MSG_MOVE_JUMP, falling heartbeats, MSG_MOVE_FALL_LAND); a new move or a stop in mid-air waits for the landing.
  Off-mesh actions require separate handling and are refused by walking preparation.
- A blocked path gets at most three alternative portal placements and six local waypoint attempts,
  sharing the caller's deadline. Every alternative passes the complete support and clearance checks.
  Unresolved obstacles remain `route_blocked`; missing coverage remains unknown. Prepared movement
  preserves the checked points and precomputed swimming modes without querying heights during ticks.
  Direction/grade changes are sent at checked vertices, even across coarse ticks; collinear samples
  need no additional packet.
  Support sampling is discrete, so sub-sample ground gaps are not a continuous terrain-contact guarantee.
- Route checks have a 15-second wall deadline including queueing (`route_timeout`). Legs are limited to
  4096 yards in 3D and a 1024-tile search rectangle (`route_limit`). A session admits at most 16 pending
  checks (`route_busy`) and runs one search at a time, shared with movement and nearby-unit reachability.
  Up to 128 exact map/start/destination searches are cached; partial routes, failures and cancelled searches are not cached. Nearby-unit reachability also requires exact endpoint matches for cache reuse; unknown collision coverage is not cached as unreachable.
  Awaiting with a shorter timeout only stops the wait. Task pause/stop/failure/return or session close
  cancels owned checks; recovery expiry does too.
- Cancellation reaches tile loading, lock waits, sliced polygon search, surface sampling, collision traversal and bounded repair. A cancelled planner
  cannot publish a late movement path. Cancellation is cooperative: a local file read already in the kernel
  or one bounded Detour operation must return first. Floor/LOS helper queries remain synchronous but have a cooperative 60 ms spatial budget; expiry is unknown.
  Movement-lease preemption or expiry also cancels that task's pending route checks, freeing the search
  slot for the next owner. Route outcomes retain status, source, endpoints, gap, floor and collision evidence in diagnostics.
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
  Exposure is observed before each leg and during walking, retaining the latest 64 observations.
  It is diagnostic by default. `stop_on_danger=true` pauses movement while checking and returns
  `danger_ahead` for hostile or unknown evidence; it resumes only after clear evidence.
- `await(world.danger(x,y,z,radius))` or waiting `act.danger{x,y,z,radius=25}` yields a `DangerCheck`
  (`clear`, `hostile`, or `unknown`). It combines observed living hostiles, aggro range, reachable floor and LOS.
  It shares route admission, cancellation and the 15-second deadline. Points outside the local observation
  radius or without collision/navmesh evidence are unknown, never implicitly safe.
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
- Raw `move.unstuck` uses the same owner and a 20-second lease. It succeeds only after an observed teleport.
  Teleport, map transfer and login reset invalidate movement leases and pending plans with `teleported` or
  `world_changed`. Movement-owner diagnostics record acquisition, waits, releases and preemption.
  A lease by itself does not establish route safety or recovery safety.
- `units.nearby` leaves out units nobody can select: the invisible triggers the server uses for events and scripts.
- Entering a place triggers it as in the game: exploration objectives complete, inns and cities make you rest.
- While the server moves you (a flight, a teleport, fear, confusion) the client sends no movement of its own:
  `move.to` is refused with the reason (`in_flight`, `feared`...), `me.movement()` says who moves you, and
  `movement_changed`, `flight_start` and `flight_end` events mark the changes.
- Teleports are `teleported` events (`far = true`: a different map, and the world around you reloads). Portals are
  objects (`type = "portal"`): `objects.use(portal)`. Boats and zeppelins aren't supported.
