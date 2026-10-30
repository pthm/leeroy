# When something doesn't work

Common failures and what they mean, and the raw protocol for what the API doesn't cover. The rest of the SDK is in
sdk.md.

## When something doesn't work

| You see | It usually means | Check |
|---|---|---|
| `move.to` with `no_path`, or ending `stuck` | no walkable route there (inside a building, wrong floor, under the map) | `world.path`, the point's z, `azworld route` |
| distance to a target not dropping | blocked by terrain or out of reach | `path_distance`, `reachable`, `in_sight` |
| cast `line_of_sight` | a wall, rock or rise in between (in caves, often the tunnel's bend) | `units.in_sight(guid)`; `act.approach` closes in until it's in sight |
| `out_of_range` | too far for that spell | `spells.info(id).range` |
| `bad_targets` | not a valid target (dead, friendly, tapped by someone else) | the unit's `alive`, `reaction`, `tapped_by_other` |
| `equipped_item_class*` | your weapon is broken or missing | `items.broken()` |
| `not_ready` / `no_power` | cooldown, or not enough mana, rage or energy | `spells.cooldown(id)`, `items.cooldown(item)`, `spells.usable(id, guid)` |
| `caster_aurastate` / `target_aurastate` | the spell's condition isn't met (Victory Rush without a kill, Execute above 20%) | `me.states()`, the target's `states`, `spells.usable` |
| `unit_not_infront` | the target isn't in front of you | `move.face(guid)` before the cast (`act.cast` faces for you) |
| casting while dead (`caster_dead`) | a script still running after you died | `me.alive()`, or wait in your loop while dead |
| `interrupted` | you moved or were hit mid-cast | `me.casting()` |
| `miss`, `resist`, `immune`, `evade`, `reflect`, `dodge`, `parry`... | the spell went off but didn't land: power and cooldown are spent, so a retry may hit `not_ready` | `spells.cooldown(id)`; `immune`/`evade`: try another spell or target |
| `bags_full`, loot left on corpses | no bag space | `items.free_slots()` |
| `turn_in` times out | the quest isn't complete, or a different NPC takes it | `quests.log()`, `azworld quest <id>` |
| accepting does nothing | level too low, a prerequisite missing, or already done | `azworld quest <id>` (min level, chain) |
| a mob takes no damage | evading, or tagged by someone else | `evading`, `tapped_by_other` |

## The raw protocol

The client doesn't implement mail or the auction house. `client.watch(opcode)` captures a server opcode,
`client.packets()` drains what was captured as `{opcode, hex, time_ms}`, `client.send(opcode, "hex body")` sends a
client packet (header and encryption handled), and `client.call("Method", ...)` calls any exported method of the
underlying `WorldClient` (`client/world.go`). Opcode numbers are in the client's `opcodes.go` and the server's
`Server/Protocol/Opcodes.h`; packet layouts in the server's `Handlers/*.cpp`; enums in `shared/SharedDefines.h`.
For example the mailbox: `CMSG_GET_MAIL_LIST` (0x23A, body = mailbox guid as u64 LE), reply
`SMSG_MAIL_LIST_RESULT` (0x23B). All numbers are little-endian.

`chat.command(".cmd")` sends a server command; GM commands only work on GM accounts, and scored runs don't use one.
