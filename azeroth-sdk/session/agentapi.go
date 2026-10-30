package session

// Agent API: game state the client doesn't track itself (casting, cooldowns, corpse, bags, quest log),
// mostly from packets and update fields. Registered from setupControl.

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

const (
	smsgCastFailed        = 0x130
	smsgSpellStart        = 0x131
	smsgSpellGo           = 0x132
	smsgSpellFailure      = 0x133
	smsgItemCooldown      = 0x0B0
	smsgInitialSpells     = 0x12A
	smsgSpellCooldown     = 0x134
	smsgCooldownEvent     = 0x135
	smsgClearCooldown     = 0x1DE
	smsgModifyCooldown    = 0x491
	msgChannelStart       = 0x139
	msgChannelUpdate      = 0x13A
	msgCorpseQuery        = 0x216
	cmsgResurrectResponse = 0x15C
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
	onEvent   map[uint32]cdRule // spell -> the cooldown SMSG_COOLDOWN_EVENT starts (potions used in combat)
	// itemCD: an item cast's cooldown, by the item's guid, or its entry when that's 0
	itemCD    func(item uint64, entry, spell uint32) (cdRule, bool)
	pushed    map[uint32]time.Time // SMSG_SPELL_COOLDOWN: sent before the SPELL_GO, it wins over Spell.dbc
	corpse    []float32            // x, y, z, map
	corpseAt  time.Time
	reclaimAt time.Time
	earned    map[uint32]bool   // achievement ids
	criteria  map[uint32]uint64 // achievement criteria id -> counter
	achAsked  bool
	zone      uint32 // from SMSG_INIT_WORLD_STATES, sent on login and every zone/area change
	area      uint32
	resFrom   uint64                 // who offered to resurrect us (SMSG_RESURRECT_REQUEST)
	timers    map[uint32]mirrorTimer // breath, fatigue, fire
}

// mirrorTimer: SMSG_START_MIRROR_TIMER as received; value and scale in ms (scale < 0 counts down)
type mirrorTimer struct {
	value, max uint32
	scale      int32
	paused     bool
	at         time.Time
}

var mirrorNames = map[uint32]string{0: "fatigue", 1: "breath", 2: "fire"}

func (t mirrorTimer) view(id uint32) types.MirrorTimer {
	left := float64(t.value)
	state := "paused"
	if !t.paused {
		left += time.Since(t.at).Seconds() * 1000 * float64(t.scale)
		state = "running"
		if t.scale > 0 {
			state = "recovering"
		}
	}
	left = math.Max(0, math.Min(left, float64(t.max)))
	return types.MirrorTimer{Timer: mirrorNames[id], State: state, SecondsLeft: math.Round(left/100) / 10, MaxS: float64(t.max / 1000)}
}

