# Other players

Groups, chat, trades and the players around you. The rest of the SDK is in sdk.md.

## Other players

- Sending: `chat.say(msg)`, `chat.yell(msg)`, `chat.whisper(name, msg)`, `chat.party(msg)`,
  `chat.channel(name, msg)`. Every message is at most 255 bytes (`message_too_long`). Whispers and channel
  messages resolve once the server has delivered them, or fail (`player_not_found`, `not_member`, `muted`,
  `throttled`). The server echoes your own say, yell, party, channel and emote messages back as `chat` events
  with `self = true`, and a whisper you send as kind `whisper_sent` (`from` is who you whispered).
- Channels: you start in your zone's General channel, and leave it when you change zone until you
  `chat.join("General - <Zone>", 1)` (zone channel ids: General 1, Trade 2, LocalDefense 22). `chat.join("name", 0)`
  joins a custom channel, in every zone (`wrong_password`, `banned`).
- Receiving: every message is a `chat` event with `kind`, `from` and `text` (`ago_s` in `wow events` says how old).
  `events.on("chat", fn)` reacts in a script. Messages sent while your session restarts are lost.
- Groups: `group.invite(name)` (fails with `bad_player_name`, `already_in_group`, `group_full`...), the invitee gets
  a `party_invite` event and calls `group.accept()`; `group.info()`, `group.leave()`. A `ready_check` event:
  answer with `group.ready(true)`; its result is `unconfirmed` because the protocol provides no matching acknowledgement. A `summon_request` event: `summon.accept()` resolves when you arrive, or `summon.decline()`.
  `group.report([minutes])`: how each member spent the last minutes (fighting, travelling, idle, dead, with the
  group), from what you've seen of them. The leader can hand the lead to a member (`group.promote(guid)`) and set
  the loot rules (`group.set_loot(method [, threshold, master])`; game.md, "Group loot"); `group.info()` shows
  `loot_method`, `loot_threshold` and `master_looter`. A member out of sight has no `z` in `group.info()`.
- The server's message of the day comes as a `motd` event; battleground and arena queue changes as `battleground`.
- `units.players(dist)`, `units.inspect(guid)`: who's around, their level, class and gear.
- Trading: `trade.start(name)`, both sides `trade.offer{items = {...}, money = copper}`, both `trade.accept()`.
  A refused trade fails the call with the reason (`too_far`, `busy`, `not_enough_money`...), also in `Trade.error`.
  The other side opens with `trade.open()` on a `trade` event with state `requested`; every change to the other
  side's offer is a `trade` event too. The server never confirms your own offer, only shows it to the other side:
  `trade.offer` is ok half a second after sending unless refused (`untradeable`, `not_enough_money`), and
  `trade.status().mine` is what you offered. Any change to either offer un-accepts both: a pending `trade.accept`
  then fails `unaccepted`, so look at the trade and accept again. Accept after the offers settle.

## Duels

- `duel.challenge(name)` challenges a player within 10 yards (the Duel spell): ok once the server has sent the
  challenge (`player_not_found`, `busy`, or the cast's refusal such as `out_of_range` or `no_dueling` where duels
  aren't allowed). The other player gets a `duel_requested` event and answers with `duel.accept()` or
  `duel.decline()`; you can withdraw your own challenge with `duel.decline()`.
- Accepting starts a 3-second countdown (`duel_countdown`); then the duel is under way. The server plants a flag
  between the two players where the challenge was made.
- A duel ends when one player is brought to 1 health (they don't die) or yields with `duel.yield()`: result `beaten`.
  A player more than 50 yards from the flag gets `duel_out_of_bounds` and loses if still out after 10 seconds: result
  `fled`. A duel broken off before it is decided (declined, withdrawn) ends `cancelled`.
- `duel_ended` says who won, who lost and how (`winner`, `loser`, `result`, `won` is true if you did).
  `duel.state()` is the duel now: `none`, `requested`, `countdown`, `fighting` or `ended`, with the opponent.
- Nothing is restored when a duel ends: the loser is left at 1 health, the winner with whatever health it had, and
  both with the mana and cooldowns the duel left them. They regenerate as usual (game.md: out of combat, food and
  drink), and the next duel starts from wherever they are.
- Only the two duellers can affect a duel: nobody else can attack, heal or buff either of them while it lasts.
