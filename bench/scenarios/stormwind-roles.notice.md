SINCE YOUR LAST RUN
- The SDK's API was rebuilt. bot.* is gone: functions are grouped by what they do (me, units, objects, world, move,
  combat, spells, items, vendor, trainer, quests, death, chat, group, trade, talents, flights), every value has
  the same shape everywhere, and every action reports a result. Scripts run as tasks that can wait, and
  everything that happens is in an event log you can read and wait on. Scripts in your lib/ that use bot.* no
  longer work: keep the strategy (and NOTES.md), rewrite the code. Start with AGENTS.md ("The runtime", "The Lua
  API" and its reference) and help() in the game (wow 'return help("me")').
- You now have a class specialisation and a role (above). Your talents have been reset: spend your points for
  your spec (talents.list, talents.learn).
- Everyone starts in Stormwind's Trade District: trainers, vendors, the bank and the flight master (flights:
  flights.map, flights.take, act.fly) are in the city.
