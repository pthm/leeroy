package session

// Typed events and action requests (docs/runtime.md). Everything here is decoded from what a stock AzerothCore
// server sends the client (SMSG opcodes on the world connection): no server-side modules or logs. Event data
// are the structs in package types (EventTypes lists them).

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pthm/wowbench/azeroth-sdk/client"
	"github.com/pthm/wowbench/azeroth-sdk/script"
	"github.com/pthm/wowbench/azeroth-sdk/types"
)

const (
	smsgSpellHealLog       = 0x150
	smsgLootMoneyNotify    = 0x163
	smsgItemPushResult     = 0x166
	smsgQuestInvalid       = 0x18F
	smsgQuestComplete      = 0x191
	smsgQuestFailed        = 0x192
	smsgQuestLogFull       = 0x195
	smsgQuestUpdateDone    = 0x198
	smsgQuestUpdateAddKill = 0x199
	smsgLogXPGain          = 0x1D0
	smsgInitWorldStates    = 0x2C2
	smsgCreatureQueryResp  = 0x061
	smsgAttackStart        = 0x143
	smsgSpellLogMiss       = 0x24B
	smsgSpellFailedOther   = 0x2A6
	smsgUpdateComboPoints  = 0x39D
)

// creature names by entry, from SMSG_CREATURE_QUERY_RESPONSE (the client asks once per entry)
var creatures = struct {
	sync.Mutex
	name  map[uint32]string
	info  map[uint32]creatureInfo
	asked map[uint32]time.Time
}{name: map[uint32]string{}, info: map[uint32]creatureInfo{}, asked: map[uint32]time.Time{}}

