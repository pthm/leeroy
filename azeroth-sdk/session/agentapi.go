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
	"github.com/pthm/wowbench/azeroth-sdk/navigation"
	"github.com/pthm/wowbench/azeroth-sdk/script"
	"github.com/pthm/wowbench/azeroth-sdk/types"
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
	fieldItemFlags     = 0x15  // ITEM_FIELD_FLAGS
	fieldItemDur       = 0x3C  // ITEM_FIELD_DURABILITY
	fieldItemMaxDur    = 0x3D  // ITEM_FIELD_MAXDURABILITY
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
	zone      uint32 // from SMSG_INIT_WORLD_STATES, sent on login and every zone/area change
	area      uint32
}

func (b *Bot) setupAgentAPI() {
	s := &agentState{cd: map[uint32]time.Time{}, catCD: map[uint32]time.Time{}, earned: map[uint32]bool{},
		criteria: map[uint32]uint64{}}
	w := b.world
	b.agent = s
	spellsDir = b.config.DataDir
	go loadSpells() // ~50MB; don't stall the packet goroutine on first use
	w.AddPacketHook(func(op uint16, data []byte) { s.onPacket(w.CharGUID(), op, data) })
	e := b.lua

	// ---------- me ----------
	e.Func("me.stats", "() → Stats?", "your character sheet: what gear, buffs and talents add up to", func() any {
		o := w.GetObject(w.CharGUID())
		if o == nil {
			return nil
		}
		f := func(i uint16) float64 { return math.Round(float64(math.Float32frombits(o.Value(i)))*100) / 100 }
		maxOf := func(base uint16, n uint16, float bool) float64 {
			best := 0.0
			for k := uint16(1); k < n; k++ { // schools 1-6 (holy..arcane); 0 is physical
				v := float64(o.Value(base + k))
				if float {
					v = f(base + k)
				}
				best = math.Max(best, v)
			}
			return best
		}
		speed := float64(o.Value(0x3E)) / 1000
		minD, maxD := f(0x46), f(0x47)
		st := types.Stats{Strength: o.Value(0x54), Agility: o.Value(0x55), Stamina: o.Value(0x56), Intellect: o.Value(0x57),
			Spirit: o.Value(0x58), Armor: o.Value(0x63), AttackPower: o.Value(0x7B), RangedAttackPower: o.Value(0x7E),
			WeaponDamage: [2]float64{minD, maxD}, WeaponSpeed: speed, CritPct: f(0x405), DodgePct: f(0x401), ParryPct: f(0x402),
			SpellCritPct: maxOf(0x408, 7, true), SpellPower: uint32(maxOf(0x493, 7, false)), BonusHealing: o.Value(0x4A8)}
		if speed > 0 {
			st.MeleeDPS = math.Round((minD+maxD)/2/speed*10) / 10
		}
		return st
	})
	e.Func("me.resting", "() → bool", "in an inn or city: rested XP builds up", func() bool {
		o := w.GetObject(w.CharGUID())
		return o != nil && o.Value(fieldPlayerFlags)&0x20 != 0
	})
	e.Func("me.casting", "() → Casting?", "the cast or channel in progress (from the moment you send it); nil when idle. Moving interrupts it", func() any {
		s.mu.Lock()
		defer s.mu.Unlock()
		if left := time.Until(s.castUntil); s.castSpell != 0 && left > 0 {
			return types.Casting{Spell: s.castSpell, Name: spellName(s.castSpell), MSLeft: uint32(left.Milliseconds())}
		}
		return nil
	})
	e.Func("me.location", "() → Location", "where you are, by the game's own zone and area names", func() types.Location {
		s.mu.Lock()
		z, a := s.zone, s.area
		s.mu.Unlock()
		names := areaNames() // loads areaParent too
		if a == 0 {          // the server's world-states packet came before we were listening: read the map's area grid
			x, y, _, _, m := w.Position()
			if a = areaAt(b.config.DataDir, m, x, y); a != 0 {
				if z = areaParent[a]; z == 0 {
					z = a
				}
			}
		}
		return types.Location{Zone: names[z], Area: names[a], ZoneID: z, AreaID: a}
	})
	e.Func("me.achievements", "() → Achievements", "what you've earned and your criteria progress (worlddb's wowbench.achievement tables have names). The first call asks the server", func() types.Achievements {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.achAsked {
			s.achAsked = true
			w.SendPacketRaw(cmsgInspectAchieves, packGUID(w.CharGUID()))
		}
		out := types.Achievements{Earned: []uint32{}, Criteria: [][2]uint64{}}
		for id := range s.earned {
			out.Earned = append(out.Earned, id)
		}
		for id, n := range s.criteria {
			out.Criteria = append(out.Criteria, [2]uint64{uint64(id), n})
		}
		return out
	})

	// ---------- spells ----------
	e.Func("spells.info", "(id) → Spell?", "a spell's name, rank, cast time, cooldown, range and cost; nil if unknown", func(id uint32) any {
		if sp := spellOf(w, id); sp != nil {
			return *sp
		}
		return nil
	})
	// AC doesn't push cooldowns for normal casts (the real client reads them from Spell.dbc), so they're tracked here.
	e.Func("spells.cooldown", "(id) → ms", "milliseconds until it's ready (its cooldown, category cooldown and the global cooldown); 0 = ready", func(id uint32) float64 {
		if !b.IsSpellReady(id) {
			return 1500
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
		return math.Max(0, float64(time.Until(at).Milliseconds()))
	})

	// ---------- world ----------
	e.Func("world.ground_z", "(x, y [, z]) → number?", "the height to stand at (x, y) on your map, for move.to; nil without map data. With z (an NPC's spawn z from wowdb) it finds the floor nearest that height: upstairs rooms, buildings, Stormwind's streets. Without it, the outdoor terrain (under any building)",
		func(x, y, hint float32) any {
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
	los := func(to navigation.Point3D) any {
		l, ok := b.nav.(interface {
			InLineOfSight(mapID uint32, a, b navigation.Point3D) (bool, bool)
		})
		if !ok {
			return nil
		}
		px, py, pz, _, m := w.Position()
		clear, ok := l.InLineOfSight(m, navigation.Point3D{X: px, Y: py, Z: pz + 1.8}, to)
		if !ok {
			return nil
		}
		return clear
	}
	e.Func("world.in_sight", "(x, y, z) → bool?", "nothing solid (walls, rock, buildings) between your eyes and the point, as the server checks spells; nil without collision data",
		func(x, y, z float32) any { return los(navigation.Point3D{X: x, Y: y, Z: z + 1.5}) })
	e.Func("units.in_sight", "(guid) → bool?", "line of sight to a unit (see world.in_sight)", func(g uint64) any {
		o := w.GetObject(g)
		if o == nil {
			return nil
		}
		x, y, z := o.InterpolatedPosition()
		return los(navigation.Point3D{X: x, Y: y, Z: z + 1.5})
	})

	// ---------- death ----------
	e.Func("death.corpse", "() → Corpse?", "your corpse and when you can reclaim it; nil while alive. Right after dying it asks the server: call again a moment later",
		func() any {
			s.mu.Lock()
			defer s.mu.Unlock()
			if b.world.Health() > 0 && !b.IsGhost() {
				s.corpse = nil
				return nil
			}
			if time.Since(s.corpseAt) > 3*time.Second {
				s.corpseAt = time.Now()
				w.SendPacketRaw(msgCorpseQuery, nil)
			}
			if s.corpse == nil {
				return nil
			}
			return types.Corpse{X: round1(s.corpse[0]), Y: round1(s.corpse[1]), Z: round1(s.corpse[2]), Map: uint32(s.corpse[3]),
				ReclaimIn: math.Max(0, math.Round(time.Until(s.reclaimAt).Seconds()))}
		})
	alive := func() *script.Outcome {
		if b.IsAlive() && !b.IsGhost() {
			return script.Ok(nil)
		}
		return nil
	}
	e.Func("death.release", "() → request", "release your spirit: ok once you're a ghost at the graveyard", func() map[string]any {
		if b.IsGhost() {
			return b.done("death.release", nil, nil)
		}
		if b.IsAlive() {
			return b.refuse("death.release", nil, "not_dead", "you're alive")
		}
		w.RepopRequest()
		return b.request("death.release", nil, 10*time.Second, func() *script.Outcome {
			if b.IsGhost() {
				return script.Ok(nil)
			}
			return nil
		}, nil)
	})
	e.Func("death.reclaim", "() → request", "as a ghost within ~30 yd of your corpse, come back to life there", func() map[string]any {
		if !b.IsGhost() {
			return b.refuse("death.reclaim", nil, "not_a_ghost", "release first (death.release)")
		}
		s.mu.Lock()
		wait := time.Until(s.reclaimAt)
		s.mu.Unlock()
		if wait > 0 {
			return b.refuse("death.reclaim", nil, "not_yet", fmt.Sprintf("you can reclaim in %.0f s", wait.Seconds()))
		}
		w.ReclaimCorpse()
		return b.request("death.reclaim", nil, 5*time.Second, alive, nil)
	})
	e.Func("death.spirit_res", "(npc) → request", "as a ghost next to a spirit healer: resurrect there (resurrection sickness from level 11, and your gear loses durability)",
		func(npc uint64) map[string]any {
			if !b.IsGhost() {
				return b.refuse("death.spirit_res", map[string]any{"npc": gs(npc)}, "not_a_ghost", "release first (death.release)")
			}
			w.SendPacketRaw(cmsgSpiritHealerAct, binary.LittleEndian.AppendUint64(nil, npc))
			return b.request("death.spirit_res", map[string]any{"npc": gs(npc)}, 5*time.Second, alive, nil)
		})

	// ---------- quests ----------
	e.Func("quests.log", "() → Quest[]", "your quests with their objectives and progress (titles fill in once the server answers)", b.questLog)
	e.Func("quests.accept", "(npc, quest) → request<QuestDone>", "talk to a quest giver and accept a quest it offers", func(npc uint64, quest uint32) map[string]any {
		args := map[string]any{"npc": gs(npc), "quest": quest}
		has := func() bool {
			for _, q := range b.questLog() {
				if q.ID == quest {
					return true
				}
			}
			return false
		}
		if has() {
			return b.done("quests.accept", args, types.QuestDone{Quest: quest, Title: questTitle(w, quest)})
		}
		w.QuestgiverHello(npc)
		w.QuestgiverAcceptQuest(npc, quest)
		return b.request("quests.accept", args, 5*time.Second, func() *script.Outcome {
			if has() {
				return script.Ok(types.QuestDone{Quest: quest, Title: questTitle(w, quest)})
			}
			return nil
		}, func(ev script.Event) *script.Outcome { return failOn(ev, "quests") })
	})
	e.Func("quests.turn_in", "(npc, quest [, reward = 0]) → request<QuestDone>", "hand a complete quest in (reward: the index of the item to choose, 0 = first; wowdb quest <id> lists them)",
		func(npc uint64, quest, reward uint32) map[string]any {
			w.QuestgiverCompleteQuest(npc, quest)
			body := binary.LittleEndian.AppendUint64(nil, npc)
			w.SendPacketRaw(cmsgQuestRequestRew, binary.LittleEndian.AppendUint32(body, quest))
			w.QuestgiverChooseReward(npc, quest, reward)
			return b.request("quests.turn_in", map[string]any{"npc": gs(npc), "quest": quest, "reward": reward}, 5*time.Second,
				nil, func(ev script.Event) *script.Outcome {
					if d, ok := ev.Data.(types.QuestDone); ok && ev.Type == "quest_turned_in" && d.Quest == quest {
						return script.Ok(d)
					}
					if e, ok := ev.Data.(types.ErrorEvent); ok && e.Op == "items" {
						return script.Fail("bags_full", "bags full: the item reward was refused; sell or destroy something", nil)
					}
					return failOn(ev, "quests")
				})
		})
}

// castSent: is_casting covers the round trip between cast_spell and the server's SPELL_START, so a script that
// checks it on the next tick doesn't move (which interrupts the cast) or recast before the server answers.
func (s *agentState) castSent(spell uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Until(s.castUntil) <= 0 {
		s.castSpell, s.castUntil = spell, time.Now().Add(400*time.Millisecond)
	}
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
		if code >= 28 && code <= 30 { // equipped_item*: usually a weapon broken by deaths, or none equipped
			s.castErr["hint"] = "needs a working weapon: check bot.broken_gear() and repair at an armorer (bot.repair)"
		}
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
	case 0x2C2: // SMSG_INIT_WORLD_STATES: map, zone, area, ...
		u32()
		s.zone, s.area = u32(), u32()
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

// --- Spell.dbc (copied to <data-dir>/dbc by the setup) ---

type spellRec struct {
	Name             string  `json:"name"`
	Rank             string  `json:"rank"`
	Category         uint32  `json:"category"`
	Cooldown         uint32  `json:"cooldown_ms"`
	CategoryCooldown uint32  `json:"category_cooldown_ms"`
	GCDCategory      uint32  `json:"gcd_category"`
	GCD              uint32  `json:"gcd_ms"`
	PowerType        uint32  `json:"power_type"`
	Cost             uint32  `json:"cost"`    // rage/runic are x10, as in get_power()
	CastMS           uint32  `json:"cast_ms"` // base cast time (0 = instant)
	Level            uint32  `json:"level"`   // the level you can learn it at (base level)
	attrs            uint32  // Attributes (passive 0x40, hidden 0x80)
	Range            float32 `json:"range"` // max range against hostile targets, yards (melee spells ~5)
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
		ranges, castTimes := dbcFloats("SpellRange.dbc", 3), dbcU32s("SpellCastTimes.dbc", 1)
		for i := uint32(0); i < n; i++ {
			rec := b[20+i*recSize:]
			f := func(idx int) uint32 { return le.Uint32(rec[idx*4:]) }
			// field indices per AzerothCore DBCStructure.h SpellEntry
			spells[f(0)] = &spellRec{Name: str(f(136)), Rank: str(f(153)), Category: f(1), Cooldown: f(29),
				CategoryCooldown: f(30), GCDCategory: f(205), GCD: f(206), PowerType: f(41), Cost: f(42),
				Range: ranges[f(46)], CastMS: castTimes[f(28)], Level: f(38), attrs: f(4)}
		}
	})
}

func spellInfo(id uint32) *spellRec {
	loadSpells() // waits for the background load; no-op if it already ran
	return spells[id]
}

// dbcFloats / dbcU32s: id -> one column of a small DBC (SpellRange max hostile range, SpellCastTimes base time)
func dbcFloats(name string, col int) map[uint32]float32 {
	out := map[uint32]float32{}
	for id, v := range dbcColumn(name, col) {
		out[id] = math.Float32frombits(v)
	}
	return out
}

func dbcU32s(name string, col int) map[uint32]uint32 { return dbcColumn(name, col) }

func dbcColumn(name string, col int) map[uint32]uint32 {
	out := map[uint32]uint32{}
	b, err := os.ReadFile(filepath.Join(spellsDir, "dbc", name))
	if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
		return out
	}
	le := binary.LittleEndian
	n, rs := le.Uint32(b[4:]), le.Uint32(b[12:])
	for i := uint32(0); i < n; i++ {
		rec := b[20+i*rs:]
		out[le.Uint32(rec)] = le.Uint32(rec[col*4:])
	}
	return out
}

var (
	areaOnce   sync.Once
	areaNameM  map[uint32]string
	areaParent map[uint32]uint32 // area -> its zone (0 for a zone)
	areaGrids  sync.Map          // "map/gx/gy" -> []uint16 (16x16 area ids of one .map grid)
)

// areaAt: the area id at (x, y) from <data>/maps/MMMXXYY.map, the same grid the server uses
func areaAt(dataDir string, mapID uint32, x, y float32) uint32 {
	const S = 1600.0 / 3
	fx, fy := 32-float64(x)/S, 32-float64(y)/S
	gx, gy := int(math.Floor(fx)), int(math.Floor(fy))
	key := fmt.Sprintf("%d/%d/%d", mapID, gx, gy)
	v, ok := areaGrids.Load(key)
	if !ok {
		var grid []uint16
		if b, err := os.ReadFile(filepath.Join(dataDir, "maps", fmt.Sprintf("%03d%02d%02d.map", mapID, gx, gy))); err == nil && len(b) > 16 {
			le := binary.LittleEndian
			if off := le.Uint32(b[12:]); off != 0 && int(off)+8 <= len(b) {
				grid = make([]uint16, 256)
				flags, single := le.Uint16(b[off+4:]), le.Uint16(b[off+6:])
				for i := range grid {
					if flags&1 != 0 || int(off)+8+i*2+2 > len(b) {
						grid[i] = single
					} else {
						grid[i] = le.Uint16(b[int(off)+8+i*2:])
					}
				}
			}
		}
		areaGrids.Store(key, grid)
		v = grid
	}
	grid := v.([]uint16)
	if grid == nil {
		return 0
	}
	i, j := min(127, int((fx-float64(gx))*128)), min(127, int((fy-float64(gy))*128))
	return uint32(grid[(i>>3)*16+(j>>3)])
}

// areaNames: AreaTable.dbc id -> name (field 11, a string offset)
func areaNames() map[uint32]string {
	areaOnce.Do(func() {
		areaNameM, areaParent = map[uint32]string{}, map[uint32]uint32{}
		b, err := os.ReadFile(filepath.Join(spellsDir, "dbc", "AreaTable.dbc"))
		if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
			return
		}
		le := binary.LittleEndian
		n, rs := le.Uint32(b[4:]), le.Uint32(b[12:])
		strs := b[20+n*rs:]
		for i := uint32(0); i < n; i++ {
			rec := b[20+i*rs:]
			if off := le.Uint32(rec[11*4:]); off < uint32(len(strs)) {
				end := bytes.IndexByte(strs[off:], 0)
				areaNameM[le.Uint32(rec)] = string(strs[off : off+uint32(end)])
				areaParent[le.Uint32(rec)] = le.Uint32(rec[2*4:])
			}
		}
	})
	return areaNameM
}

