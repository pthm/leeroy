# Death and travel

Flying, your hearthstone, and coming back from death. The rest of the SDK is in sdk.md.

## Travel and death

- `flights.map(npc)` at a flight master discovers its point (`learned`) and lists where you can fly;
  `flights.take(npc, node)` resolves when you land. `act.fly{to = "Sentinel Hill"}` from near a flight master.
- `innkeeper.bind(npc)` sets your home; the hearthstone is item 6948 (`items.use(6948)`); `me.home()`.
- `death.release()`, `death.corpse()`, `death.reclaim()`, and `death.spirit_res(npc_guid)` return raw requests.
  `await(death.corpse())` waits at most five seconds; `not_found` means alive or explicit server absence,
  `not_available` means missing character/connection evidence, and silence is `timeout`. A confirmed corpse
  snapshot is cached for three seconds. `act.recover()` coordinates release, corpse recovery, gear checks, and optional rest. It uses
  recovery movement priority. Concurrent callers join the first caller's policy and result; a joining caller
  cannot enable healer fallback or enlarge the original budget.
- Recovery defaults to `timeout_s=180` and `max_attempts=2` across both resurrection methods. Timeout must be
  greater than zero and at most 600 seconds; the attempt limit is 1 or 2 and includes immediate refusals such
  as `not_yet`. Reclaim timers are re-read between waits. Failed release, movement, resurrection, and rest
  steps propagate with partial evidence. Recovery never restarts a farming loop or repeats a death cycle.
- Spirit-healer use requires `act.recover{spirit=true}` or `allow_spirit_fallback=true`. Calling
  `act.spirit_res{...}` directly also authorizes that cost. Fallback reserves a second attempt when possible;
  it cannot exceed the shared attempt limit. A hostile or unverified healer site is refused even with
  permission. Available healer metadata includes `durability_loss=true` and `cost_known=false`: the exact
  server-configured penalty is not exposed.
- Corpse recovery checks path endpoint gap, vertical tolerance, reclaim range, line of sight, and the server's
  timer. If the corpse site is hostile, it considers observed clear points 32 yards away. A failed walk to
  such a point terminates recovery. `safe=false` skips the corpse-site hostile check; it does not bypass
  floor/range/LOS checks or the healer-site refusal.
- Safety evidence is conservative and local. Missing floor or collision information is a refusal, and
  nearby hostiles on uncertain floors are not discarded as harmless. Recovery uses the same bounded
  `act.danger{x,y,z,radius}` assessment as travel; inspect its state, not just the hostiles list.
- The deadline bounds scheduled waits and retries. Recovery awaits `world.path`; its own expiry also cancels
  the pending route and underlying navmesh search. Route checks independently time out after 15 seconds.
  Cancellation is cooperative between tile reads, lock attempts, polygon-search batches and smoothing
  steps; an in-progress local kernel read or bounded Detour operation must finish first. Floor/LOS helpers
  remain synchronous with a cooperative 60 ms spatial budget; expiry is unknown. A request already sent to the server cannot be recalled; late resurrection is handled
  as observed state on a subsequent call, without turning the expired call into success.
- `act.recover`, `act.corpse_run`, and `act.spirit_res` return `ActResult<Recovery>`. Terminal `data.state` is
  `done`, `attention`, or `needs_repair`; the latter two have `ok=false`. `data.attempts`, `steps`, `failed_step`,
  and `last_error` describe partial work when available. Lifecycle cancellation may only provide the terminal
  state and last observed attempt count. Empty optional collections are omitted.
- After resurrection, helpers inspect health, power, money, and gear. A broken main hand returns
  `needs_repair` with up to 20 observed repair NPCs; it does not choose a destination. `act.recover` rests only
  at a locally clear site and rechecks gear afterward. The standalone corpse/healer helpers do not rest.
  `rest=false` still performs gear checks, including when the character was already alive.
- `recovery_transition` and `gear_state` diagnostics are saved beside action records and retained in reports
  with source references and task IDs. Pause, stop, failure, timeout, and session close terminate the shared
  attempt. A resumed or late old callback cannot complete a newer attempt. Reports count resurrection
  attempts and sampled dead/broken-weapon durations. Pauses, missing events, unavailable samples and
  session boundaries are excluded; these are observed intervals, not extrapolated wall time.
- A `travel_refused` event: a portal or instance turned you away (`reason`, `message`: level, group, full, lockout).
- `me.reputation()`: your standing with each faction (`rank`: hated ... exalted).
