# E2E: SDK and harness fixes from the models-frost run

You are testing a World of Warcraft automation SDK end to end. A GM test character, **Devmage** (a level-1 Human
Mage on a GM account), is logged in on a local AzerothCore server, and you drive it with the `wow` command
(`WOW_PORT` is already set). Read `sdk.md` and `api-reference.md` in `../../azeroth-sdk/docs/` if you need the API.

- `wow '<lua>'` evaluates Lua in the session and prints the result as JSON. Anything that waits (`act.*`, `await`,
  `wait`) has to run in a task: `wow 'task.spawn("t", function() R = act.go_to{x = 1, y = 2} end)'`, then read
  `R` with `wow 'return R'` after `wow wait 5` or `sleep 5`.
- GM commands go through chat: `wow 'chat.command(".gm on")'`. Useful ones: `.gm on|off` (mobs ignore you or not),
  `.cheat god on|off` (can't die or can), `.go xyz <x> <y> <z> 0` (teleport), `.revive`, `.die`, `.levelup <n>`.
  The character starts with GM mode and god mode on. With GM mode on, every creature reads `reaction = "neutral"`
  (they ignore you); turn it off before anything that looks for hostiles.
- `azworld` queries the world database (`azworld quest 62`, `azworld route ...`).

Run each check below in order, recording what you observed. Don't fix anything; if a step doesn't work as
described, note exactly what happened (the command and its output) and move on. Put the character back to a safe
state between checks (`.revive`, `.gm on`, `.cheat god on`) if a check left it dead or in a fight.

## Checks

1. **A missing argument is an error.** `wow 'return spells.known()'` fails with a message naming
   `spells.known(id)` and saying it needs 1 argument. `wow 'return spells.known(133)'` is `true` (Fireball).
2. **The level-1 spellbook.** `wow 'return me.spellbook()'` reports `known` of at least 2 (Fireball and Frost
   Armor), and its `missing` list doesn't contain "Chilled".
3. **Spell level and cost.** `wow 'return spells.info(133)'` has `level` 1 and a `cost` above 0 (a share of base
   mana); `spells.info(116)` (Frostbolt) has `level` 4.
4. **A misspelled function is explained.** `wow 'return units.nearbyy()'` fails with a message that says the name
   called isn't a function.
5. **events.off by name.** Register `events.on("chat", function() end, "probe")`, check `events.handlers()` lists
   it, call `events.off("probe")`, and check it's gone.
6. **wow tasks.** Spawn a task that finishes at once (`task.spawn("quick", function() end)`) and one that fails
   (`task.spawn("bad", function() error("boom") end)`). `wow tasks` lists the failed one (with its error) but not the
   finished one; `wow tasks --all` lists both.
7. **Options tables.** In a task, `pcall(act.loot_nearby, 10)` returns false with an error saying act.loot_nearby
   takes an options table.
8. **NPCs by entry, and the wrong NPC.** Teleport to Northshire Abbey: `.go xyz -8913 -137 82 0`. Then:
   - `await(quests.accept(823, 783))` in a task, passing Deputy Willem's **entry** 823 (he starts quest 783: walk
     to him first with `act.reach_npc{npc = 823}`), succeeds: the quest is in `quests.log()`.
   - `await(trainer.list(197))` fails at once with `wrong_npc` saying he isn't a trainer.
   - `await(vendor.repair(197))` fails at once with `wrong_npc`.
   - `act.vendor{npc = 197}` returns `wrong_npc`.
   - `await(trainer.list(198))` (Khelden Bremen, the mage trainer, inside the abbey: walk to him with
     `act.reach_npc{npc = 198}` first) succeeds.
   - `await(trainer.learn(198, 116))` (Frostbolt at level 1) fails with `level_too_low`, saying it needs level 4.
9. **Facing before a cast.** Find a living hostile creature within 30 yd (`units.nearby(40)`; the kobolds north
   of the abbey, around -8830 -110 84, if none is near). Turn your back to it (`move.turn(<your facing + 3.14>)`,
   facing is `me.position().o`), then in a task `R = act.cast(133, <its guid>)`. `R.ok` is true.
10. **Walking with on_attacked.** Go to Fargodeep Mine's entrance with GM on: `.go xyz -9771 279 43 0`. Turn GM and
    god mode off (`.gm off`, `.cheat god off`). In a task, walk 80 yd away through the kobolds with
    `act.go_to{x = -9700, y = 330, on_attacked = function(foes) ATTACKED = foes[1].name; chat.command(".gm on");
    chat.command(".cheat god on"); wait(3) end}`. `ATTACKED` names a creature, and the walk finishes ok (`R.ok`).
11. **Coming back to life away from the killer.** Back at `.go xyz -9771 279 43 0`, with GM and god mode off, stand
    still until the kobolds kill you (`me.alive()` false; `.die` if nothing attacks within 60 s). Note the
    corpse position (`death.corpse()`) and the hostile creatures near it (`act.danger(x, y, 30)`). Then in a task
    `R = act.recover{rest = false}`. When `R` is set: `R.ok` is true, you're alive, the session's log (`/tmp/devmage.log` for
    the GM character) has a line "hostiles at the corpse, reclaiming from",
    and you came back on the side of the corpse away from where the hostiles were. Restore GM and god mode at once.
12. **reclaim_in.** While dead in check 11, `death.corpse().reclaim_in` was never 0 while `death.reclaim()` still
    answered "not_yet" (if you saw both, note it).
13. **azworld.** `time azworld route -9475 -285 -9120 -590 --level 5` finishes in under 15 s with waypoints.
    `azworld quest 62` lists an objective of kind `explore` with x, y, z and a radius.

## Report

End with a table: check number, PASS or FAIL, and one line of evidence (the key output). Then list anything else
that looked wrong.
