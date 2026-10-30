package session

// Typed events and action requests (docs/sdk.md). Everything here is decoded from what a stock AzerothCore
// server sends the client (SMSG opcodes on the world connection): no server-side modules or logs. Event data
// are the structs in package types (EventTypes lists them).

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"fmt"
	"github.com/pthm/leeroy/azeroth-sdk/metrics"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

const (
	smsgSpellHealLog       = 0x150
	smsgLootMoneyNotify    = 0x163
	smsgItemPushResult     = 0x166
	smsgQuestInvalid       = 0x18F
	smsgQuestComplete      = 0x191
	smsgQuestFailed        = 0x192
	smsgQuestLogFull       = 0x195
	smsgQuestFailedTimer   = 0x197
	smsgQuestUpdateDone    = 0x198
	smsgQuestUpdateAddKill = 0x199
	smsgLogXPGain          = 0x1D0
	smsgInitWorldStates    = 0x2C2
	smsgCreatureQueryResp  = 0x061
	smsgAttackStart        = 0x143
	smsgSpellLogMiss       = 0x24B
	smsgSpellFailedOther   = 0x2A6
	smsgUpdateComboPoints  = 0x39D
	smsgResurrectRequest   = 0x15B
	smsgStartMirrorTimer   = 0x1D9
	smsgPauseMirrorTimer   = 0x1DA
	smsgStopMirrorTimer    = 0x1DB
	smsgEnvironmentalDmg   = 0x1FC
)

var envCauses = map[uint8]string{0: "fatigue", 1: "drowning", 2: "fall", 3: "lava", 4: "slime", 5: "fire", 6: "fall"}

// creature names by entry, from SMSG_CREATURE_QUERY_RESPONSE (the client asks once per entry)
var creatures = struct {
	sync.Mutex
	name  map[uint32]string
	info  map[uint32]creatureInfo
	asked map[uint32]time.Time
}{name: map[uint32]string{}, info: map[uint32]creatureInfo{}, asked: map[uint32]time.Time{}}

// creatureInfo: the rest of SMSG_CREATURE_QUERY_RESPONSE
type creatureInfo struct {
	subname    string
	typ, rank  uint32
	killCredit []uint32
}

// readCreatureQuery: SMSG_CREATURE_QUERY_RESPONSE as WorldSession::HandleCreatureQueryOpcode writes it:
// entry (high bit: unknown), names x4, subname, icon, type flags, type, family, rank, kill credit x2, ...
func readCreatureQuery(r *bytes.Reader) (entry uint32, n string, ci creatureInfo, ok bool) {
	u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
	if entry = u32(); entry&0x80000000 != 0 {
		return entry, "", ci, false
	}
	n = readCString(r)
	readCString(r)
	readCString(r)
	readCString(r)
	ci.subname = readCString(r)
	readCString(r)
	u32()
	ci.typ = u32()
	u32()
	ci.rank = u32()
	for range 2 {
		if k := u32(); k != 0 {
			ci.killCredit = append(ci.killCredit, k)
		}
	}
	return entry, n, ci, true
}

// objName: an object's name, asking the server for creature names it hasn't told us yet ("" until it answers)
func (b *Bot) objName(o *client.WorldObject) string {
	if o.Name != "" || o.IsPlayer || o.Entry == 0 {
		return o.Name
	}
	creatures.Lock()
	defer creatures.Unlock()
	if n, ok := creatures.name[o.Entry]; ok {
		return n
	}
	if time.Since(creatures.asked[o.Entry]) > 5*time.Second {
		creatures.asked[o.Entry] = time.Now()
		go b.world.CreatureQuery(o.Entry, o.GUID)
	}
	return ""
}

// emit records an event on the character's stream (no-op before the engine exists). data is a types.* struct.
func (b *Bot) emit(typ string, data any) {
	if b.lua != nil {
		b.lua.Emit(typ, data)
	}
}

// request starts an action request: check is polled each tick, match sees each event; either returns nil to wait.
func (b *Bot) request(op string, args map[string]any, timeout time.Duration, check func() *script.Outcome,
	match func(script.Event) *script.Outcome) map[string]any {
	return b.lua.Reqs.Start(script.Spec{Op: op, Args: args, Timeout: timeout, Check: check, Match: match, Hint: timeoutHints[op]})
}

