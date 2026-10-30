# Death and travel

Flying, your hearthstone, and coming back from death. The rest of the SDK is in sdk.md.

## Travel and death

- `flights.map(npc)` at a flight master discovers its point (`learned`) and lists where you can fly;
  `flights.take(npc, node)` resolves when you land. `act.fly{to = "Sentinel Hill"}` from near a flight master.
- `innkeeper.bind(npc)` sets your home; the hearthstone is item 6948 (`items.use(6948)`); `me.home()`.
- `death.release()`, `death.corpse()` (where it is, which way and how far from you, and when you can reclaim), `death.reclaim()` within ~30 yards,
  `death.spirit_res(npc)`. `act.recover()` does release, corpse run (spirit healer if that fails) and rest. A
  `gear_broken` event follows a revive with broken gear.
- Coming back to life where you died puts you next to what killed you. `act.corpse_run` and `act.recover` reclaim
  from the far side of the reclaim circle (32 yards out) when hostiles stand by the corpse, and `act.recover{spirit =
  true}` runs to the corpse instead when hostiles camp the graveyard (the spirit healer revives you where you stand).
  `act.danger(x, y [, r])` lists the hostile creatures near any point. The wait before you can reclaim grows with
  deaths in quick succession.
- A `travel_refused` event: a portal or instance turned you away (`reason`, `message`: level, group, full, lockout).
- `me.reputation()`: your standing with each faction (`rank`: hated ... exalted).
