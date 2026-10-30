# Other players

Groups, chat, trades and the players around you. The rest of the SDK is in sdk.md.

## Other players

- Sending: `chat.say(msg)`, `chat.yell(msg)`, `chat.whisper(name, msg)`, `chat.party(msg)`,
  `chat.channel(name, msg)`. Every message is at most 255 bytes (`message_too_long`). Whispers and channel
  messages resolve once the server has delivered them, or fail (`player_not_found`, `not_member`, `muted`,
  `throttled`); your own messages aren't echoed back as chat events.
- Channels: you start in your zone's General channel, and leave it when you change zone until you
  `chat.join("General - <Zone>", 1)` (zone channel ids: General 1, Trade 2, LocalDefense 22). `chat.join("name", 0)`
  joins a custom channel, in every zone (`wrong_password`, `banned`).
- Receiving: every message is a `chat` event with `kind`, `from` and `text` (`ago_s` in `wow events` says how old).
  `events.on("chat", fn)` reacts in a script. Messages sent while your session restarts are lost.
- Groups: `group.invite(name)` (fails with `bad_player_name`, `already_in_group`, `group_full`...), the invitee gets
  a `party_invite` event and calls `group.accept()`; `group.info()`, `group.leave()`. A `ready_check` event:
  answer with `group.ready(true)`. A `summon_request` event: `summon.accept()` resolves when you arrive, or `summon.decline()`.
  `group.report([minutes])`: how each member spent the last minutes (fighting, travelling, idle, dead, with the
  group), from what you've seen of them.
- The server's message of the day comes as a `motd` event; battleground and arena queue changes as `battleground`.
- `units.players(dist)`, `units.inspect(guid)`: who's around, their level, class and gear.
- Trading: `trade.start(name)`, both sides `trade.offer{items = {...}, money = copper}`, both `trade.accept()`.
  A refused trade fails the call with the reason (`too_far`, `busy`, `not_enough_money`...), also in `Trade.error`.
