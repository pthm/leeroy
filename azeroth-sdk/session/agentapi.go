package session

// Agent API: game state the client doesn't track itself (casting, cooldowns, corpse, bags, quest log),
// mostly from packets and update fields. Registered from setupControl.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pthm/wowbench/azeroth-sdk/client"
)

const (
	smsgCastFailed        = 0x130
	smsgSpellStart        = 0x131
	smsgSpellGo           = 0x132
	smsgSpellFailure      = 0x133
	msgChannelStart       = 0x139
	msgChannelUpdate      = 0x13A
	msgCorpseQuery        = 0x216
	cmsgSpiritHealerAct   = 0x21C
	smsgCorpseReclaimWait = 0x269
	cmsgQuestRequestRew   = 0x18C
	smsgAchievementEarned = 0x468
	smsgCriteriaUpdate    = 0x46A
	cmsgInspectAchieves   = 0x46B
	smsgInspectAchieves   = 0x46C

	fieldPlayerFlags   = 0x96  // PLAYER_FLAGS
	fieldQuestLog      = 0x9E  // PLAYER_QUEST_LOG_1_1; 25 slots x 5 fields: id, state, counters (4 x u16), time
	fieldInvSlots      = 0x144 // PLAYER_FIELD_INV_SLOT_HEAD: slots 0-18 equipped, 19-22 bags, 23-38 backpack
	fieldItemStack     = 0x0E  // ITEM_FIELD_STACK_COUNT
	fieldContainerSize = 0x40  // CONTAINER_FIELD_NUM_SLOTS
	fieldContainerSlot = 0x42  // CONTAINER_FIELD_SLOT_1
)

type agentState struct {
	mu        sync.Mutex
	castSpell uint32
	castUntil time.Time
	castErr   map[string]any
	cd        map[uint32]time.Time // spell -> ready at
	catCD     map[uint32]time.Time // spell category -> ready at
	gcdUntil  time.Time
	corpse    []float32 // x, y, z, map
	corpseAt  time.Time
	reclaimAt time.Time
	earned    map[uint32]bool   // achievement ids
	criteria  map[uint32]uint64 // achievement criteria id -> counter
	achAsked  bool
}