// refuse resolves a request at once as failed (the action was never sent); done as ok.
func (b *Bot) refuse(op string, args map[string]any, code, msg string) map[string]any {
	return b.lua.Reqs.Now(script.Spec{Op: op, Args: args}, script.Fail(code, msg, nil))
}

// noNPC validates an observed NPC GUID. Template entries belong in act's npc_entry selector.
func (b *Bot) noNPC(op string, args map[string]any, npc *uint64) map[string]any {
	if b.world.GetObject(*npc) != nil {
		return nil
	}
	return b.refuse(op, args, "npc_not_found", "no such NPC in sight (out of range, or gone); use act with npc_entry to find a template")
}

// npc roles (UNIT_NPC_FLAGS) an action needs: a trainer that isn't one, or a vendor that isn't, says nothing at all
const (
	roleTrainer = 0x10
	roleVendor  = 0xF80 // vendor, or an ammo, food, poison or reagent vendor
	roleRepair  = 0x1000
)

// noRole refuses an action at an NPC (in sight: after noNPC) without the role it needs, naming the NPC.
func (b *Bot) noRole(op string, args map[string]any, npc uint64, role uint32, what string) map[string]any {
	o := b.world.GetObject(npc)
	if o == nil || o.Value(client.UnitNPCFlags)&role != 0 {
		return nil
	}
	return b.refuse(op, args, "wrong_npc", fmt.Sprintf("%s isn't %s (act.npc(entry).roles says what an NPC does)", cmp.Or(b.objName(o), "that NPC"), what))
}

func (b *Bot) done(op string, args map[string]any, data any) map[string]any {
	return b.lua.Reqs.Now(script.Spec{Op: op, Args: args}, script.Ok(data))
}

// sent never equates transport success with a game postcondition. Callers with
// observable state use request/check or a server event instead.
func (b *Bot) sent(op string, args map[string]any, err error) map[string]any {
	if err != nil {
		return b.refuse(op, args, "failed", err.Error())
	}
	return b.refuse(op, args, "unconfirmed", "packet was sent, but this operation has no observed completion; inspect state before retrying")
}

// timeoutHints: the usual reason the server stayed silent
var timeoutHints = map[string]string{
	"quests.accept":    "the quest giver didn't offer it: level, prerequisites, already done, or a different NPC",
	"quests.turn_in":   "the server ignores a turn-in when the quest isn't in your log or you aren't next to the NPC that takes it",
	"combat.attack":    "no swing started: out of range or not facing it (move.face)",
	"trainer.learn":    "stand within ~5 yd of the trainer",
	"trainer.list":     "stand within ~5 yd of the trainer, alive (it won't talk to ghosts), with a trainer for your class",
	"gossip.open":      "stand within ~5 yd of the NPC; dead, only spirit healers talk to you",
	"vendor.buy":       "stand within ~5 yd of the vendor",
	"vendor.sell":      "stand within ~5 yd of the vendor",
	"objects.use":      "stand within ~5 yd of the object",
	"spells.cast":      "the cast never started: check range, facing and spells.usable",
	"talents.learn":    "check talents.list(): free points, tier (5 points per row) and prerequisites",
	"group.invite":     "no answer from the server",
	"chat.whisper":     "no echo from the server: a notice may say why (level requirement, silenced)",
	"chat.channel":     "no echo from the server: a notice may say why (level requirement)",
	"chat.join":        "zone channels (id > 0) only join in their zone, and say nothing when you're already in them",
	"death.reclaim":    "too far from your corpse (within ~30 yd)",
	"death.spirit_res": "stand within ~5 yd of the spirit healer",
}

// failOn turns an "error" event for op into a failed outcome.
func failOn(ev script.Event, op string) *script.Outcome {
	if e, ok := ev.Data.(types.ErrorEvent); ok && ev.Type == "error" && e.Op == op {
		return script.Fail(e.Code, e.Message, nil)
	}
	return nil
}

// slug: "Inventory is full" -> "inventory_is_full", a stable error code from a message.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, s)
}

