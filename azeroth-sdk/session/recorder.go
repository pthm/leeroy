package session

// Recorder: the client-side twin of the server's mod-recorder. Writes one JSON line per event in the same
// format ({"t","ev","p","n",...}), so the viewer (azview: live, replay) reads either. It sees the daemon's
// own characters and what their clients observe, plus agent-side events the server can't: "api" (each API
// call and the Lua it ran) and "script_error".

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// Recorder is shared by all sessions in a daemon (one file).
type Recorder struct {
	mu sync.Mutex
	f  *os.File
}

// writeDiagnostic preserves the diagnostic schema and its session sequence. Regular viewer records keep
// their existing ev field; diagnostics have kind and are ignored by gameplay scoring.
func (r *Recorder) writeDiagnostic(ev types.Diagnostic) {
	if r == nil {
		return
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.f.Write(append(line, '\n'))
}

func OpenRecorder(path string) (*Recorder, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Recorder{f: f}, nil
}

func (r *Recorder) write(ev string, guid uint64, name string, body map[string]any) {
	if r == nil {
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	body["t"], body["ev"], body["p"], body["n"] = time.Now().UnixMilli(), ev, guid&0xFFFFFFFF, name
	line, _ := json.Marshal(body)
	r.mu.Lock()
	r.f.Write(append(line, '\n'))
	r.mu.Unlock()
}

// SetRecorder attaches a recorder before Run.
func (b *Bot) SetRecorder(r *Recorder) { b.rec = r }

// Record writes an event for this character (no-op without a recorder). Used for agent-side events.
func (b *Bot) Record(ev string, body map[string]any) {
	if b.rec != nil && b.world != nil {
		b.rec.write(ev, b.world.CharGUID(), b.config.CharacterName, body)
	}
}

// unitRef identifies a unit like mod-recorder: players by guid counter + name, creatures by entry (+ level).
func (b *Bot) unitRef(guid uint64) map[string]any {
	if guid>>48 == 0 { // player
		name := ""
		if o := b.world.GetObject(guid); o != nil {
			name = o.Name
		}
		if guid == b.world.CharGUID() {
			name = b.config.CharacterName
		}
		return map[string]any{"id": guid & 0xFFFFFFFF, "n": name, "pl": 1}
	}
	ref := map[string]any{"id": (guid >> 24) & 0xFFFFFF}
	if o := b.world.GetObject(guid); o != nil {
		if o.Entry != 0 {
			ref["id"] = o.Entry
		}
		ref["n"], ref["lvl"] = o.Name, o.Level()
	}
	return ref
}

// startRecording hooks the world client once the character is in the world (called from setupControl).
func (b *Bot) startRecording() {
	if b.rec == nil {
		return
	}
	w := b.world
	b.Record("login", map[string]any{"race": b.GetRace(), "cls": b.GetClass(), "lvl": w.PlayerLevel()})

	var lastAttacker uint64
	prevKill, prevDeath, prevLevel := w.OnKill, w.OnDeath, w.OnLevelUp
	var lastKill uint64 // OnKill only sees melee killing blows; spell kills come from the XP gain below
	w.OnKill = func(victim uint64) {
		if victim != lastKill {
			lastKill = victim
			b.Record("kill", map[string]any{"victim": b.unitRef(victim)})
		}
		if prevKill != nil {
			prevKill(victim)
		}
	}
	w.OnDeath = func() {
		body := map[string]any{}
		if lastAttacker != 0 {
			body["killer"] = b.unitRef(lastAttacker)
		}
		b.Record("death", body)
		if prevDeath != nil {
			prevDeath()
		}
	}
	w.OnLevelUp = func(lvl uint32) {
		b.Record("level", map[string]any{"to": lvl})
		if prevLevel != nil {
			prevLevel(lvl)
		}
	}

	me := w.CharGUID()
	// party changes: {grp (the server's group id, 0 = none), leader, members (names, including self)}. The server
	// sends the list at login, before recording starts, so the current state is recorded first.
	onGroup := func(st client.GroupState) {
		observed := observedGroup(me, st)
		b.lua.RecordObservation("group_state", types.DiagnosticDetails{Group: &observed})
		if !st.InGroup {
			b.Record("group", map[string]any{"grp": 0, "members": []string{}})
			return
		}
		members, leader := []string{b.config.CharacterName}, ""
		if st.LeaderGUID == me {
			leader = b.config.CharacterName
		}
		for _, m := range st.Members {
			members = append(members, m.Name)
			if m.GUID == st.LeaderGUID {
				leader = m.Name
			}
		}
		b.Record("group", map[string]any{"grp": st.GroupGUID & 0xFFFFFFFF, "leader": leader, "members": members})
	}
	onGroup(w.GroupState())
	w.AddGroupListHook(onGroup)
	w.AddPacketHook(func(op uint16, data []byte) {
		r := bytes.NewReader(data)
		u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
		switch op {
		case 0x14A: // SMSG_ATTACKERSTATEUPDATE: hitInfo, attacker, target, damage
			u32()
			src, dst := readPackedGUID(r), readPackedGUID(r)
			if dmg := u32(); (src == me || dst == me) && dmg > 0 {
				if dst == me {
					lastAttacker = src
				}
				b.Record("dmg", map[string]any{"src": b.unitRef(src), "dst": b.unitRef(dst), "amount": dmg})
			}
		case 0x250: // SMSG_SPELLNONMELEEDAMAGELOG: target, caster, spell, damage
			dst, src := readPackedGUID(r), readPackedGUID(r)
			spell, dmg := u32(), u32()
			if (src == me || dst == me) && dmg > 0 {
				if dst == me {
					lastAttacker = src
				}
				b.Record("dmg", map[string]any{"src": b.unitRef(src), "dst": b.unitRef(dst), "amount": dmg, "spell": spell})
			}
		case 0x1D0: // SMSG_LOG_XPGAIN: victim guid (u64), amount, kill/non-kill
			var victim uint64
			binary.Read(r, binary.LittleEndian, &victim)
			b.Record("xp", map[string]any{"amount": u32()})
			if typ, _ := r.ReadByte(); typ == 0 && victim != 0 && victim != lastKill { // 0 = kill XP
				lastKill = victim
				b.Record("kill", map[string]any{"victim": b.unitRef(victim)})
			}
		case 0x191: // SMSG_QUESTGIVER_QUEST_COMPLETE: quest id, xp, money
			q := u32()
			b.Record("quest", map[string]any{"id": q, "xp": u32(), "money": u32()})
		case 0x166: // SMSG_ITEM_PUSH_RESULT: player guid, received, created, show, bag, slot, entry, suffix, random, count
			var who uint64
			binary.Read(r, binary.LittleEndian, &who)
			if who != me {
				return // the server also sends this to party members
			}
			u32()
			u32()
			u32()
			r.ReadByte()
			u32()
			item := u32()
			u32()
			u32()
			b.Record("loot", map[string]any{"item": item, "count": u32()})
		case smsgAchievementEarned:
			if readPackedGUID(r) == me {
				b.Record("achievement", map[string]any{"id": u32()})
			}
		case smsgSpellGo:
			readPackedGUID(r)
			if readPackedGUID(r) == me {
				r.ReadByte()
				spell := u32()
				if sp := spellInfo(spell); sp != nil && sp.Name != "" {
					b.Record("cast", map[string]any{"spell": spell, "name": sp.Name})
				}
			}
		case smsgMessageChat:
			if m, guid, ok := parseChat(op, data); ok && guid != me {
				b.Record("chat", map[string]any{"type": m.Type, "msg": m.Text, "from": m.From, "to": m.Channel})
			}
		}
	})

	// position and state once a second, as mod-recorder's "pos"
	go func() {
		b.lua.RecordObservation("runtime_state", types.DiagnosticDetails{Component: "tooling", State: "instrumented_v1"})
		var previousGear *types.DiagnosticGear
		sampleCondition := func() {
			available := w.GetObject(w.CharGUID()) != nil
			alive := b.IsAlive() && !b.IsGhost()
			g := b.gear()
			gear := types.DiagnosticGear{Money: w.Money(), MainHandBroken: g.Weapon != nil && g.Weapon.Broken, BrokenSlots: g.Broken}
			if available && (previousGear == nil || !reflect.DeepEqual(*previousGear, gear)) {
				b.lua.RecordObservation("gear_state", types.DiagnosticDetails{Gear: &gear})
				previousGear = &gear
			}
			b.lua.RecordObservation("character_state", types.DiagnosticDetails{SampleAvailable: &available, Alive: &alive, Gear: &gear})
		}
		sampleCondition()
		var previous *types.DiagnosticQuestLog
		observeQuests := func() {
			current := observedQuests(w.GetObject(w.CharGUID()))
			if previous == nil || !reflect.DeepEqual(*previous, current) {
				b.lua.RecordObservation("quest_state", types.DiagnosticDetails{QuestLog: &current})
				previous = &current
			}
		}
		observeQuests()
		t := time.NewTicker(time.Second)
		defer t.Stop()
		tick := 0
		for {
			select {
			case <-b.stopCh:
				b.Record("logout", nil)
				return
			case <-t.C:
				sampleCondition()
				observeQuests()
				x, y, z, o, m := w.Position()
				hp, hpmax := b.GetHealth()
				pw, pwmax := b.GetPower()
				var xp uint32
				if me := w.GetObject(w.CharGUID()); me != nil {
					xp = me.Value(0x27A)
				}
				b.Record("pos", map[string]any{"m": m, "x": x, "y": y, "z": z, "o": o, "hp": hp, "hpmax": hpmax,
					"pt": b.GetPowerType(), "pw": pw, "pwmax": pwmax, "lvl": w.PlayerLevel(), "xp": xp, "money": w.Money(),
					"zone": 0, "grp": groupID(w), "alive": b01(b.IsAlive()), "ghost": b01(b.IsGhost()), "combat": b01(b.InCombat())})
				if tick++; tick%2 == 0 {
					b.Record("units", map[string]any{"m": m, "u": b.nearbyUnitRows()})
				}
			}
		}
	}()
}

// groupID is the server's group id (the low half of the group GUID), 0 when not in a group.
func groupID(w *client.WorldClient) uint64 {
	if st := w.GroupState(); st.InGroup {
		return st.GroupGUID & 0xFFFFFFFF
	}
	return 0
}

func b01(v bool) int {
	if v {
		return 1
	}
	return 0
}

// nearbyUnitRows: the creatures within 60 yd for the viewer, each [entry, level, x, y, health %, state], state 0
// hostile, 1 neutral, 2 friendly, plus 4 when dead
func (b *Bot) nearbyUnitRows() [][]any {
	var faction uint32
	if me := b.world.GetObject(b.world.CharGUID()); me != nil {
		faction = me.Value(fieldUnitFaction)
	}
	rows := [][]any{}
	for _, u := range b.GetNearbyUnits(60) {
		if u.IsPlayer || u.Flags&0x2000000 != 0 { // UNIT_FLAG_NOT_SELECTABLE: invisible triggers
			continue
		}
		state := map[string]int{"hostile": 0, "neutral": 1, "friendly": 2}[factionReaction(u.Faction, faction)]
		if !u.IsAlive {
			state += 4
		}
		hp := 0
		if u.MaxHealth > 0 {
			hp = int(100 * u.Health / u.MaxHealth)
		}
		rows = append(rows, []any{u.Entry, u.Level, round1(u.PosX), round1(u.PosY), hp, state})
	}
	return rows
}