func (b *Bot) setupAgentAPI() {
	s := &agentState{cd: map[uint32]time.Time{}, catCD: map[uint32]time.Time{}, earned: map[uint32]bool{},
		criteria: map[uint32]uint64{}}
	w := b.world
	spellsDir = b.config.DataDir
	go loadSpells() // ~50MB; don't stall the packet goroutine on first use
	w.AddPacketHook(func(op uint16, data []byte) { s.onPacket(w.CharGUID(), op, data) })
	e := b.lua

	e.RegisterFunc("is_ghost", b.IsGhost)
	e.RegisterFunc("is_casting", func() (any, float64) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if left := time.Until(s.castUntil); s.castSpell != 0 && left > 0 {
			return s.castSpell, float64(left.Milliseconds())
		}
		return nil, 0
	})
	e.RegisterFunc("last_cast_error", func() map[string]any {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.castErr
	})
	// is_spell_ready(id) -> ready, ms until ready. The client only knew server-pushed cooldowns, which
	// AC doesn't send for normal casts (the real client reads them from Spell.dbc), so it said "ready"
	// for spells on cooldown.
	e.RegisterFunc("is_spell_ready", func(id uint32) (bool, float64) {
		if !b.IsSpellReady(id) {
			return false, 0
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		at := s.cd[id]
		if sp := spellInfo(id); sp != nil {
			if c := s.catCD[sp.Category]; sp.Category != 0 && c.After(at) {
				at = c
			}
			if sp.GCDCategory != 0 && s.gcdUntil.After(at) {
				at = s.gcdUntil
			}
		}
		left := time.Until(at)
		return left <= 0, math.Max(0, float64(left.Milliseconds()))
	})
	// last_attack_error() -> {reason, guid, time_ms} for the latest rejected melee swing: NOT_IN_RANGE,
	// BAD_FACING (face_target), DEAD_TARGET, CANT_ATTACK
	e.RegisterFunc("last_attack_error", func() map[string]any {
		r := b.lastAttackReject.Load()
		if r == nil {
			return nil
		}
		return map[string]any{"reason": r.Reason, "guid": fmt.Sprint(r.GUID), "time_ms": r.at.UnixMilli()}
	})
	// achievements() -> {earned = {ids...}, criteria = {{id, counter}...}}. Join with worlddb's
	// wowbench.achievement / achievement_criteria. The first call asks the server for the full list.
	e.RegisterFunc("achievements", func() map[string]any {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.achAsked {
			s.achAsked = true
			w.SendPacketRaw(cmsgInspectAchieves, packGUID(w.CharGUID()))
		}
		earned, crit := []uint32{}, []map[string]any{}
		for id := range s.earned {
			earned = append(earned, id)
		}
		for id, n := range s.criteria {
			crit = append(crit, map[string]any{"id": id, "counter": n})
		}
		return map[string]any{"earned": earned, "criteria": crit}
	})
	// ground_z(x, y [, z]) -> the height to stand at (x, y) on your current map, for move_to; nil if no map data.
	// With z (e.g. an NPC's spawn z from wowdb) it returns the walkable floor nearest that height, so indoor and
	// upstairs targets work; without it, the outdoor terrain height (under any building).
	e.RegisterFunc("ground_z", func(x, y, hint float32) any {
		_, _, _, _, m := w.Position()
		if hint != 0 {
			if fn, ok := b.nav.(interface {
				FloorHeight(mapID uint32, x, y, hint float32) (float32, bool)
			}); ok {
				if f, ok := fn.FloorHeight(m, x, y, hint); ok {
					return f
				}
			}
		}
		if tn, ok := b.nav.(terrainHeightNavigator); ok && b.nav != nil {
			if gh, ok := tn.GetTerrainHeight(m, x, y); ok {
				return gh
			}
		}
		return nil
	})
	e.RegisterFunc("spell_info", func(id uint32) *spellRec { return spellInfo(id) })

	// corpse_position() -> x, y, z, seconds until it can be reclaimed; nil when not dead. The first call
	// after dying asks the server and returns nil; ask again a moment later.
	e.RegisterFunc("corpse_position", func() (any, any, any, any) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if b.world.Health() > 0 && !b.IsGhost() {
			s.corpse = nil
			return nil, nil, nil, nil
		}
		if time.Since(s.corpseAt) > 3*time.Second {
			s.corpseAt = time.Now()
			w.SendPacketRaw(msgCorpseQuery, nil)
		}
		if s.corpse == nil {
			return nil, nil, nil, nil
		}
		return s.corpse[0], s.corpse[1], s.corpse[2], math.Max(0, time.Until(s.reclaimAt).Seconds())
	})
	e.RegisterFunc("spirit_res", func(npc uint64) { // resurrect at a spirit healer (with res sickness)
		w.SendPacketRaw(cmsgSpiritHealerAct, binary.LittleEndian.AppendUint64(nil, npc))
	})
	// get_attackers(dist) -> living units within dist that are targeting you
	e.RegisterFunc("get_attackers", func(dist float32) []map[string]any {
		me := w.CharGUID()
		out := []map[string]any{}
		for _, u := range b.GetNearbyUnits(dist) {
			if u.Target == me && u.IsAlive {
				out = append(out, map[string]any{"guid": fmt.Sprint(u.GUID), "entry": u.Entry, "level": u.Level,
					"health": u.Health, "max_health": u.MaxHealth, "distance": u.Distance, "is_player": u.IsPlayer})
			}
		}
		return out
	})
	e.RegisterFunc("bag_items", func() []map[string]any { return bagItems(w) })
	e.RegisterFunc("has_item", func(entry uint32) uint32 {
		n := uint32(0)
		for _, it := range bagItems(w) {
			if it["entry"] == entry {
				n += it["count"].(uint32)
			}
		}
		return n
	})
	e.RegisterFunc("quest_log", func() []map[string]any {
		out := []map[string]any{}
		o := w.GetObject(w.CharGUID())
		if o == nil {
			return out
		}
		for slot := uint16(0); slot < 25; slot++ {
			f := fieldQuestLog + slot*5
			id := o.Value(f)
			if id == 0 {
				continue
			}
			c1, c2 := o.Value(f+2), o.Value(f+3)
			out = append(out, map[string]any{"id": id, "complete": o.Value(f+1)&1 != 0, "failed": o.Value(f+1)&2 != 0,
				"counts": []uint32{c1 & 0xFFFF, c1 >> 16, c2 & 0xFFFF, c2 >> 16}}) // kill/object counters (items: has_item)
		}
		return out
	})
	// turn_in(npc, quest, reward_index): complete -> request reward -> choose reward in one go.
	// Check quest_log()/has_item first; the server silently ignores an incomplete turn-in.
	e.RegisterFunc("turn_in", func(npc uint64, quest, reward uint32) {
		w.QuestgiverCompleteQuest(npc, quest)
		body := binary.LittleEndian.AppendUint64(nil, npc)
		w.SendPacketRaw(cmsgQuestRequestRew, binary.LittleEndian.AppendUint32(body, quest))
		w.QuestgiverChooseReward(npc, quest, reward)
	})
}