// ChatMsg (SharedDefines.h). The leaders' party, raid and battleground chat is that chat.
var chatKinds = map[uint8]string{0: "system", 1: "say", 2: "party", 3: "raid", 4: "guild", 5: "officer", 6: "yell",
	7: "whisper", 8: "whisper", 9: "whisper_sent", 0xA: "emote", 0xB: "emote", 0xC: "npc_say", 0xD: "npc_party",
	0xE: "npc_yell", 0xF: "npc_whisper", 0x10: "npc_emote", 0x11: "channel", 0x12: "channel_join", 0x13: "channel_leave",
	0x14: "channel_list", 0x15: "channel_notice", 0x16: "channel_notice", 0x17: "afk", 0x18: "dnd", 0x19: "ignored",
	0x1A: "skill", 0x1B: "loot", 0x1C: "money", 0x1D: "opening", 0x1E: "tradeskill", 0x1F: "pet_info", 0x20: "combat_info",
	0x21: "xp", 0x22: "honor", 0x23: "reputation", 0x24: "battleground_system", 0x25: "battleground_system",
	0x26: "battleground_system", 0x27: "raid", 0x28: "raid_warning", 0x29: "npc_emote", 0x2A: "npc_whisper", 0x2B: "filtered",
	0x2C: "battleground", 0x2D: "battleground", 0x2E: "restricted", 0x2F: "npc_say", 0x30: "achievement",
	0x31: "guild_achievement", 0x32: "arena_points", 0x33: "party"}

func chatKind(t uint8) string {
	if k, ok := chatKinds[t]; ok {
		return k
	}
	return fmt.Sprintf("type_%d", t)
}

func spellName(id uint32) string {
	if sp := spellInfo(id); sp != nil {
		return sp.Name
	}
	return ""
}