const fieldUnitFaction = 0x37 // UNIT_FIELD_FACTIONTEMPLATE (OBJECT_END + 0x31)

type factionTemplate struct {
	faction, our, friend, hostile uint32
	enemies, friends              [4]uint32
}

var (
	ftOnce sync.Once
	ftByID map[uint32]factionTemplate
)

// factionReaction: FactionTemplateEntry::IsHostileTo / IsFriendlyTo (reputation-based standing isn't modelled)
func factionReaction(unitFT, playerFT uint32) string {
	ftOnce.Do(func() {
		ftByID = map[uint32]factionTemplate{}
		for id, rec := range dbcRows("FactionTemplate.dbc") {
			f := func(i int) uint32 { return binary.LittleEndian.Uint32(rec[i*4:]) }
			ftByID[id] = factionTemplate{f(1), f(3), f(4), f(5), [4]uint32{f(6), f(7), f(8), f(9)}, [4]uint32{f(10), f(11), f(12), f(13)}}
		}
	})
	c, okc := ftByID[unitFT]
	p, okp := ftByID[playerFT]
	switch {
	case !okc || !okp:
		return "neutral"
	case unitFT == playerFT:
		return "friendly"
	}
	for _, e := range c.enemies {
		if e != 0 && e == p.faction {
			return "hostile"
		}
	}
	for _, fr := range c.friends {
		if fr != 0 && fr == p.faction {
			return "friendly"
		}
	}
	if c.hostile&p.our != 0 {
		return "hostile"
	}
	if c.friend&p.our != 0 || c.our&p.friend != 0 {
		return "friendly"
	}
	return "neutral"
}

// dbcRows: id -> raw record of a DBC in <data>/dbc
func dbcRows(name string) map[uint32][]byte {
	out := map[uint32][]byte{}
	b, err := os.ReadFile(filepath.Join(spellsDir, "dbc", name))
	if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
		return out
	}
	le := binary.LittleEndian
	n, rs := le.Uint32(b[4:]), le.Uint32(b[12:])
	for i := uint32(0); i < n; i++ {
		rec := b[20+i*rs : 20+(i+1)*rs]
		out[le.Uint32(rec)] = rec
	}
	return out
}
