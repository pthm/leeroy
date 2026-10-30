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
  `world.path(x, y, z)` returns the walking distance and the gap between where the path ends and the point: a gap
  over a few yards means the point can't be reached.
- `units.nearby` gives each living creature `in_sight`, and for the nearest `path_distance` (the walk) and
  `reachable`; `distance` is a straight line. A mob in a mine below you can be 30 yards away and a 300-yard walk.
- Indoors and upstairs, pass the point's z to get the right floor: `move.to(x, y, world.ground_z(x, y, z))`.
  `act.go_to` without a z uses your own height for points within 80 yards, so it stays on your floor in a cave or a
  building; for anywhere else, pass the z (azworld gives spawn and quest points with theirs).
- Walking doesn't fight: `move.to`, `act.go_to` and `act.travel` keep walking while something attacks you, and drag
  it into your next fight. Give `act.go_to` and `act.travel` an `on_attacked = function(attackers) ... end` (your
  fighting) and they stop for whoever attacks you on the way, then carry on.
- Raw `move.to` requests have one owner per session. Other callers queue FIFO for at most 5 seconds, then
  fail with `movement_busy` and the current owner when present. A nearby destination from another task is a
  separate request. Replacing your own task's destination fails the old request with `replaced_by_owner`.
- `move.owner()` exposes the active task, call, request, destination, priority and phase; nil means idle.
  `me.movement()` also exposes the queue, and `wow tasks` / `task.list()` show each task's movement.
- `move.stop()` cancels the calling task's walk (`cancelled`); it refuses another caller's walk with
  `movement_busy`. `move.cancel(request_id)` explicitly cancels an active or queued walk, including another
  task's. An immediate `wow` evaluation has no persistent task identity: retain its request ID for cancellation.
- Pause, stop, failure and normal task return cancel that task's active and queued walks. Session close clears
  all ownership. Cancellation never counts as arrival. Start a task and await a walk if it must finish.
- Planning waits at most 15 seconds (`route_timeout`), and walking at most 600 seconds (`timeout`). There is
  one planning worker per session. Cancellation prevents a late plan from starting movement; interruption of
  the underlying native path query is still pending, so a slow query can delay subsequent planning.
- This is raw-request ownership. Priority is currently always `normal`; helper-wide leases, configurable
  queue waits, recovery preemption, and arbitration for face/turn/jump/unstuck are still pending. Helpers can
  currently lose ownership between raw walks or during combat callbacks. Teleport/route cancellation also
  needs the remaining route-controller work.
- `units.nearby` leaves out units nobody can select: the invisible triggers the server uses for events and scripts.
- Entering a place triggers it as in the game: exploration objectives complete, inns and cities make you rest.
- While the server moves you (a flight, a teleport, fear, confusion) the client sends no movement of its own:
  `move.to` is refused with the reason (`in_flight`, `feared`...), `me.movement()` says who moves you, and
  `movement_changed`, `flight_start` and `flight_end` events mark the changes.
- Teleports are `teleported` events (`far = true`: a different map, and the world around you reloads). Portals are
  objects (`type = "portal"`): `objects.use(portal)`. Boats and zeppelins aren't supported.