func (s *agentState) onPacket(me uint64, op uint16, data []byte) {
	r := bytes.NewReader(data)
	u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
	f32 := func() (v float32) { binary.Read(r, binary.LittleEndian, &v); return }
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	switch op {
	case smsgSpellStart, smsgSpellGo:
		readPackedGUID(r) // caster item
		if readPackedGUID(r) != me {
			return
		}
		r.ReadByte() // cast count
		spell, _ := u32(), u32()
		sp := spellInfo(spell)
		if sp != nil && sp.GCD > 0 {
			s.gcdUntil = now.Add(time.Duration(sp.GCD) * time.Millisecond)
		}
		if op == smsgSpellStart {
			s.castSpell, s.castUntil = spell, now.Add(time.Duration(u32())*time.Millisecond)
			return
		}
		if s.castSpell == spell {
			s.castSpell = 0
		}
		// ponytail: base cooldowns from Spell.dbc; talents/glyphs that shorten them aren't applied
		if sp != nil && sp.Cooldown > 0 {
			s.cd[spell] = now.Add(time.Duration(sp.Cooldown) * time.Millisecond)
		}
		if sp != nil && sp.Category != 0 && sp.CategoryCooldown > 0 {
			s.catCD[sp.Category] = now.Add(time.Duration(sp.CategoryCooldown) * time.Millisecond)
		}
	case smsgSpellFailure:
		if readPackedGUID(r) == me {
			s.castSpell = 0
		}
	case smsgCastFailed:
		r.ReadByte()
		spell := u32()
		code, _ := r.ReadByte()
		s.castSpell = 0
		s.castErr = map[string]any{"spell": spell, "code": code, "reason": castResultNames[code], "time_ms": now.UnixMilli()}
	case msgChannelStart:
		if readPackedGUID(r) == me {
			s.castSpell = u32()
			s.castUntil = now.Add(time.Duration(u32()) * time.Millisecond)
		}
	case msgChannelUpdate:
		if readPackedGUID(r) == me {
			s.castUntil = now.Add(time.Duration(u32()) * time.Millisecond)
		}
	case msgCorpseQuery:
		if found, _ := r.ReadByte(); found != 0 {
			m := u32()
			s.corpse = []float32{f32(), f32(), f32(), float32(m)}
		}
	case smsgAchievementEarned: // also sent for players nearby
		if readPackedGUID(r) == me {
			s.earned[u32()] = true
		}
	case smsgCriteriaUpdate:
		id := u32()
		if n := readPackedGUID(r); readPackedGUID(r) == me {
			s.criteria[id] = n
		}
	case smsgInspectAchieves: // AchievementMgr::BuildAllDataPacket after the player's packed guid
		if readPackedGUID(r) != me {
			return
		}
		for id := u32(); id != 0xFFFFFFFF && r.Len() > 0; id = u32() {
			s.earned[id] = true
			u32() // packed date
		}
		for id := u32(); id != 0xFFFFFFFF && r.Len() > 0; id = u32() {
			s.criteria[id] = readPackedGUID(r)
			readPackedGUID(r)
			r.Seek(16, io.SeekCurrent) // flags, date, 2x elapsed
		}
	case smsgCorpseReclaimWait:
		s.reclaimAt = now.Add(time.Duration(u32()) * time.Millisecond)
	}
}

