package session

// Typed events and action requests (docs/runtime.md). Everything here is decoded from what a stock AzerothCore
// server sends the client (SMSG opcodes on the world connection): no server-side modules or logs.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/pthm/wowbench/azeroth-sdk/client"
	"github.com/pthm/wowbench/azeroth-sdk/script"
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
)

// creature names by entry, from SMSG_CREATURE_QUERY_RESPONSE (the client asks once per entry)
var creatures = struct {
	sync.Mutex
	name  map[uint32]string
	asked map[uint32]time.Time
}{name: map[uint32]string{}, asked: map[uint32]time.Time{}}

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

// emit records an event on the character's stream (no-op before the engine exists).
func (b *Bot) emit(typ string, data map[string]any) {
	if b.lua != nil {
		b.lua.Emit(typ, data)
	}
}

// request starts an action request; refuse resolves one at once (the action was never sent).
func (b *Bot) request(op string, args map[string]any, timeout time.Duration, check func() *script.Outcome,
	match func(script.Event) *script.Outcome) map[string]any {
	return b.lua.Reqs.Start(script.Spec{Op: op, Args: args, Timeout: timeout, Check: check, Match: match, Hint: timeoutHints[op]})
}

// timeoutHints: the usual reason the server stayed silent
var timeoutHints = map[string]string{
	"turn_in":      "the server ignores a turn-in when the quest isn't complete (bot.quest_log) or you aren't next to the NPC that takes it",
	"train":        "stand within ~5 yd of the trainer",
	"buy":          "stand within ~5 yd of the vendor",
	"sell":         "stand within ~5 yd of the vendor",
	"use_object":   "stand within ~5 yd of the object",
	"cast":         "the cast never started: check range, facing and is_spell_ready",
	"learn_talent": "check bot.talents(): free points, tier (5 points per row) and prerequisites",
}

func (b *Bot) refuse(op string, args map[string]any, code, msg string) map[string]any {
	return b.lua.Reqs.Now(script.Spec{Op: op, Args: args}, script.Fail(code, msg, nil))
}

func (b *Bot) done(op string, args, data map[string]any) map[string]any {
	return b.lua.Reqs.Now(script.Spec{Op: op, Args: args}, script.Ok(data))
}

