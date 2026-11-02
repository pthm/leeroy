# Casting and fighting

Spells, attacks and fights, and looking back at a fight. The rest of the SDK is in sdk.md.

## Casting and fighting

- `me.casting()` is set from the moment you call `spells.cast` (or `items.use`) until the cast ends. Moving
  interrupts a cast.
- `spells.check(id, guid)` returns readiness evidence: cooldown, power needed, observed target, nominal range,
  line of sight, facing, and `unknown` checks. `usable` means no known local refusal; server acceptance is
  separate. Nominal DBC range excludes talents/hitboxes, so `nominal_range_exceeded` is advisory.
  `spells.usable` is the boolean convenience view; use `act.within_nominal_range` only for that estimate.
- Facing: the server requires the target within 90 degrees either side of where you face only for spells flagged
  for it in Spell.dbc (melee attacks, bolts, Smite). Heals, buffs and Taunt reach a target behind you.
  `spells.check`'s `facing` says where the target is; its `unit_not_infront` refusal comes only for flagged spells.
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
- `units.engaged()` lists the creatures in combat with you, your pet or your party: each one with one of you on its
  threat list (`on_threat`, highest first) or as its target, fleeing or targetless ones included. A unit's `fleeing`
  is the server's fleeing flag. A unit's `control` lists the auras that take control from it (stun, polymorph,
  fear, root, silence...): the spell, its `mechanic`, `caster_guid`, `remaining_ms`, whether damage breaks it
  (`breaks_on_damage`) and the damage it takes to break (`damage_limit`). The rules behind them are in game.md,
  "Mobs and combat".
- `spells.stop_casting()` stops the cast or channel in progress (`not_casting`, or `cast_finished` when it went off
  first); `spells.stop_auto_repeat()` stops a wand's Shoot or Auto Shot.
- Throw, Shoot and a wand's shots have the ranged attack time (your ranged weapon's speed) as their cooldown, in
  `spells.cooldown` and `spells.check`; Auto Shot doesn't.
- Every fight ends with a `fight_end` event: duration, damage dealt and taken, abilities used, outcomes. A unit's
  `casting`, a `unit_cast_start` event, `units.auras`, `threat_pct` and `combat.threat(guid)` show what the game
  shows a player.
- The combat log: `damage` and `heal` events (`periodic` for DoT and HoT ticks), `energize` (mana, rage, energy
  given), `dispel` (auras dispelled or stolen). A `cast_done` event says how many targets it `hits` and which it
  `misses`, and why.

## Your pet

- `pet.info()` is nil without a pet out; otherwise its health, power (focus for a hunter's pet, else mana),
  `happiness` (a hunter's pet), `react`, `command`, `target` and `spells`: its abilities, each `passive` (always on)
  or castable, and whether it uses it by itself (`autocast`). The server sends the abilities, stance and command
  when the pet comes out; it doesn't confirm a command or stance change, so `react` and `command` are the last ones
  sent, as in the game's own client.
- Commands: `pet.follow()` (it comes back and stops fighting), `pet.stay()`, `pet.attack(guid)`. Stances,
  `pet.react(stance)`: `passive` (it never attacks on its own), `defensive` (it attacks what attacks you or it),
  `aggressive` (it attacks hostiles in range).
- `pet.cast(id, guid)` uses an ability on a unit (its own target without one): a `pet_cast_done` event when it goes
  off, `pet_cast_failed` with the server's reason (`not_ready`, `out_of_range`, ...), which the request returns too.
  `pet.autocast(id, on)` sets whether it uses an ability by itself.
- A hunter's pet's happiness falls over time. Below content it does less damage, and at 0 it leaves for good.
  `pet.feed(item)` casts Feed Pet on food you carry: each family eats some kinds of food, and food far below the
  pet's level is refused (`food_lowlevel`).
- Summoning, dismissing and reviving are the class's spells (Call Pet, Dismiss Pet, Revive Pet; a warlock's
  Summon Imp...): `spells.cast`.

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