func (b *Bot) setupAgentAPI() {
	s := &agentState{cd: map[uint32]time.Time{}, catCD: map[uint32]time.Time{}, onEvent: map[uint32]cdRule{}, pushed: map[uint32]time.Time{}, earned: map[uint32]bool{},
		criteria: map[uint32]uint64{}, timers: map[uint32]mirrorTimer{}}
	w := b.world
	b.agent = s
	s.itemCD = func(item uint64, entry, spell uint32) (cdRule, bool) {
		if o := w.GetObject(item); o != nil {
			entry = o.Value(3)
		}
		if p := itemInfo(w, entry); entry != 0 && p != nil && p.UseSpell == spell {
			return p.useCD, true
		}
		return cdRule{}, false
	}
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
	e.Func("me.achievements", "() → Achievements", "what you've earned and your criteria progress (the world database's wowbench.achievement tables have names). The first call asks the server", func() types.Achievements {
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
	e.Func("spells.cooldown", "(id) → ms", "milliseconds until it's ready (its cooldown, category cooldown and the global cooldown); 0 = ready, -1 = not until an event (a potion used in combat: until you leave combat)", func(id uint32) float64 {
		if left := s.cooldownLeft(id); left > 0 {
			return cdMS(left)
		}
		if w.KnowsSpell(id) && !b.IsSpellReady(id) {
			return 1500
		}
		return 0
	})
	e.Func("items.cooldown", "(item) → ms", "milliseconds until an item you carry or wear (entry or guid) can be used again: potions, healthstones, trinkets, the Hearthstone; 0 = ready, -1 = until you leave combat (a potion used in combat)", func(item uint64) float64 {
		var p *itemProto
		for _, it := range bagItems(w) {
			if uint64(it.GUID) == item || it.Entry == uint32(item) && item < 1<<32 {
				p = itemInfo(w, it.Entry)
				break
			}
		}
		if p == nil && item < 1<<32 {
			p = itemInfo(w, uint32(item))
		}
		if p == nil || p.UseSpell == 0 {
			return 0
		}
		return cdMS(s.itemCooldownLeft(p.UseSpell, p.useCD.cat))
	})

	// ---------- world ----------
	e.Func("world.ground_z", "(x, y [, z]) → number?", "the height to stand at (x, y) on your map, for move.to; nil without map data. With z (an NPC's spawn z from azworld) it finds the floor nearest that height: upstairs rooms, buildings, Stormwind's streets. Without it, the outdoor terrain (under any building)",
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
		if clear, ok := b.inSight(to); ok {
			return clear
		}
		return nil
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
			c := types.Corpse{Place: types.Place{X: round1(s.corpse[0]), Y: round1(s.corpse[1]), Z: round1(s.corpse[2])}, Map: uint32(s.corpse[3]),
				ReclaimIn: math.Max(0, math.Ceil(time.Until(s.reclaimAt).Seconds()))} // up: 0 means reclaim now works
			m := b.me3() // from where your ghost stands: "40 yd behind you, 12 below"
			c.Distance = round1(float32(math.Sqrt((c.X-m.x)*(c.X-m.x) + (c.Y-m.y)*(c.Y-m.y) + (c.Z-m.z)*(c.Z-m.z))))
			relate(m.x, m.y, m.z, m.o, &c.Place)
			return c
		})
	alive := func() *script.Outcome {
		if b.IsAlive() && !b.IsGhost() {
			return script.Ok(nil)
		}
		return nil
	}
	e.Func("death.release", "() → Request", "release your spirit: ok once you're a ghost at the graveyard", func() map[string]any {
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
	e.Func("death.reclaim", "() → Request", "as a ghost within ~30 yd of your corpse, come back to life there", func() map[string]any {
		if !b.IsGhost() {
			return b.refuse("death.reclaim", nil, "not_a_ghost", "release first (death.release)")
		}
		s.mu.Lock()
		wait := time.Until(s.reclaimAt)
		s.mu.Unlock()
		if wait > 0 {
			return b.refuse("death.reclaim", nil, "not_yet", fmt.Sprintf("you can reclaim in %.0f s", math.Ceil(wait.Seconds())))
		}
		w.ReclaimCorpse()
		return b.request("death.reclaim", nil, 5*time.Second, alive, nil)
	})
	e.Func("death.accept_res", "() → Request", "accept a player's resurrection (after a resurrect_offered event): you come back where your body lies",
		func() map[string]any {
			s.mu.Lock()
			from := s.resFrom
			s.mu.Unlock()
			if b.IsAlive() && !b.IsGhost() {
				return b.refuse("death.accept_res", nil, "not_dead", "you're alive")
			}
			if from == 0 {
				return b.refuse("death.accept_res", nil, "no_offer", "nobody has offered to resurrect you (wait for resurrect_offered)")
			}
			w.SendPacketRaw(cmsgResurrectResponse, append(binary.LittleEndian.AppendUint64(nil, from), 1))
			return b.request("death.accept_res", nil, 5*time.Second, alive, nil)
		})
	e.Func("me.timers", "() → MirrorTimer[]", "your breath (underwater), fatigue (deep water) and fire timers while one runs: at 0 you take damage every few seconds until you surface or turn back",
		func() []types.MirrorTimer {
			s.mu.Lock()
			defer s.mu.Unlock()
			out := []types.MirrorTimer{}
			for id, t := range s.timers {
				out = append(out, t.view(id))
			}
			return out
		})
	e.Func("death.spirit_res", "(npc_guid) → Request", "as a ghost next to a spirit healer: resurrect there (resurrection sickness from level 11, and your gear loses durability)",
		func(npc uint64) map[string]any {
			args := map[string]any{"npc_guid": gs(npc)}
			if !b.IsGhost() {
				return b.refuse("death.spirit_res", args, "not_a_ghost", "release first (death.release)")
			}
			if r := b.noNPC("death.spirit_res", args, &npc); r != nil {
				return r
			}
			w.SendPacketRaw(cmsgSpiritHealerAct, binary.LittleEndian.AppendUint64(nil, npc))
			return b.request("death.spirit_res", map[string]any{"npc_guid": gs(npc)}, 5*time.Second, alive, nil)
		})

	// ---------- quests ----------
	e.Func("quests.log", "() → Quest[]", "your quests with their objectives and progress (titles fill in once the server answers)", b.questLog)
	e.Func("quests.info", "(quest) → QuestInfo?", "any quest's text (the story, what to do, who to return to) and rewards, as the quest giver shows them; nil the first time (it asks the server: call again a moment later)",
		func(id uint32) any {
			p := questInfo(w, id)
			if p == nil {
				return nil
			}
			q := types.QuestInfo{ID: id, Title: p.title, Level: p.level, MinLevel: p.minLevel, Summary: b.gameText(p.summary),
				Details: b.gameText(p.details), Completed: b.gameText(p.completed), Money: p.money, Spell: p.rewSpell, NextQuest: p.next}
			for _, o := range p.objectives {
				if o != "" {
					q.Objectives = append(q.Objectives, b.gameText(o))
				}
			}
			items := func(l [][2]uint32) (out []types.Item) {
				for _, it := range l {
					if it[0] != 0 {
						t := itemTemplate(w, it[0])
						t.Count = it[1]
						out = append(out, t)
					}
				}
				return out
			}
			q.Rewards, q.Choices = items(p.rewards[:]), items(p.choices[:])
			return q
		})
	e.Func("quests.accept", "(npc_guid, quest_id) → Request<QuestDone>", "talk to a quest giver and accept a quest it offers", func(npc uint64, quest uint32) map[string]any {
		args := map[string]any{"npc_guid": gs(npc), "quest": quest}
		if r := b.noNPC("quests.accept", args, &npc); r != nil {
			return r
		}
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
	e.Func("quests.abandon", "(quest) → Request", "drop a quest from your log (to make room: the log holds 25)", func(quest uint32) map[string]any {
		args := map[string]any{"quest": quest}
		o := w.GetObject(w.CharGUID())
		slot := -1
		for i := uint16(0); o != nil && i < 25; i++ {
			if o.Value(fieldQuestLog+i*5) == quest {
				slot = int(i)
			}
		}
		if slot < 0 {
			return b.refuse("quests.abandon", args, "not_in_log", fmt.Sprintf("quest %d isn't in your quest log", quest))
		}
		w.SendPacketRaw(cmsgQuestLogRemoveQuest, []byte{byte(slot)})
		return b.request("quests.abandon", args, 3*time.Second, func() *script.Outcome {
			if me := w.GetObject(w.CharGUID()); me != nil && me.Value(fieldQuestLog+uint16(slot)*5) != quest {
				return script.Ok(nil)
			}
			return nil
		}, nil)
	})
	e.Func("quests.turn_in", "(npc_guid, quest_id [, reward = 0]) → Request<QuestDone>", "hand a complete quest in (reward: the index of the item to choose, 0 = first; quests.info lists the choices)",
		func(npc uint64, quest, reward uint32) map[string]any {
			args := map[string]any{"npc_guid": gs(npc), "quest": quest, "reward": reward}
			if r := b.noNPC("quests.turn_in", args, &npc); r != nil {
				return r
			}
			if p := questInfo(w, quest); reward >= 6 || p != nil && p.choices[0][0] != 0 && p.choices[reward][0] == 0 {
				return b.refuse("quests.turn_in", args, "bad_reward", fmt.Sprintf("no reward choice %d (0 = first; quests.info lists the choices)", reward))
			}
			// the server answers COMPLETE_QUEST with REQUEST_ITEMS (an incomplete one fails on it) or OFFER_REWARD
			w.QuestgiverCompleteQuest(npc, quest)
			body := binary.LittleEndian.AppendUint64(nil, npc)
			w.SendPacketRaw(cmsgQuestRequestRew, binary.LittleEndian.AppendUint32(body, quest))
			w.QuestgiverChooseReward(npc, quest, reward)
			return b.request("quests.turn_in", args, 5*time.Second,
				nil, func(ev script.Event) *script.Outcome {
					if d, ok := ev.Data.(types.QuestDone); ok && ev.Type == "quest_turned_in" && d.Quest == quest {
						return script.Ok(d)
					}
					if e, ok := ev.Data.(types.ErrorEvent); ok && ev.Type == "error" && e.Op == "items" { // Player::CanRewardQuest
						if e.Code == "inventory_full" || e.Code == "bag_full" {
							e.Code = "bags_full"
						}
						return script.Fail(e.Code, "the turn-in was refused: "+e.Message, nil)
					}
					return failOn(ev, "quests")
				})
		})
}

// questInvalid: QuestFailedReason (QuestDef.h), SMSG_QUESTGIVER_QUEST_INVALID
var questInvalid = map[uint32][2]string{0: {"requirements_not_met", "you don't meet its requirements"},
	1: {"low_level", "you're not high enough level"}, 6: {"wrong_race", "not available to your race"},
	7: {"already_done", "you've completed that quest"}, 12: {"only_one_timed", "you can only be on one timed quest at a time"},
	13: {"already_on", "you're already on that quest"}, 16: {"expansion", "it needs an expansion"},
	18: {"already_on", "you're already on that quest"}, 21: {"missing_items", "you don't have the required items with you"},
	23: {"not_enough_money", "you don't have enough money"}, 26: {"daily_limit", "you've done 25 daily quests today"},
	27: {"tired", "too much play time"}, 29: {"daily_done", "you've done that daily quest today"}}

// parseQuestRequestItems: SMSG_QUESTGIVER_REQUEST_ITEMS (PlayerMenu::SendQuestGiverRequestItems): npc, quest, title,
// text, 0, emote, close on cancel, flags, suggested players, money, items (entry, count, display), then 3 if completable.
// turnIn: the answer to CMSG_QUESTGIVER_COMPLETE_QUEST, the only sender that doesn't set close on cancel.
func parseQuestRequestItems(data []byte) (quest uint32, done, turnIn bool, items [][2]uint32) {
	r := bytes.NewReader(data)
	u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
	r.Seek(8, io.SeekCurrent)
	quest = u32()
	readCString(r)
	readCString(r)
	u32()
	u32()
	turnIn = u32() == 0
	for range 3 {
		u32()
	}
	for n := u32(); n > 0 && r.Len() >= 12; n-- {
		items = append(items, [2]uint32{u32(), u32()})
		u32()
	}
	return quest, u32() != 0, turnIn, items
}

// castSent: is_casting covers the round trip between cast_spell and the server's SPELL_START, so a script that
// checks it on the next tick doesn't move (which interrupts the cast) or recast before the server answers.
// cooldownLeft is how long until the spell is off its own, category and global cooldown, from SPELL_GO.
func (s *agentState) cooldownLeft(id uint32) time.Duration {
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
	return max(0, time.Until(at))
}

// itemCooldownLeft: an item spell's own cooldown and its item category's (no GCD: items.use has none of its own).
func (s *agentState) itemCooldownLeft(spell, cat uint32) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := s.cd[spell]
	if c := s.catCD[cat]; cat != 0 && c.After(at) {
		at = c
	}
	return max(0, time.Until(at))
}

// cdRule: a cast's cooldown as Player::AddSpellAndCategoryCooldowns works it out: the item template's values for item
// casts (the item query sends those, or Spell.dbc's), else Spell.dbc's. ms; <= 0 = none.
type cdRule struct {
	rec, catrec int32
	cat         uint32
	onEvent     bool // starts on SMSG_COOLDOWN_EVENT (potions, SPELL_ATTR0_COOLDOWN_ON_EVENT), blocked until then
}

// cdWait: a cooldown waiting for its event (SMSG_INITIAL_SPELLS sends it as infinite)
var cdWait = time.Now().AddDate(100, 0, 0)

func cdMS(left time.Duration) float64 {
	if left > 365*24*time.Hour {
		return -1
	}
	return float64(left.Milliseconds())
}

func dbcRule(sp *spellRec) cdRule {
	if sp == nil {
		return cdRule{}
	}
	return cdRule{rec: int32(sp.Cooldown), cat: sp.Category, catrec: int32(sp.CategoryCooldown), onEvent: sp.attrs&0x02000000 != 0}
}

// startCD: the spell's cooldown (rec, or catrec when it has none) and its category's. Caller holds s.mu.
// ponytail: base values; talents, glyphs and SPELL_AURA_MOD_COOLDOWN aren't applied (the last sends SMSG_SPELL_COOLDOWN)
func (s *agentState) startCD(spell uint32, c cdRule, now time.Time) {
	if c.onEvent {
		s.cd[spell], s.onEvent[spell] = cdWait, c
		if c.cat != 0 { // the server refuses every potion while one waits (Spell::CheckItems: GetLastPotionId)
			s.catCD[c.cat] = cdWait
		}
		return
	}
	delete(s.onEvent, spell)
	if rec := cmp.Or(max(c.rec, 0), max(c.catrec, 0)); rec > 0 {
		s.cd[spell] = now.Add(time.Duration(rec) * time.Millisecond)
	} else {
		delete(s.cd, spell)
	}
	if c.cat != 0 && c.catrec > 0 {
		s.catCD[c.cat] = now.Add(time.Duration(c.catrec) * time.Millisecond)
	} else if s.catCD[c.cat] == cdWait {
		delete(s.catCD, c.cat)
	}
}

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
	u64 := func() (v uint64) { binary.Read(r, binary.LittleEndian, &v); return }
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	switch op {
	case smsgSpellStart, smsgSpellGo:
		item := readPackedGUID(r) // the item for item casts, else the caster
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
		c := dbcRule(sp)
		if item != me && s.itemCD != nil {
			if ic, ok := s.itemCD(item, 0, spell); ok {
				ic.onEvent = ic.onEvent || c.onEvent
				c = ic
			}
		}
		pushed, ok := s.cd[spell]
		s.startCD(spell, c, now)
		if ok && now.Sub(s.pushed[spell]) < time.Second {
			s.cd[spell] = pushed
		}
	case smsgCooldownEvent: // spell, player: an on-event cooldown starts now (Player::SendCooldownEvent)
		spell := u32()
		if u64() != me {
			return
		}
		c, ok := s.onEvent[spell]
		if !ok && s.cd[spell] != cdWait { // a failed cast's reset (Spell::finish): nothing was waiting
			return
		}
		if !ok { // waiting since login
			c = dbcRule(spellInfo(spell))
		}
		c.onEvent = false
		s.startCD(spell, c, now)
	case smsgSpellCooldown: // player, flags, (spell, ms)...: cooldowns the client can't work out itself
		if u64() != me {
			return
		}
		r.ReadByte()
		for r.Len() >= 8 {
			spell, ms := u32(), u32()
			s.cd[spell], s.pushed[spell] = now.Add(time.Duration(ms)*time.Millisecond), now
		}
	case smsgItemCooldown: // item, spell: the 30 s on-use cooldown of an item just equipped
		u64()
		s.cd[u32()] = now.Add(30 * time.Second)
	case smsgModifyCooldown: // spell, player, ms delta
		spell := u32()
		if u64() == me && !s.cd[spell].IsZero() {
			s.cd[spell] = s.cd[spell].Add(time.Duration(int32(u32())) * time.Millisecond)
		}
	case smsgClearCooldown: // spell, unit
		spell := u32()
		if u64() == me {
			delete(s.cd, spell)
			if c, ok := s.onEvent[spell]; ok {
				delete(s.catCD, c.cat)
			}
			delete(s.onEvent, spell)
			if sp := spellInfo(spell); sp != nil { // ponytail: clears the whole category; a reset spell clears its siblings too
				delete(s.catCD, sp.Category)
			}
		}
	case smsgInitialSpells: // on login and every far teleport: spells, then the cooldowns still running (Player::SendInitialSpells)
		r.ReadByte()
		var n, item, cat uint16
		binary.Read(r, binary.LittleEndian, &n)
		r.Seek(int64(n)*6, io.SeekCurrent)
		waiting := s.onEvent
		if n > 0 { // a spell script sends one with only a cooldown
			clear(s.cd)
			clear(s.catCD)
			s.onEvent = map[uint32]cdRule{}
		}
		binary.Read(r, binary.LittleEndian, &n) // counts entries it then skips, so read to the end
		for r.Len() >= 16 {
			spell := u32()
			binary.Read(r, binary.LittleEndian, &item)
			binary.Read(r, binary.LittleEndian, &cat)
			rec, catrec := u32(), u32()
			// the cast spell comes with category 0 and its category's other spells mostly not at all
			// (Player::AddSpellAndCategoryCooldowns), so its category comes from the item or Spell.dbc
			c, ok := waiting[spell]
			if !ok && item != 0 && s.itemCD != nil {
				c, ok = s.itemCD(0, uint32(item), spell)
			}
			if !ok {
				c = dbcRule(spellInfo(spell))
			}
			if catrec == 0x80000000 {
				c.onEvent = true
				s.startCD(spell, c, now)
				continue
			}
			left := time.Duration(max(rec, catrec)) * time.Millisecond
			s.cd[spell] = now.Add(left)
			if cat != 0 { // a sibling that has its own (catrec != rec): sent with its category's time
				s.catCD[uint32(cat)] = now.Add(left)
			} else if c.cat != 0 && c.catrec > 0 { // its category started with it: catrec of its rec (or catrec)
				if catLeft := left + time.Duration(c.catrec-cmp.Or(max(c.rec, 0), c.catrec))*time.Millisecond; catLeft > 0 {
					s.catCD[c.cat] = now.Add(catLeft)
				}
			}
		}
	case smsgSpellFailure: // caster, cast count, spell, reason
		if readPackedGUID(r) == me {
			r.ReadByte()
			if u32() == s.castSpell {
				s.castSpell = 0
			}
		}
	case smsgSpellDelayed: // caster, ms: pushback from a hit
		if readPackedGUID(r) == me && s.castSpell != 0 {
			s.castUntil = s.castUntil.Add(time.Duration(u32()) * time.Millisecond)
		}
	case smsgCastFailed:
		r.ReadByte()
		spell := u32()
		code, _ := r.ReadByte()
		if code == 0 { // SPELL_FAILED_SUCCESS
			return
		}
		if spell == s.castSpell { // not a second spell refused while this one casts (spell_in_progress)
			s.castSpell = 0
		}
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
	costPct          uint32  // ManaCostPercentage: most 3.3.5 spells cost a percentage of base mana, with Cost 0
	attrs            uint32  // Attributes (passive 0x40, hidden 0x80)
	dispel           uint32  // 1 magic, 2 curse, 3 disease, 4 poison
	casterState      uint32  // the aura state you need (Victory Rush: victory_rush), 0 = none
	targetState      uint32  // the aura state the target needs (Execute: health_below_20)
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
				Range: ranges[f(46)], CastMS: castTimes[f(28)], Level: f(38), costPct: f(204), attrs: f(4), dispel: f(2),
				casterState: f(20), targetState: f(21)}
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

// inSight: line of sight from your eyes to a point, against world models (ok = false without collision data).
func (b *Bot) inSight(to navigation.Point3D) (clear, ok bool) {
	l, has := b.nav.(interface {
		InLineOfSight(mapID uint32, a, b navigation.Point3D) (bool, bool)
	})
	if !has {
		return false, false
	}
	px, py, pz, _, m := b.world.Position()
	return l.InLineOfSight(m, navigation.Point3D{X: px, Y: py, Z: pz + 1.8}, to)
}

type pathEntry struct {
	from, to navigation.Point3D
	at       time.Time
	dist     float64
	ok       bool
}

// reach answers, for things around you, whether they're in sight and how far you'd walk to them. Line of sight is
// cheap; a route costs a few ms and this runs inside a script step, so routes are worked out (nearest first, as
// callers ask) only until a time budget runs out, and cached per thing while neither end has moved much.
// ponytail: 60 ms budget per call and a 5 s / 3 yd cache; a background path worker if agents need it for everything
type reach struct {
	b        *Bot
	from     navigation.Point3D
	m        uint32
	deadline time.Time
}

func (b *Bot) newReach() *reach {
	if b.nav == nil {
		return nil
	}
	if b.pathCache == nil || len(b.pathCache) > 500 {
		b.pathCache = map[uint64]pathEntry{} // ponytail: crude bound; things come and go
	}
	px, py, pz, _, m := b.world.Position()
	return &reach{b: b, from: navigation.Point3D{X: px, Y: py, Z: pz}, m: m, deadline: time.Now().Add(60 * time.Millisecond)}
}

// of: line of sight to a point eye yards above (x, y, z) (nil without collision data), and the walking distance
// (nil when unreachable or not worked out this call) with whether a route gets there (nil when not worked out).
func (r *reach) of(guid uint64, x, y, z float64, eye float32) (inSight *bool, path *float64, reachable *bool) {
	if r == nil {
		return nil, nil, nil
	}
	b, to := r.b, navigation.Point3D{X: float32(x), Y: float32(y), Z: float32(z)}
	if clear, ok := b.inSight(navigation.Point3D{X: to.X, Y: to.Y, Z: to.Z + eye}); ok {
		inSight = &clear
	}
	e, hit := b.pathCache[guid]
	if !hit || time.Since(e.at) > 5*time.Second || dist3(e.from, r.from) > 3 || dist3(e.to, to) > 3 {
		if time.Now().After(r.deadline) {
			return inSight, nil, nil
		}
		e = pathEntry{from: r.from, to: to, at: time.Now()}
		if res, err := b.nav.FindPath(r.m, r.from, to); err == nil && res != nil && len(res.Points) > 0 {
			prev := r.from
			for _, p := range res.Points {
				e.dist += dist3(prev, p)
				prev = p
			}
			e.ok = dist3(prev, to) <= 5
		}
		b.pathCache[guid] = e
	}
	d, ok := math.Round(e.dist*10)/10, e.ok
	if ok {
		path = &d
	}
	return inSight, path, &ok
}

// place fills a Place's sight and route (eye: how high above its base to look: 1.5 for units, 0.5 for objects on
// the ground). Callers share one reach and go most important first: the time budget is per reach.
func (r *reach) place(guid uint64, p *types.Place, eye float32) {
	p.InSight, p.PathDistance, p.Reachable = r.of(guid, p.X, p.Y, p.Z, eye)
}

// annotateNearby: reach for every living creature (see reach)
func (b *Bot) annotateNearby(cs []types.Creature) {
	r := b.newReach()
	for i := range cs {
		if c := &cs[i]; c.Alive {
			r.place(uint64(c.GUID), &c.Place, 1.5)
			if c.InSight != nil && !*c.InSight {
				c.InAggro = false // it can't see you (rock between you: the mob in the mine below)
			}
		}
	}
}

// annotatePlayers: reach for players
func (b *Bot) annotatePlayers(ps []types.Player) {
	r := b.newReach()
	for i := range ps {
		r.place(uint64(ps[i].GUID), &ps[i].Place, 1.5)
	}
}

// annotateObjects: reach for game objects (aimed just above their base: chests and nodes sit on the ground)
func (b *Bot) annotateObjects(os []types.Object) {
	r := b.newReach()
	for i := range os {
		r.place(uint64(os[i].GUID), &os[i].Place, 0.5)
	}
}