// watchEvents decodes server packets into events (called from setupControl).
func (b *Bot) watchEvents() {
	w := b.world
	me := w.CharGUID()
	prevDeath, prevLevel := w.OnDeath, w.OnLevelUp
	var lastAttacker uint64
	var lastCause string // the environment hit us last
	w.OnDeath = func() {
		d := types.Death{Cause: lastCause}
		if lastAttacker != 0 && lastCause == "" {
			r := b.ref(lastAttacker)
			d.Killer = &r
		}
		b.emit("death", d)
		if prevDeath != nil {
			prevDeath()
		}
	}
	// A character whose connection drops in combat stays in the world and can die there; it then logs in dead
	// with no death event. Log it (no killer) once our own object has arrived.
	go func() {
		for range 20 {
			time.Sleep(500 * time.Millisecond)
			if w.GetObject(me) != nil {
				if !b.IsAlive() {
					b.emit("death", types.Death{})
				}
				return
			}
		}
	}()
	w.OnLevelUp = func(lvl uint32) {
		b.emit("level_up", types.LevelUp{Level: lvl})
		if prevLevel != nil {
			prevLevel(lvl)
		}
	}
	w.AddGroupListHook(func(st client.GroupState) {
		members := []string{}
		for _, m := range st.Members {
			members = append(members, m.Name)
		}
		b.emit("party_changed", types.PartyChanged{InGroup: st.InGroup, Members: members})
	})
	var zone, area uint32
	failedAt := map[uint32]time.Time{} // spell -> its last SMSG_CAST_FAILED (packet hooks run on one goroutine)
	w.AddPacketHook(func(op uint16, data []byte) {
		r := bytes.NewReader(data)
		u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
		u64 := func() (v uint64) { binary.Read(r, binary.LittleEndian, &v); return }
		hit := func(src, dst uint64, kind string, d types.Damage) {
			if (src != me && dst != me) || (d.Amount == 0 && d.Overheal == 0 && d.Outcome == "") {
				return
			}
			if dst == me && kind == "damage" && (d.Amount > 0 || d.Outcome == "instakill") {
				lastAttacker, lastCause = src, ""
			}
			d.Src, d.Dst, d.SpellName, d.Taken = b.ref(src), b.ref(dst), spellName(d.Spell), dst == me
			b.emit(kind, d)
		}
		energize := func(src, dst uint64, spell, power, amount uint32, periodic bool) {
			if (src == me || dst == me) && amount > 0 {
				b.emit("energize", types.Energize{Src: b.ref(src), Dst: b.ref(dst), Spell: spell, SpellName: spellName(spell),
					Power: name(powerNames, uint8(power)), Amount: amount, Periodic: periodic})
			}
		}
		switch op {
		case smsgEnvironmentalDmg: // victim (u64), type, amount, resisted, absorbed
			if u64() != me {
				return
			}
			typ, _ := r.ReadByte()
			amount := u32()
			lastCause = envCauses[typ]
			b.emit("damage", types.Damage{Dst: b.ref(me), Amount: amount, Taken: true, Cause: lastCause})
			b.clog.add(combatEvent{T: time.Now().UnixMilli(), Dst: me, Amount: amount, Kind: "environment"})
		case smsgResurrectRequest: // caster (u64), name length, name + 2 NULs (the string's and an explicit one), sickness
			from := u64()
			u32()
			readCString(r)
			r.ReadByte()
			sick, _ := r.ReadByte()
			b.agent.mu.Lock()
			b.agent.resFrom = from
			b.agent.mu.Unlock()
			b.emit("resurrect_offered", types.ResurrectOffer{From: b.ref(from), Sickness: sick != 0})
		case smsgStartMirrorTimer, smsgPauseMirrorTimer, smsgStopMirrorTimer: // timer, then start: value, max, scale, paused, spell; pause: paused
			id := u32()
			s := b.agent
			s.mu.Lock()
			t, ok := s.timers[id]
			switch op {
			case smsgStartMirrorTimer:
				t = mirrorTimer{value: u32(), max: u32(), scale: int32(u32()), at: time.Now()}
				p, _ := r.ReadByte()
				t.paused = p != 0
				s.timers[id] = t
			case smsgPauseMirrorTimer:
				if !ok {
					s.mu.Unlock()
					return
				}
				t = mirrorTimer{value: uint32(t.view(id).SecondsLeft * 1000), max: t.max, scale: t.scale, at: time.Now()}
				p, _ := r.ReadByte()
				t.paused = p != 0
				s.timers[id] = t
			default:
				delete(s.timers, id)
			}
			v := t.view(id)
			s.mu.Unlock()
			if op == smsgStopMirrorTimer {
				v.State, v.SecondsLeft = "stopped", 0
			}
			b.emit("mirror_timer", v)
		case smsgCreatureQueryResp:
			if entry, n, ci, ok := readCreatureQuery(r); ok {
				creatures.Lock()
				creatures.name[entry], creatures.info[entry] = n, ci
				creatures.Unlock()
			}
		case smsgPartyKillLog: // the player credited (the tapper: Unit::Kill), victim; sent to them and their party
			credited, victim := u64(), u64()
			pet, party := b.GetPetGUID(), w.GroupState().Members // only the credited player's party gets this (Unit::Kill)
			killer := b.clog.killingBlow(victim, credited, func(g uint64) bool {
				return g == me || g == pet || slices.ContainsFunc(party, func(m client.GroupMember) bool { return m.GUID == g })
			})
			if pet != 0 && killer == pet {
				killer = me
			}
			if killer == me {
				b.kills++ // every kill of yours, spells included (OnKill only sees melee killing blows)
				v := b.ref(victim)
				b.addEvent("kill", "Killed %s (entry %d)", cmp.Or(v.Name, "a creature"), v.Entry)
				b.emit("kill", types.Kill{Victim: v})
			} else if victim != me {
				k := b.ref(killer)
				b.emit("party_kill", types.Kill{Victim: b.ref(victim), Killer: &k})
			}
		case smsgAttackStart: // attacker, victim
			if u64() == me {
				b.emit("attack_start", types.AttackStart{Target: types.GUID(u64())})
			}
		case smsgAttackerState: // hitInfo, attacker, target, damage, overkill, subs, (absorb), (resist), victim state
			hitInfo := u32()
			src, dst := readPackedGUID(r), readPackedGUID(r)
			dmg := u32()
			u32()
			n, _ := r.ReadByte()
			for i := uint8(0); i < n; i++ {
				u32()
				u32()
				u32()
			}
			if hitInfo&0x60 != 0 { // absorbed
				for i := uint8(0); i < n; i++ {
					u32()
				}
			}
			if hitInfo&0x180 != 0 { // resisted
				for i := uint8(0); i < n; i++ {
					u32()
				}
			}
			state, _ := r.ReadByte()
			b.attackOutcome(src, dst, types.Damage{Amount: dmg, Outcome: meleeOutcome(hitInfo, state)}, hit)
		case smsgSpellDmgLog: // target, caster, spell, damage
			dst, src := readPackedGUID(r), readPackedGUID(r)
			spell := u32()
			hit(src, dst, "damage", types.Damage{Spell: spell, Amount: u32()})
		case smsgSpellLogMiss: // spell, caster (u64), unk u8, count, then per target: target (u64), miss reason
			spell := u32()
			src := u64()
			r.ReadByte()
			for n := u32(); n > 0 && n < 64; n-- {
				dst := u64()
				reason, _ := r.ReadByte()
				b.attackOutcome(src, dst, types.Damage{Spell: spell, Outcome: missReasons[reason]}, hit)
			}
		case smsgSpellHealLog: // target, caster, spell, heal, overheal
			dst, src := readPackedGUID(r), readPackedGUID(r)
			spell, heal, over := u32(), u32(), u32()
			hit(src, dst, "heal", types.Damage{Amount: heal - min(heal, over), Overheal: min(heal, over), Spell: spell})
		case smsgPeriodicAura:
			dst, src, spell, ticks := parsePeriodic(data)
			for _, t := range ticks {
				switch t.kind {
				case "dot":
					hit(src, dst, "damage", types.Damage{Amount: t.amount, Spell: spell, Periodic: true})
				case "hot":
					hit(src, dst, "heal", types.Damage{Amount: t.amount - min(t.amount, t.over), Overheal: min(t.amount, t.over), Spell: spell, Periodic: true})
				case "energize":
					energize(src, dst, spell, t.power, t.amount, true)
				}
			}
		case smsgSpellEnergizeLog: // target, caster, spell, power, amount
			dst, src := readPackedGUID(r), readPackedGUID(r)
			spell, power := u32(), u32()
			energize(src, dst, spell, power, u32(), false)
		case smsgDamageShield: // owner (u64), attacker (u64), spell, damage, overkill, school: the attacker takes it
			src, dst := u64(), u64()
			spell := u32()
			hit(src, dst, "damage", types.Damage{Spell: spell, Amount: u32()})
		case smsgInstakillLog: // caster (u64), target (u64), spell
			src, dst := u64(), u64()
			hit(src, dst, "damage", types.Damage{Spell: u32(), Outcome: "instakill"})
		case smsgSpellImmune, smsgProcResist: // caster (u64), target (u64), spell, debug
			src, dst := u64(), u64()
			b.attackOutcome(src, dst, types.Damage{Spell: u32(), Outcome: map[uint16]string{smsgSpellImmune: "immune", smsgProcResist: "resist"}[op]}, hit)
		case smsgDispelLog, smsgStealLog: // target, caster, spell, unused, count, (spell, unused)...
			dst, src := readPackedGUID(r), readPackedGUID(r)
			spell := u32()
			r.ReadByte()
			if src != me && dst != me {
				return
			}
			d := types.Dispel{Src: b.ref(src), Dst: b.ref(dst), Spell: spell, SpellName: spellName(spell), Removed: []string{}, Stolen: op == smsgStealLog}
			for n := u32(); n > 0 && r.Len() >= 5; n-- {
				d.Removed = append(d.Removed, spellName(u32()))
				r.ReadByte()
			}
			b.emit("dispel", d)
		case smsgLogXPGain: // victim (u64), amount, 0 = kill / 1 = other
			victim, amount := u64(), u32()
			typ, _ := r.ReadByte()
			x := types.XPGain{Amount: amount, Source: "other"}
			if typ == 0 {
				v := b.ref(victim)
				x.Source, x.Victim = "kill", &v
			}
			b.emit("xp", x)
		case smsgQuestComplete: // quest, xp, money
			q := u32()
			b.emit("quest_turned_in", types.QuestDone{Quest: q, Title: questTitle(w, q), XP: u32(), Money: u32()})
		case smsgQuestUpdateAddKill: // quest, entry, count, required, guid
			q, entry, n, need := u32(), u32(), u32(), u32()
			b.emit("quest_progress", types.QuestProgress{Quest: q, Entry: entry & 0x7FFFFFFF, Have: n, Need: need})
		case smsgQuestUpdateDone:
			q := u32()
			b.emit("quest_complete", types.QuestDone{Quest: q, Title: questTitle(w, q)})
		case smsgQuestInvalid: // QuestFailedReason (Player::SendCanTakeQuestResponse)
			r, ok := questInvalid[u32()]
			if !ok {
				r = questInvalid[0]
			}
			b.emit("error", types.ErrorEvent{Op: "quests", Code: r[0], Message: "the quest giver refused: " + r[1]})
		case smsgQuestFailed, smsgQuestFailedTimer: // quest (+ an InventoryResult, always 0): Player::FailQuest
			q := u32()
			ev := types.QuestDone{Quest: q, Title: questTitle(w, q), Reason: "failed"}
			if op == smsgQuestFailedTimer {
				ev.Reason = "timer"
			}
			b.emit("quest_failed", ev)
		case client.SmsgQuestgiverRequestItems:
			if q, done, turnIn, need := parseQuestRequestItems(data); turnIn && !done { // a hello or quest query shows it too
				msg := fmt.Sprintf("quest %d isn't complete (quests.log)", q)
				sep := ": it takes "
				for _, it := range need {
					msg += fmt.Sprintf("%s%d x %s", sep, it[1], itemTemplate(w, it[0]).Name)
					sep = ", "
				}
				b.emit("error", types.ErrorEvent{Op: "quests", Code: "incomplete", Message: msg})
			}
		case smsgQuestLogFull:
			b.emit("error", types.ErrorEvent{Op: "quests", Code: "quest_log_full", Message: "your quest log is full (25)"})
		case smsgLootMoneyNotify:
			b.emit("money", types.Money{Amount: u32(), Source: "loot"})
		case smsgItemPushResult: // player, received, created, show, bag, slot, entry, suffix, random, count
			if u64() != me {
				return // also sent to party members
			}
			received := u32()
			u32()
			u32()
			r.ReadByte()
			u32()
			it := itemTemplate(w, u32())
			u32()
			u32()
			it.Count = u32()
			ev := types.ItemReceived{Item: it, From: "loot"}
			if received == 1 {
				ev.From = "npc" // quest reward or purchase
			}
			if it.Name != "" {
				b.emit("item_received", ev)
				return
			}
			go func() { // the item's template may still be on its way
				for i := 0; i < 10 && ev.Item.Name == ""; i++ {
					time.Sleep(100 * time.Millisecond)
					count := ev.Item.Count
					ev.Item = itemTemplate(w, ev.Item.Entry)
					ev.Item.Count = count
				}
				b.emit("item_received", ev)
			}()
		case smsgAchievementEarned:
			if readPackedGUID(r) == me {
				b.emit("achievement", types.AchievementEarned{ID: u32()})
			}
		case smsgSpellStart:
			readPackedGUID(r)
			if readPackedGUID(r) != me {
				return
			}
			r.ReadByte()
			spell := u32()
			u32()
			b.emit("cast_start", types.Cast{Spell: spell, Name: spellName(spell), CastMS: u32()})
		case smsgSpellGo: // misses here aren't in SMSG_SPELLLOGMISS (that only has the ones found on impact)
			g := parseSpellGo(data)
			c := types.Cast{Spell: g.spell, Name: spellName(g.spell), Hits: len(g.hits)}
			for _, m := range g.misses {
				b.attackOutcome(g.caster, m.guid, types.Damage{Spell: g.spell, Outcome: missReasons[m.reason]}, hit)
				if g.caster == me {
					c.Misses = append(c.Misses, types.SpellMiss{Unit: b.ref(m.guid), Outcome: missReasons[m.reason]})
				}
			}
			if g.caster == me {
				b.emit("cast_done", c)
			}
		case smsgSpellFailure: // caster, cast count, spell, reason
			if readPackedGUID(r) != me {
				return
			}
			r.ReadByte()
			spell := u32()
			code, _ := r.ReadByte()
			if failedAt[spell].After(time.Now().Add(-time.Second)) { // SMSG_CAST_FAILED just said why (Spell::cancel, Spell::cast send both)
				delete(failedAt, spell)
				return
			}
			c := types.Cast{Spell: spell, Name: spellName(spell), Code: castResultNames[code], Interrupted: true}
			if code == 0 { // the cast bar ended without the spell, and no reason
				c.Code = "cancelled"
			}
			b.emit("cast_failed", c)
		case smsgCastFailed: // cast count, spell, reason
			r.ReadByte()
			spell := u32()
			code, _ := r.ReadByte()
			if code == 0 || (code == 27 && !w.KnowsSpell(spell)) { // success; dont_report on a spell you don't have (mount forms tried on death): a client shows nothing
				return
			}
			failedAt[spell] = time.Now()
			c := types.Cast{Spell: spell, Name: spellName(spell), Code: castResultNames[code],
				Detail: castFailDetail(code, r, func(e uint32) string {
					if n := itemTemplate(w, e).Name; n != "" {
						return n
					}
					return fmt.Sprintf("item %d", e)
				})}
			if code >= 28 && code <= 30 {
				c.Hint = "needs a working weapon: check items.broken() and repair at an armorer (vendor.repair)"
			}
			b.emit("cast_failed", c)
		case smsgInitWorldStates: // map, zone, area: sent on login and on every zone/area change
			u32()
			z, a := u32(), u32()
			if z != zone || a != area {
				zone, area = z, a
				names := areaNames()
				b.emit("area_changed", types.Location{Zone: names[z], Area: names[a], ZoneID: z, AreaID: a})
			}
		}
	})
	prevReject := w.OnAttackReject
	w.OnAttackReject = func(r client.AttackReject) {
		if r.Reason != "ATTACK_STOP" { // the server stopping your swings (target died, you moved off): not a failure
			b.emit("attack_failed", types.AttackFailed{Code: strings.ToLower(r.Reason), Target: types.GUID(r.GUID)})
		}
		if prevReject != nil {
			prevReject(r)
		}
	}
	b.setupActions()
}