// creatureInfo: the rest of SMSG_CREATURE_QUERY_RESPONSE
type creatureInfo struct {
	subname   string
	typ, rank uint32
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

func (b *Bot) done(op string, args map[string]any, data any) map[string]any {
	return b.lua.Reqs.Now(script.Spec{Op: op, Args: args}, script.Ok(data))
}

// sent: an action the server doesn't answer resolves at once: ok, or the client's error.
func (b *Bot) sent(op string, args map[string]any, err error) map[string]any {
	if err != nil {
		return b.refuse(op, args, "failed", err.Error())
	}
	return b.done(op, args, nil)
}

// timeoutHints: the usual reason the server stayed silent
var timeoutHints = map[string]string{
	"quests.accept":    "the quest giver didn't offer it: level, prerequisites, already done, or a different NPC",
	"quests.turn_in":   "the server ignores a turn-in when the quest isn't complete (quests.log) or you aren't next to the NPC that takes it",
	"combat.attack":    "no swing started: out of range or not facing it (move.face)",
	"trainer.learn":    "stand within ~5 yd of the trainer",
	"vendor.buy":       "stand within ~5 yd of the vendor",
	"vendor.sell":      "stand within ~5 yd of the vendor",
	"objects.use":      "stand within ~5 yd of the object",
	"spells.cast":      "the cast never started: check range, facing and spells.usable",
	"talents.learn":    "check talents.list(): free points, tier (5 points per row) and prerequisites",
	"group.invite":     "no answer from the server",
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

var chatKinds = map[uint8]string{0: "system", 1: "say", 2: "party", 3: "raid", 4: "guild", 5: "officer", 6: "yell",
	7: "whisper", 8: "whisper_sent", 9: "whisper_sent", 0xA: "emote", 0xB: "emote", 0xC: "npc_say", 0xD: "npc_party",
	0xE: "npc_yell", 0xF: "npc_whisper", 0x10: "npc_emote", 0x11: "channel", 0x29: "npc_emote", 0x2A: "npc_whisper",
	0x2F: "npc_say"}

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
	w.OnDeath = func() {
		var d types.Death
		if lastAttacker != 0 {
			r := b.ref(lastAttacker)
			d.Killer = &r
		}
		b.emit("death", d)
		if prevDeath != nil {
			prevDeath()
		}
	}
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
	w.AddPacketHook(func(op uint16, data []byte) {
		r := bytes.NewReader(data)
		u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
		u64 := func() (v uint64) { binary.Read(r, binary.LittleEndian, &v); return }
		hit := func(src, dst uint64, amount uint32, spell uint32, kind string, outcome ...string) {
			if (src != me && dst != me) || (amount == 0 && len(outcome) == 0) {
				return
			}
			if dst == me && kind == "damage" {
				lastAttacker = src
			}
			d := types.Damage{Src: b.ref(src), Dst: b.ref(dst), Amount: amount, Spell: spell, SpellName: spellName(spell), Taken: dst == me}
			if len(outcome) > 0 {
				d.Outcome = outcome[0]
			}
			b.emit(kind, d)
		}
		switch op {
		case smsgCreatureQueryResp: // entry (high bit: unknown), name
			if entry := u32(); entry&0x80000000 == 0 { // names x4, subname, icon, type flags, type, family, rank
				n := readCString(r)
				readCString(r)
				readCString(r)
				readCString(r)
				ci := creatureInfo{subname: readCString(r)}
				readCString(r)
				u32()
				ci.typ = u32()
				u32()
				ci.rank = u32()
				creatures.Lock()
				creatures.name[entry], creatures.info[entry] = n, ci
				creatures.Unlock()
			}
		case smsgPartyKillLog: // killer, victim: sent to the killer and their party
			killer, victim := u64(), u64()
			if killer == me {
				b.emit("kill", types.Kill{Victim: b.ref(victim)})
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
			outcome := map[uint8]string{0: "miss", 1: "hit", 2: "dodge", 3: "parry", 4: "interrupt", 5: "block", 6: "evade", 7: "immune", 8: "deflect"}[state]
			if outcome == "hit" && hitInfo&0x200 != 0 {
				outcome = "crit"
			}
			b.attackOutcome(src, dst, dmg, 0, outcome, hit)
		case smsgSpellDmgLog: // target, caster, spell, damage
			dst, src := readPackedGUID(r), readPackedGUID(r)
			spell := u32()
			hit(src, dst, u32(), spell, "damage")
		case smsgSpellLogMiss: // spell, caster (u64), unk u8, count, then per target: target (u64), miss reason
			spell := u32()
			src := u64()
			r.ReadByte()
			for n := u32(); n > 0 && n < 64; n-- {
				dst := u64()
				reason, _ := r.ReadByte()
				outcome := map[uint8]string{1: "miss", 2: "resist", 3: "dodge", 4: "parry", 5: "block", 6: "evade", 7: "immune",
					8: "immune", 9: "deflect", 10: "absorb", 11: "reflect"}[reason]
				b.attackOutcome(src, dst, 0, spell, outcome, hit)
			}
		case smsgSpellHealLog: // target, caster, spell, heal, overheal
			dst, src := readPackedGUID(r), readPackedGUID(r)
			spell, heal, over := u32(), u32(), u32()
			if (src == me || dst == me) && heal > 0 {
				b.emit("heal", types.Damage{Src: b.ref(src), Dst: b.ref(dst), Amount: heal - min(heal, over), Overheal: min(heal, over),
					Spell: spell, SpellName: spellName(spell), Taken: dst == me})
			}
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
		case smsgQuestInvalid, smsgQuestFailed:
			code := "quest_invalid"
			if op == smsgQuestFailed {
				u32()
				code = "quest_failed"
			}
			reason := u32()
			b.emit("error", types.ErrorEvent{Op: "quests", Code: code,
				Message: fmt.Sprintf("the quest giver refused (reason %d): level, prerequisites, or already done", reason)})
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
		case smsgSpellStart, smsgSpellGo:
			readPackedGUID(r)
			if readPackedGUID(r) != me {
				return
			}
			r.ReadByte()
			spell := u32()
			c := types.Cast{Spell: spell, Name: spellName(spell)}
			if op == smsgSpellStart {
				u32()
				c.CastMS = u32()
				b.emit("cast_start", c)
			} else {
				b.emit("cast_done", c)
			}
		case smsgSpellFailure: // caster, cast count, spell, reason
			if readPackedGUID(r) != me {
				return
			}
			r.ReadByte()
			spell := u32()
			code, _ := r.ReadByte()
			b.emit("cast_failed", types.Cast{Spell: spell, Name: spellName(spell), Code: castResultNames[code], Interrupted: true})
		case smsgCastFailed: // cast count, spell, reason
			r.ReadByte()
			spell := u32()
			code, _ := r.ReadByte()
			c := types.Cast{Spell: spell, Name: spellName(spell), Code: castResultNames[code]}
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
	combat, alive := b.InCombat(), b.IsAlive() && !b.IsGhost()
	groupCombat := combat || b.partyInCombat()
	if b.wasAlive != nil {
		b.fightTransitions(groupCombat && !b.wasGroupCombat, !groupCombat && b.wasGroupCombat, !alive && *b.wasAlive && !b.IsGhost())
		if combat != b.wasCombat {
			b.emit(map[bool]string{true: "combat_start", false: "combat_end"}[combat], types.None{})
		}
		if alive && !*b.wasAlive {
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
	type move struct {
		id                int
		x, y, z           float32
		started, deadline time.Time
		straight          bool
		planned, cancel   atomic.Bool // route worked out (started/deadline/straight set); superseded or stopped
	}
	var cur *move // ponytail: guarded by the engine lock (Lua calls) and only read in Check (tick goroutine)
	// routes are worked out off the Lua thread (a long or cold-tile path takes longer than a script step may run);
	// planMu keeps them in call order so a superseded route can't land after its replacement
	var planMu sync.Mutex
	dist := func(m *move) float64 {
		x, y, z, _ := b.GetPosition()
		return math.Sqrt(float64((x-m.x)*(x-m.x) + (y-m.y)*(y-m.y) + (z-m.z)*(z-m.z)))
	}
	e.Func("move.to", "(x, y, z) → request<MoveDone>", "walk there along the navmesh: ok on arrival, else stuck or timeout. Calling it again near the same point (chasing) keeps the same request; a different point supersedes it. With no navmesh path it walks a straight line (no_path)",
		func(x, y, z float32) map[string]any {
			args := map[string]any{"x": x, "y": y, "z": z}
			if lock := b.world.MovementLock(); lock != "" {
				return b.refuse("move.to", args, lock, map[string]string{"in_flight": "you're on a flight: wait for flight_end",
					"teleporting": "you're being teleported: wait a moment", "feared": "you're feared: the server moves you",
					"confused": "you're confused: the server moves you", "charmed": "something controls you",
					"stunned": "you're stunned", "rooted": "you're rooted"}[lock])
			}
			if m := cur; m != nil && b.lua.Reqs.Result(m.id)["pending"] == true {
				if dx, dy := m.x-x, m.y-y; dx*dx+dy*dy < 100 {
					m.x, m.y, m.z = x, y, z
					if m.planned.Load() && planMu.TryLock() { // short chase repaths stay inline
						b.MoveTo(x, y, z)
						planMu.Unlock()
					}
					return b.lua.Reqs.Result(m.id)
				}
				m.cancel.Store(true)
				b.lua.Reqs.Resolve(m.id, script.Fail("superseded", "a newer move.to replaced this one", nil))
			}
			m := &move{x: x, y: y, z: z}
			cur = m
			go func() {
				planMu.Lock()
				defer planMu.Unlock()
				if m.cancel.Load() {
					return
				}
				m.straight = b.MoveTo(x, y, z) != nil
				if m.cancel.Load() { // stopped while planning (a newer move.to plans after this)
					b.StopMoving()
				}
				px, py, pz, _ := b.GetPosition()
				d := math.Sqrt(float64((px-x)*(px-x) + (py-y)*(py-y) + (pz-z)*(pz-z)))
				m.started = time.Now()
				m.deadline = m.started.Add(time.Duration(d/5*1.5+10) * time.Second)
				m.planned.Store(true)
			}()
			req := b.request("move.to", args, time.Hour, func() *script.Outcome {
				if !m.planned.Load() {
					return nil
				}
				d := math.Round(dist(m)*10) / 10
				res := types.MoveDone{Distance: d, NoPath: m.straight}
				switch {
				case d < 2.5 || (d < 5 && !b.IsMoving()):
					return script.Ok(res)
				case !b.IsMoving() && time.Since(m.started) > time.Second:
					msg := fmt.Sprintf("stopped %.0f yd short", d)
					if m.straight {
						msg += " (no navmesh path, walked a straight line: blocked by terrain?)"
					}
					return script.Fail("stuck", msg, res)
				case time.Now().After(m.deadline):
					b.StopMoving()
					return script.Fail("timeout", fmt.Sprintf("still %.0f yd away", d), res)
				}
				return nil
			}, nil)
			m.id = req["id"].(int)
			return req
		})
	e.Func("move.stop", "() → request<MoveDone>", "stop walking (a pending move.to resolves ok with stopped = true)", func() map[string]any {
		if cur != nil {
			cur.cancel.Store(true)
			b.lua.Reqs.Resolve(cur.id, script.Ok(types.MoveDone{Stopped: true, Distance: math.Round(dist(cur)*10) / 10}))
		}
		b.StopMoving()
		return b.done("move.stop", nil, nil)
	})
	e.Func("spells.cast", "(id [, guid = you]) → request<CastDone>", "cast a spell on a unit: ok when it goes off, else the server's reason (out_of_range, line_of_sight, not_ready, no_power, bad_targets, moving, interrupted, not_infront, equipped_item_class, ...) or not_known",
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
			return script.Ok(types.CastDone{Spell: spell})
		case "cast_failed":
			msg := c.Code
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
