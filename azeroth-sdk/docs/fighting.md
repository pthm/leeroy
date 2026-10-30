# Casting and fighting

Spells, attacks and fights, and looking back at a fight. The rest of the SDK is in sdk.md.

## Casting and fighting

- `me.casting()` is set from the moment you call `spells.cast` (or `items.use`) until the cast ends. Moving
  interrupts a cast.
- `spells.check(id, guid)` returns readiness evidence: cooldown, power needed, observed target, nominal range,
  line of sight, facing, and `unknown` checks. `usable` means no known local refusal; server acceptance is
  separate. Nominal DBC range excludes talents/hitboxes, so `nominal_range_exceeded` is advisory.
  `spells.usable` is the boolean convenience view; use `act.within_nominal_range` only for that estimate.
- `act.cast(id, guid, options)` holds one normal-priority movement lease, stops only owned movement, faces
  the target, and waits for the server. It performs at most two casts, retrying only a facing refusal.
  `data.check`, `data.attempts`, `data.corrections`, and `data.action` preserve what happened.
  It never stops a competing owner (`movement_busy`). The default deadline is 30 seconds, at most 60.
- Explicit `options.approach=true` permits one approach along a verified path. `max_move_yards=10` (at most
  40) bounds the measured path length; it is not just straight-line displacement. Without this option,
  the helper does not move toward the target. One shared deadline covers planning, movement, facing, and casting.
  `cast_timeout` means no matching result before that deadline; an already sent server cast may complete later.
- `spells.cooldown(id)` and `items.cooldown(item)` say how long until a spell or item is ready, including
  cooldowns still running from before you logged in (the Hearthstone) and shared ones (all potions). A potion used
  in combat reads `-1`: its cooldown only starts when you leave combat.
- `cast_failed` events carry the server's reason. A cast bar that ends without the spell and without one is
  `cancelled`. A failed cast's code is in its request's result too, so check `r.ok` and branch on `r.error.code`:
  retrying the same call unchanged gets the same answer.
- `combat.attack(guid)` starts auto-attack. A refused swing is an `attack_failed` event (`not_in_range`,
  `bad_facing`: `move.face`).
- Each spell rank is its own id: `spells.known(id)` tests one rank; `spells.find(name)` returns the highest rank you know.
- Every fight ends with a `fight_end` event: duration, damage dealt and taken, abilities used, outcomes. A unit's
  `casting`, a `unit_cast_start` event, `units.auras`, `threat_pct` and `combat.threat(guid)` show what the game
  shows a player.
- The combat log: `damage` and `heal` events (`periodic` for DoT and HoT ticks), `energize` (mana, rage, energy
  given), `dispel` (auras dispelled or stolen). A `cast_done` event says how many targets it `hits` and which it
  `misses`, and why.

## Looking back at a fight

The session keeps a fight log: where you, your party and the enemies were every second, and every hit, heal, cast,
kill and death. Each fight is also a file in `fights/` in your workspace, so the log outlasts a restart.

- `wow fights` lists your last fights: when, how long, the outcome, the enemies, damage dealt and taken, and how many
  joined after the first (adds).
- `wow fight <n>` (or `wow fight last`) shows one: who joined when and from where (and whether it came with an enemy
  already in the fight: its pack), who was out of heal range of you and for how long, where you died; a map of
  everyone's movement through the fight (each one's trail in its lowercase letter, where it ended in its capital,
  `@` you); small maps of the key moments (the pull, each add, your health past half and a fifth, deaths); and the
  timeline. `--map` shows only the maps; `--at <s>` the map at that second.
- `combat.fights()` and `combat.fight(id)` give the same as data; `fight_end` carries the fight's `id` and its
  `positioning`.