// stateEvents emits transitions only visible in client state (each AI tick, before scripts run).
func (b *Bot) stateEvents() {
	b.sampleFightLog()
	combat, alive := b.InCombat(), b.IsAlive() && !b.IsGhost()
	groupCombat := combat || b.partyInCombat()
	if b.wasAlive != nil {
		b.fightTransitions(groupCombat && !b.wasGroupCombat, !groupCombat && b.wasGroupCombat, !alive && *b.wasAlive && !b.IsGhost())
		if combat != b.wasCombat {
			b.emit(map[bool]string{true: "combat_start", false: "combat_end"}[combat], types.None{})
		}
		if alive && !*b.wasAlive {
			b.agent.mu.Lock()
			b.agent.resFrom = 0
			b.agent.mu.Unlock()
			b.emit("resurrected", types.None{})
			broken := []types.Item{}
			for _, it := range bagItems(b.world) {
				if it.Equipped && it.Broken {
					broken = append(broken, it)
				}
			}
			if len(broken) > 0 {
				b.emit("gear_broken", types.GearBroken{Items: broken, Message: fmt.Sprintf("%d equipped item(s) at 0 durability give no stats (a broken weapon: fighting bare-handed): repair at an armorer (vendor.repair)", len(broken))})
			}
		}
	}
	b.wasCombat, b.wasGroupCombat, b.wasAlive = combat, groupCombat, &alive
	b.travelEvents(b.ConsumeTeleport())
}