type attackReject struct {
	client.AttackReject
	at time.Time
}

func packGUID(g uint64) []byte {
	out := []byte{0}
	for i := 0; i < 8; i++ {
		if b := byte(g >> (8 * i)); b != 0 {
			out[0] |= 1 << i
			out = append(out, b)
		}
	}
	return out
}

func guidAt(o *client.WorldObject, f uint16) uint64 {
	return uint64(o.Value(f)) | uint64(o.Value(f+1))<<32
}

// bagItems: bag 255 = the character's own slots (0-18 equipped, 19-22 bags, 23-38 backpack, the
// same numbering as AutoEquipItem), bag 19-22 = slot inside that bag.
func bagItems(w *client.WorldClient) []map[string]any {
	out := []map[string]any{}
	me := w.GetObject(w.CharGUID())
	if me == nil {
		return out
	}
	add := func(bag, slot uint8, guid uint64) {
		if it := w.GetObject(guid); guid != 0 && it != nil {
			out = append(out, map[string]any{"bag": bag, "slot": slot, "guid": fmt.Sprint(guid),
				"entry": it.Value(3), "count": max(it.Value(fieldItemStack), 1)})
		}
	}
	for slot := uint8(0); slot < 39; slot++ {
		add(255, slot, guidAt(me, fieldInvSlots+uint16(slot)*2))
	}
	for bag := uint8(19); bag < 23; bag++ {
		c := w.GetObject(guidAt(me, fieldInvSlots+uint16(bag)*2))
		if c == nil {
			continue
		}
		for slot := uint8(0); uint32(slot) < c.Value(fieldContainerSize) && slot < 36; slot++ {
			add(bag, slot, guidAt(c, fieldContainerSlot+uint16(slot)*2))
		}
	}
	return out
}

// --- Spell.dbc (copied to <data-dir>/dbc by the setup) ---

type spellRec struct {
	Name             string `json:"name"`
	Rank             string `json:"rank"`
	Category         uint32 `json:"category"`
	Cooldown         uint32 `json:"cooldown_ms"`
	CategoryCooldown uint32 `json:"category_cooldown_ms"`
	GCDCategory      uint32 `json:"gcd_category"`
	GCD              uint32 `json:"gcd_ms"`
	PowerType        uint32 `json:"power_type"`
	Cost             uint32 `json:"cost"` // rage/runic are x10, as in get_power()
}

var (
	spellsDir  string
	spellsOnce sync.Once
	spells     map[uint32]*spellRec
)

func loadSpells() {
	spellsOnce.Do(func() {
		spells = map[uint32]*spellRec{}
		b, err := os.ReadFile(filepath.Join(spellsDir, "dbc", "Spell.dbc"))
		if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
			fmt.Fprintf(os.Stderr, "Spell.dbc not loaded (cooldowns/spell_info unavailable): %v\n", err)
			return
		}
		le := binary.LittleEndian
		n, recSize := le.Uint32(b[4:]), le.Uint32(b[12:])
		strs := b[20+n*recSize:]
		str := func(off uint32) string {
			if off >= uint32(len(strs)) {
				return ""
			}
			end := bytes.IndexByte(strs[off:], 0)
			return string(strs[off : off+uint32(end)])
		}
		for i := uint32(0); i < n; i++ {
			rec := b[20+i*recSize:]
			f := func(idx int) uint32 { return le.Uint32(rec[idx*4:]) }
			// field indices per AzerothCore DBCStructure.h SpellEntry
			spells[f(0)] = &spellRec{Name: str(f(136)), Rank: str(f(153)), Category: f(1), Cooldown: f(29),
				CategoryCooldown: f(30), GCDCategory: f(205), GCD: f(206), PowerType: f(41), Cost: f(42)}
		}
	})
}

func spellInfo(id uint32) *spellRec {
	loadSpells() // waits for the background load; no-op if it already ran
	return spells[id]
}
