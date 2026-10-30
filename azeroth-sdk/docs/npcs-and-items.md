# NPCs and items

Quest givers, vendors, trainers, objects, your bags and gear. The rest of the SDK is in sdk.md.

## NPCs

- NPCs have `roles` (`quest_giver`, `vendor`, `trainer`, `repair`, `flight_master`, `innkeeper`, `spirit_healer`...);
  quest givers have `quest_status` (the marker a client draws). `azworld near x y --kind <role>` finds the nearest.
- Raw NPC calls take `npc_guid`, the observed GUID string, and need you within ~5 yards.
  Waiting helpers take exactly one of `npc_entry` (a template ID) or `npc_guid`. `await(gossip.open(npc_guid))`
  returns `data.kind` (`gossip`, `quest`, `vendor`, `trainer`, or `flight`); dialogue contents are in `data.value`.
  `await(gossip.select(index))` picks an option and may also return `kind="closed"`, confirming dialogue closure
  only. No server response produces `timeout`, not success. An explicitly refused open produces `not_available`.
- Vendors: `await(vendor.items(npc_guid))` (cached or uncached; typed failure on refusal or timeout), `vendor.buy(npc, entry, count)`,
  `vendor.sell(npc, item)`, `vendor.sell_junk(npc)` (every grey), `vendor.repair(npc)`. `act.vendor{...}` walks
  there and performs only the operations requested: `act.vendor{npc_entry = 123, repair = true}`.
  Selling junk and repairing default to false. A failed step stops the errand; inspect `data.completed`,
  `data.remaining`, and `steps` before deciding whether to retry.
- Trainers: `await(trainer.list(npc_guid))` (what it teaches you, cost, level, available now), `trainer.learn(npc, spell)`. The
  `act.train{npc_entry = 123, spells = {133, 116}}` skips spells already known and stops on failure;
  `continue_on_error = true` attempts the rest but still reports failure. The level-70 "<Class> Trainer" NPCs in Northshire are placeholders that teach nothing.
- Quests: `quests.accept(npc, quest)`, `quests.turn_in(npc, quest [, reward index])` (0 = the first choice;
  `await(quests.info(id)).data` lists the choices on success), `quests.log()`, `quests.info(id)` (an awaitable lookup
  of the quest giver's text), `quests.abandon`.
  `act.accept` and `act.turn_in` walk there first. A kill objective also counts the creatures whose
  `kill_credit` lists its entry (`units.nearby`, `units.get`). A turn-in fails at once with `incomplete` when the objectives
  aren't done; a `quest_failed` event means an escort died or a timer ran out: abandon it and take it again.

## Items

- `items.list()` has each item's name, quality and vendor price once its info has loaded (a second after it first
  appears). `items.free_slots()`, `items.destroy(item)`, `items.use(entry)`, `items.equip(item)`.
- `await(items.compare(item))` waits for the item and equipped templates (five seconds) and shows the change against what you wear in that slot (item level, stats, armor, dps) and
  `usable = false` with the reason when you can't use it. `await(items.info(entry)).data` has the details on success; `me.gear()` every
  slot; `items.broken()` what's at 0 durability.
- Group loot: a `loot_roll_start` event per item up for rolls; `items.roll(roll, "need"|"greed"|"disenchant"|"pass")`
  before the countdown ends (`items.rolls()` lists the open ones), then `loot_roll` and `loot_roll_won` events
  (`loot_roll_all_passed` when nobody wants it).
- Full bags: loot stays on the corpse and turn-ins with an item reward fail with `bags_full` (and an "Inventory is
  full" notice).
- `act.loot_nearby{dist = 8}` returns confirmed items and money, including partial work on failure. It destroys
  nothing unless `make_room = true`; that allows one attempt to free space and retry per corpse.
  `act.make_room()` protects possible usable gear upgrades and items with missing information, and waits for
  the selected item's destruction to be confirmed. `act.consume("Food")` and `act.consume("Drink")` also wait
  for confirmation; check their `ActResult` before assuming consumption started.
- `act.rest{hp = 0.95, mana = 0.9, max_s = 40}` requires both thresholds. Missing consumables allow passive
  regeneration, but an item-use failure propagates. Expiry returns `rest_timeout` with achieved fractions.
- `objects.use(guid)` within ~5 yards uses a chest, node or quest object. `objects.nearby` says what each takes
  (`gather`, `skill`, `can_use`, `key`), `lit` when one of your quests needs it, and how far the walk is.
- `act.use_object{entry = object_entry, quest_id = quest_id}` verifies progress or completion in your quest log.
  An absent or disappearing quest fails. Without `quest_id`, it verifies `count` successful uses instead.
  The defaults are three attempts and a 60-second deadline (`max_attempts`, `timeout_s`); failure preserves
  confirmed use count and attempted use count. Increasing `count` above three requires increasing `max_attempts`.

## Professions

`skills.list()` and `skills.get(skill_id)` expose observed base ranks and caps; metadata availability is
explicit. Gather with `objects.use(guid)` and cast known recipes with `spells.cast(recipe_id)`. Both await
server cast completion; inspect inventory and skill changes separately. Object-use success does not itself
prove quest progress. A non-casting object requires an observed object state change, otherwise it times out.