// same compares event fields across Go number types (events carry uint32s, args float64s).
func same(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

// failOn turns an "error" event for op into a failed outcome.
func failOn(ev script.Event, op string) *script.Outcome {
	if ev.Type == "error" && ev.Data["op"] == op {
		return script.Fail(fmt.Sprint(ev.Data["code"]), fmt.Sprint(ev.Data["message"]), nil)
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

// who describes a unit for events: {name, guid, entry?, level?, player?}
func (b *Bot) who(guid uint64) map[string]any {
	m := map[string]any{"guid": fmt.Sprint(guid)}
	if guid == b.world.CharGUID() {
		m["name"], m["player"], m["me"] = b.config.CharacterName, true, true
		return m
	}
	if o := b.world.GetObject(guid); o != nil {
		m["name"], m["level"] = b.objName(o), o.Level()
		if o.Entry != 0 {
			m["entry"] = o.Entry
		}
	}
	if guid>>48 == 0 {
		m["player"] = true
		if b.playerName != nil && (m["name"] == nil || m["name"] == "") {
			m["name"] = b.playerName(guid)
		}
	}
	return m
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

// watchEvents decodes server packets into events (called from setupControl).
func (b *Bot) watchEvents() {
	w := b.world
	me := w.CharGUID()
	prevDeath, prevLevel := w.OnDeath, w.OnLevelUp
	var lastAttacker uint64
	w.OnDeath = func() {
		data := map[string]any{}
		if lastAttacker != 0 {
			data["killer"] = b.who(lastAttacker)
		}
		b.emit("death", data)
		if prevDeath != nil {
			prevDeath()
		}
	}
	w.OnLevelUp = func(lvl uint32) {
		b.emit("level_up", map[string]any{"level": lvl})
		if prevLevel != nil {
			prevLevel(lvl)
		}
	}
	w.AddGroupListHook(func(st client.GroupState) {
		members := []string{}
		for _, m := range st.Members {
			members = append(members, m.Name)
		}
		b.emit("party_changed", map[string]any{"in_group": st.InGroup, "members": members})
	})
	var zone, area uint32
	w.AddPacketHook(func(op uint16, data []byte) {
		r := bytes.NewReader(data)
		u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
		u64 := func() (v uint64) { binary.Read(r, binary.LittleEndian, &v); return }
		hit := func(src, dst uint64, amount uint32, spell uint32, kind string) {
			if (src != me && dst != me) || amount == 0 {
				return
			}
			if dst == me && kind == "damage" {
				lastAttacker = src
			}
			d := map[string]any{"src": b.who(src), "dst": b.who(dst), "amount": amount, "taken": dst == me}
			if spell != 0 {
				d["spell"] = spell
				if sp := spellInfo(spell); sp != nil {
					d["spell_name"] = sp.Name
				}
			}
			b.emit(kind, d)
		}
		switch op {
		case smsgCreatureQueryResp: // entry (high bit: unknown), name
			if entry := u32(); entry&0x80000000 == 0 {
				creatures.Lock()
				creatures.name[entry] = readCString(r)
				creatures.Unlock()
			}
		case smsgPartyKillLog: // killer, victim: sent to the killer and their party
			killer, victim := u64(), u64()
			if killer == me {
				b.emit("kill", map[string]any{"victim": b.who(victim)})
			} else if victim != me {
				b.emit("party_kill", map[string]any{"killer": b.who(killer), "victim": b.who(victim)})
			}
		case smsgAttackerState: // hitInfo, attacker, target, damage
			u32()
			src, dst := readPackedGUID(r), readPackedGUID(r)
			hit(src, dst, u32(), 0, "damage")
		case smsgSpellDmgLog: // target, caster, spell, damage
			dst, src := readPackedGUID(r), readPackedGUID(r)
			spell := u32()
			hit(src, dst, u32(), spell, "damage")
		case smsgSpellHealLog: // target, caster, spell, heal, overheal
			dst, src := readPackedGUID(r), readPackedGUID(r)
			spell := u32()
			hit(src, dst, u32(), spell, "heal")
		case smsgLogXPGain: // victim (u64), amount, 0 = kill / 1 = other
			victim, amount := u64(), u32()
			typ, _ := r.ReadByte()
			d := map[string]any{"amount": amount, "source": "other"}
			if typ == 0 {
				d["source"] = "kill"
				d["victim"] = b.who(victim)
			}
			b.emit("xp", d)
		case smsgQuestComplete: // quest, xp, money
			q := u32()
			b.emit("quest_turned_in", map[string]any{"quest": q, "xp": u32(), "money": u32()})
		case smsgQuestUpdateAddKill: // quest, entry, count, required, guid
			q, entry, n, need := u32(), u32(), u32(), u32()
			b.emit("quest_progress", map[string]any{"quest": q, "entry": entry & 0x7FFFFFFF, "count": n, "required": need})
		case smsgQuestUpdateDone:
			b.emit("quest_complete", map[string]any{"quest": u32()})
		case smsgQuestInvalid, smsgQuestFailed:
			code := "quest_invalid"
			if op == smsgQuestFailed {
				u32()
				code = "quest_failed"
			}
			reason := u32()
			b.emit("error", map[string]any{"op": "quest", "code": code, "reason": reason,
				"message": fmt.Sprintf("the quest giver refused (reason %d): level, prerequisites, or already done", reason)})
		case smsgQuestLogFull:
			b.emit("error", map[string]any{"op": "quest", "code": "quest_log_full", "message": "your quest log is full (25)"})
		case smsgLootMoneyNotify:
			b.emit("money", map[string]any{"amount": u32(), "source": "loot"})
		case smsgItemPushResult: // player, received, created, show, bag, slot, entry, suffix, random, count
			if u64() != me {
				return // also sent to party members
			}
			received := u32()
			u32()
			u32()
			r.ReadByte()
			u32()
			entry := u32()
			u32()
			u32()
			d := map[string]any{"item": entry, "count": u32(), "from": "loot"}
			if received == 1 {
				d["from"] = "npc" // quest reward or purchase
			}
			if p := itemInfo(w, entry); p != nil {
				d["name"], d["quality"] = p.Name, p.Quality
			}
			b.emit("item_received", d)
		case smsgAchievementEarned:
			if readPackedGUID(r) == me {
				b.emit("achievement", map[string]any{"id": u32()})
			}
		case smsgSpellStart, smsgSpellGo:
			readPackedGUID(r)
			if readPackedGUID(r) != me {
				return
			}
			r.ReadByte()
			spell := u32()
			d := map[string]any{"spell": spell}
			if sp := spellInfo(spell); sp != nil {
				d["name"] = sp.Name
			}
			if op == smsgSpellStart {
				u32()
				d["cast_ms"] = u32()
				b.emit("cast_start", d)
			} else {
				b.emit("cast_done", d)
			}
		case smsgSpellFailure: // caster, cast count, spell, reason
			if readPackedGUID(r) != me {
				return
			}
			r.ReadByte()
			spell := u32()
			code, _ := r.ReadByte()
			b.emit("cast_failed", map[string]any{"spell": spell, "code": castResultNames[code], "interrupted": true})
		case smsgCastFailed: // cast count, spell, reason
			r.ReadByte()
			spell := u32()
			code, _ := r.ReadByte()
			d := map[string]any{"spell": spell, "code": castResultNames[code]}
			if code >= 28 && code <= 30 {
				d["hint"] = "needs a working weapon: check bot.broken_gear() and repair at an armorer"
			}
			b.emit("cast_failed", d)
		case smsgInitWorldStates: // map, zone, area: sent on login and on every zone/area change
			u32()
			z, a := u32(), u32()
			if z != zone || a != area {
				zone, area = z, a
				names := areaNames()
				b.emit("area_changed", map[string]any{"zone": z, "area": a, "zone_name": names[z], "area_name": names[a]})
			}
		}
	})
	prevReject := w.OnAttackReject
	w.OnAttackReject = func(r client.AttackReject) {
		if r.Reason != "ATTACK_STOP" { // the server stopping your swings (target died, you moved off): not a failure
			b.emit("attack_failed", map[string]any{"code": strings.ToLower(fmt.Sprint(r.Reason)), "target": fmt.Sprint(r.GUID)})
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
	if b.wasAlive != nil {
		if combat != b.wasCombat {
			b.emit(map[bool]string{true: "combat_start", false: "combat_end"}[combat], nil)
		}
		if alive && !*b.wasAlive {
			b.emit("resurrected", nil)
			broken := []any{}
			for _, it := range bagItems(b.world) {
				if it["bag"] == uint8(255) && it["slot"].(uint8) < 19 && it["max_durability"] != nil && it["durability"] == uint32(0) {
					broken = append(broken, it["entry"])
				}
			}
			if len(broken) > 0 {
				b.emit("gear_broken", map[string]any{"items": broken, "message": fmt.Sprintf("%d equipped item(s) at 0 durability give no stats (a broken weapon: fighting bare-handed): repair at an armorer, bot.repair(npc)", len(broken))})
			}
		}
	}
	b.wasCombat, b.wasAlive = combat, &alive
}

// setupActions: move and cast as requests.
func (b *Bot) setupActions() {
	e := b.lua
	type move struct {
		id       int
		x, y, z  float32
		started  time.Time
		deadline time.Time
		straight bool
	}
	var cur *move // ponytail: guarded by the engine lock (Lua calls) and only read in Check (tick goroutine)
	dist := func(m *move) float64 {
		x, y, z, _ := b.GetPosition()
		return math.Sqrt(float64((x-m.x)*(x-m.x) + (y-m.y)*(y-m.y) + (z-m.z)*(z-m.z)))
	}
	// move_to(x, y, z) -> request "move_to": ok on arrival; stuck / timeout with how far short. Calling it again
	// with a nearby point (chasing) keeps the same request; a different destination supersedes it.
	e.RegisterFunc("move_to", func(x, y, z float32) map[string]any {
		args := map[string]any{"x": x, "y": y, "z": z}
		if m := cur; m != nil && b.lua.Reqs.Result(m.id)["pending"] == true {
			if dx, dy := m.x-x, m.y-y; dx*dx+dy*dy < 100 {
				m.x, m.y, m.z = x, y, z
				b.MoveTo(x, y, z)
				return b.lua.Reqs.Result(m.id)
			}
			b.lua.Reqs.Resolve(m.id, script.Fail("superseded", "a newer move_to replaced this one", nil))
		}
		m := &move{x: x, y: y, z: z, started: time.Now()}
		m.straight = b.MoveTo(x, y, z) != nil
		m.deadline = m.started.Add(time.Duration(dist(m)/5*1.5+10) * time.Second)
		cur = m
		req := b.request("move_to", args, time.Hour, func() *script.Outcome {
			d := dist(m)
			short := map[string]any{"distance": math.Round(d*10) / 10}
			switch {
			case d < 2.5 || (d < 5 && !b.IsMoving()):
				return script.Ok(short)
			case !b.IsMoving() && time.Since(m.started) > time.Second:
				msg := fmt.Sprintf("stopped %.0f yd short", d)
				if m.straight {
					msg += " (no navmesh path, walked a straight line: blocked by terrain?)"
				}
				return script.Fail("stuck", msg, short)
			case time.Now().After(m.deadline):
				b.StopMoving()
				return script.Fail("timeout", fmt.Sprintf("still %.0f yd away", d), short)
			}
			return nil
		}, nil)
		m.id = req["id"].(int)
		if m.straight {
			req["no_path"] = true
		}
		return req
	})
	e.RegisterFunc("stop_moving", func() {
		if cur != nil {
			b.lua.Reqs.Resolve(cur.id, script.Ok(map[string]any{"stopped": true, "distance": math.Round(dist(cur)*10) / 10}))
		}
		b.StopMoving()
	})
	// cast_spell(spell, target) -> request "cast": ok when the spell goes off, else the server's reason
	e.RegisterFunc("cast_spell", func(spell uint32, target uint64) map[string]any {
		args := map[string]any{"spell": spell, "target": fmt.Sprint(target)}
		if !b.world.KnowsSpell(spell) { // the server silently ignores these
			return b.refuse("cast", args, "not_known", "you don't know this spell (train it, or check the rank id)")
		}
		if err := b.CastSpell(spell, target); err != nil {
			return b.refuse("cast", args, "refused", err.Error())
		}
		timeout := 5 * time.Second
		if sp := spellInfo(spell); sp != nil {
			timeout += time.Duration(sp.CastMS) * time.Millisecond
		}
		return b.request("cast", args, timeout, nil, castMatch(spell))
	})
}

// castMatch resolves a request on the spell's cast_done / cast_failed.
func castMatch(spell uint32) func(script.Event) *script.Outcome {
	return func(ev script.Event) *script.Outcome {
		if !same(ev.Data["spell"], spell) {
			return nil
		}
		switch ev.Type {
		case "cast_done":
			return script.Ok(map[string]any{"spell": spell})
		case "cast_failed":
			msg := fmt.Sprint(ev.Data["code"])
			if h, ok := ev.Data["hint"]; ok {
				msg += ": " + fmt.Sprint(h)
			}
			return script.Fail(fmt.Sprint(ev.Data["code"]), msg, nil)
		}
		return nil
	}
}

// SetEventsFile keeps the event stream in path (JSONL), before Run.
func (b *Bot) SetEventsFile(path string) { b.config.EventsFile = path }