// setupActions: move.to / move.stop and spells.cast as requests.
func (b *Bot) setupActions() {
	e := b.lua
	b.setupMovementRequests()
	b.setupRoutes()
	b.setupRecovery()
	e.Func("spells.cast", "(id [, guid = you]) → Request<CastDone>", "cast a spell on a unit: ok when it goes off, else the server's reason (out_of_range, line_of_sight, not_ready, no_power, bad_targets, moving, interrupted, cancelled, unit_not_infront, caster_aurastate, equipped_item_class, ...) or not_known; when it goes off but every target avoids it, how (miss, resist, immune, evade, reflect, dodge, parry...): power and cooldown are spent",
		func(spell uint32, target uint64) map[string]any {
			if target == 0 {
				target = b.world.CharGUID()
			}
			args := map[string]any{"spell": spell, "target": gs(target)}
			if !b.world.KnowsSpell(spell) { // the server silently ignores these
				return b.refuse("spells.cast", args, "not_known", "you don't know this spell (train it, or check the rank id)")
			}
			if err := b.CastSpell(spell, target); err != nil {
				return b.refuse("spells.cast", args, "failed", err.Error())
			}
			return b.request("spells.cast", args, castTimeout(spell), nil, castMatch(spell))
		})
}

// castMatch resolves a request on the spell's cast_done / cast_failed.
func castMatch(spell uint32) func(script.Event) *script.Outcome {
	return func(ev script.Event) *script.Outcome {
		c, ok := ev.Data.(types.Cast)
		if !ok || c.Spell != spell {
			return nil
		}
		switch ev.Type {
		case "cast_done":
			if c.Hits == 0 && len(c.Misses) > 0 {
				o := c.Misses[0].Outcome
				return script.Fail(o, "it went off but didn't land: "+o, types.CastDone{Spell: spell})
			}
			return script.Ok(types.CastDone{Spell: spell})
		case "cast_failed":
			msg := c.Code
			if c.Detail != "" {
				msg += ": " + c.Detail
			}
			if c.Hint != "" {
				msg += ": " + c.Hint
			}
			return script.Fail(c.Code, msg, nil)
		}
		return nil
	}
}

// SetEventsFile keeps the event stream in path (JSONL), before Run.
func (b *Bot) SetEventsFile(path string) { b.config.EventsFile = path }

// setupWatch: the "stall" and "progress" events, so the agent can wait for its scripts to need it (subscribe to
// stall) instead of waking for everything that happens. XP is the measure: kills, quests and exploring all give it.
//
//	stall    (types.Stall): no XP for stall_s, or the XP rate over the last window_s below `below`. Once, until
//	         progress resumes.
//	progress (types.Progress): a digest every every_s (0 = off)
func (b *Bot) setupWatch() {
	type gain struct {
		t  time.Time
		xp uint32
	}
	var (
		mu                             sync.Mutex
		stallS, below, everyS, windowS = 180.0, 0.0, 0.0, 300.0
		gains                          []gain         // ponytail: trimmed to the window each tick
		period                         metrics.Combat // kills and deaths since the last digest
		lastXP, lastDigest             = time.Now(), time.Now()
		stalled                        string // the reason of the stall reported, until it's over
	)
	b.lua.Bus.Listen(func(ev script.Event) {
		mu.Lock()
		defer mu.Unlock()
		switch ev.Type {
		case "xp":
			if x, ok := ev.Data.(types.XPGain); ok {
				gains, lastXP = append(gains, gain{time.Now(), x.Amount}), time.Now()
				if stalled == "no_xp" {
					stalled = ""
				}
			}
		}
		period.Add(metrics.Event{Type: ev.Type, Data: ev.Data})
	})
	b.lua.Func("events.watch", "{[stall_s = 180, below = 0, window_s = 300, every_s = 0]} → settings",
		"when to emit stall (no XP for stall_s seconds, or fewer than below XP/hour over the last window_s; 0 = off) and progress (a digest every every_s; 0 = off) events. Subscribe to them to be woken",
		func(o map[string]any) map[string]any {
			mu.Lock()
			defer mu.Unlock()
			for k, p := range map[string]*float64{"stall_s": &stallS, "below": &below, "window_s": &windowS, "every_s": &everyS} {
				if v, ok := o[k].(float64); ok {
					*p = v
				}
			}
			stalled = ""
			return map[string]any{"stall_s": stallS, "below": below, "window_s": windowS, "every_s": everyS}
		})
	start := time.Now()
	stopped := make(chan struct{})
	previousClose := b.lua.OnClose
	b.lua.OnClose = func() {
		close(stopped)
		if previousClose != nil {
			previousClose()
		}
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-ticker.C:
			}
			mu.Lock()
			now := time.Now()
			for len(gains) > 0 && now.Sub(gains[0].t).Seconds() > max(windowS, everyS) {
				gains = gains[1:]
			}
			sum := func(s float64) (xp uint32) { // XP over the last s seconds
				for _, g := range gains {
					if now.Sub(g.t).Seconds() <= s {
						xp += g.xp
					}
				}
				return xp
			}
			rate := func(s float64) float64 { // XP/hour over the last s seconds (or since the start)
				s = min(s, now.Sub(start).Seconds())
				return metrics.PerHour(float64(sum(s)), s, 60)
			}
			type out struct {
				typ  string
				data any
			}
			var emit []out
			idle, r := now.Sub(lastXP).Seconds(), rate(windowS)
			slow := below > 0 && now.Sub(start).Seconds() >= windowS && r < below
			switch {
			case stalled == "slow" && !slow:
				stalled = ""
			case stalled == "" && stallS > 0 && idle >= stallS:
				stalled = "no_xp"
				emit = append(emit, out{"stall", types.Stall{Reason: "no_xp", Seconds: int(idle), XPPerHour: r}})
			case stalled == "" && slow:
				stalled = "slow"
				emit = append(emit, out{"stall", types.Stall{Reason: "slow", XPPerHour: r, Below: below, WindowS: windowS}})
			}
			if everyS > 0 && now.Sub(lastDigest).Seconds() >= everyS {
				s := now.Sub(lastDigest).Seconds()
				d := types.Progress{XP: sum(s), XPPerHour: rate(s), Kills: period.Kills + period.PartyKills,
					Deaths: period.Deaths, Seconds: int(s)}
				lastDigest, period = now, metrics.Combat{}
				emit = append(emit, out{"progress", d})
			}
			mu.Unlock()
			for _, o := range emit {
				b.emit(o.typ, o.data)
			}
		}
	}()
}
