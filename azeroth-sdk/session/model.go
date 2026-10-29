package session

// model.go: game state -> the API's data model (package types). Every query, result and event builds its values
// here, so a creature, an item or a quest looks the same wherever it shows up.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

var classNames = map[uint8]string{1: "warrior", 2: "paladin", 3: "hunter", 4: "rogue", 5: "priest", 6: "death_knight",
	7: "shaman", 8: "mage", 9: "warlock", 11: "druid"}
var raceNames = map[uint8]string{1: "human", 2: "orc", 3: "dwarf", 4: "night_elf", 5: "undead", 6: "tauren", 7: "gnome",
	8: "troll", 10: "blood_elf", 11: "draenei"}
var powerNames = map[uint8]string{0: "mana", 1: "rage", 2: "focus", 3: "energy", 6: "runic_power"}
var qualityNames = []string{"poor", "common", "uncommon", "rare", "epic", "legendary", "artifact", "heirloom"}
var itemClassNames = map[uint32]string{0: "consumable", 1: "container", 2: "weapon", 3: "gem", 4: "armor", 5: "reagent",
	6: "projectile", 7: "trade_goods", 9: "recipe", 11: "quiver", 12: "quest", 13: "key", 15: "misc", 16: "glyph"}
var npcRoles = []struct {
	flag uint32
	role string
}{{0x1, "gossip"}, {0x2, "quest_giver"}, {0x10, "trainer"}, {0x80, "vendor"}, {0x1000, "repair"}, {0x2000, "flight_master"},
	{0x4000, "spirit_healer"}, {0x10000, "innkeeper"}, {0x20000, "banker"}}

func round1(f float32) float64 { return math.Round(float64(f)*10) / 10 }

// sortBy sorts ascending by key (nearest first, for distances).
func sortBy[T any](s []T, key func(T) float64) {
	sort.Slice(s, func(i, j int) bool { return key(s[i]) < key(s[j]) })
}

func name[T comparable](m map[T]string, k T) string {
	if s, ok := m[k]; ok {
		return s
	}
	return fmt.Sprint(k)
}

// ---------- units ----------

func (b *Bot) baseUnit(u UnitInfo) types.Unit {
	t := types.Unit{GUID: types.GUID(u.GUID), Name: u.Name, Level: u.Level, Health: u.Health, MaxHealth: u.MaxHealth,
		Place: types.Place{X: round1(u.PosX), Y: round1(u.PosY), Z: round1(u.PosZ), Distance: round1(u.Distance)}, Alive: u.IsAlive,
		Target: types.GUID(u.Target), InCombat: u.Flags&0x80000 != 0} // UNIT_FLAG_IN_COMBAT
	if me := b.world.GetObject(b.world.CharGUID()); me != nil {
		t.Reaction = factionReaction(u.Faction, me.Value(fieldUnitFaction))
	}
	return t
}

func (b *Bot) creature(u UnitInfo) types.Creature {
	c := types.Creature{Unit: b.baseUnit(u), Entry: u.Entry, Lootable: u.Lootable,
		TappedByOther: u.DynFlags&0x4 != 0 && u.DynFlags&0x8 == 0,                       // tapped, not by you
		Attackable:    u.Flags&0x2 == 0 && u.Flags&0x2000000 == 0 && u.Flags&0x100 == 0, // non-attackable, not selectable, immune to players
		Elite:         u.MaxHealth > u.Level*45+40}                                      // ponytail: rank isn't sent; elites have ~3x normal health
	for _, r := range npcRoles {
		if u.NPCFlags&r.flag != 0 {
			c.Roles = append(c.Roles, r.role)
		}
	}
	b.unitExtras(&c.Unit, u.GUID)
	b.creatureExtras(&c, u)
	return c
}

func (b *Bot) player(u UnitInfo) types.Player {
	p := types.Player{Unit: b.baseUnit(u)}
	b.unitExtras(&p.Unit, u.GUID)
	if o := b.world.GetObject(u.GUID); o != nil {
		b0 := o.Value(fieldUnitBytes0)
		p.Race, p.Class = name(raceNames, uint8(b0&0xFF)), name(classNames, uint8(b0>>8&0xFF))
	}
	return p
}

func (b *Bot) creatures(us []UnitInfo) []types.Creature {
	out := make([]types.Creature, 0, len(us))
	for _, u := range us {
		out = append(out, b.creature(u))
	}
	sortBy(out, func(c types.Creature) float64 { return c.Distance })
	return out
}

func (b *Bot) players(us []UnitInfo) []types.Player {
	out := make([]types.Player, 0, len(us))
	for _, u := range us {
		out = append(out, b.player(u))
	}
	sortBy(out, func(p types.Player) float64 { return p.Distance })
	return out
}

// ref names a unit for events and results.
func (b *Bot) ref(g uint64) types.UnitRef {
	r := types.UnitRef{GUID: types.GUID(g), Name: b.unitName(g), Player: g>>48 == 0, Me: g == b.world.CharGUID()}
	if o := b.world.GetObject(g); o != nil {
		r.Level = o.Level()
		if o.Entry != 0 && !r.Player {
			r.Entry = o.Entry
		}
	}
	if r.Entry == 0 && !r.Player {
		r.Entry = uint32(g>>24) & 0xFFFFFF
	}
	return r
}

// unitName: a player's name, or a creature's ("" until the server has told us)
func (b *Bot) unitName(g uint64) string {
	if g == b.world.CharGUID() {
		return b.config.CharacterName
	}
	if g>>48 == 0 && b.playerName != nil {
		return b.playerName(g)
	}
	if o := b.world.GetObject(g); o != nil {
		return b.objName(o)
	}
	return ""
}

// ---------- items ----------

// itemTemplate: an item's template (Name empty until the server answers the item query)
func itemTemplate(w *client.WorldClient, entry uint32) types.Item {
	it := types.Item{Entry: entry}
	fillTemplate(w, &it)
	return it
}

func fillTemplate(w *client.WorldClient, it *types.Item) {
	p := itemInfo(w, it.Entry)
	if p == nil {
		return
	}
	it.Name, it.Class, it.Subclass, it.Slot = p.Name, name(itemClassNames, p.Class), p.SubClass, p.Slot
	if int(p.Quality) < len(qualityNames) {
		it.Quality = qualityNames[p.Quality]
	}
	it.ItemLevel, it.RequiredLevel, it.Armor, it.DPS, it.Stats = p.ItemLevel, p.ReqLevel, p.Armor, p.DPS, p.Stats
	it.UseSpell, it.StartsQuest, it.MaxStack, it.BuyPrice, it.SellPrice = p.UseSpell, p.StartQuest, p.Stack, p.BuyPrice, p.SellPrice
	why := usable(w, p)
	it.Usable, it.WhyNot = why == "", why
}

// bagItems: everything you wear and carry. Bag 255 = your own slots (0-18 equipped, 19-22 bags, 23-38 backpack),
// bags 19-22 = inside that bag.
func bagItems(w *client.WorldClient) []types.Item {
	out := []types.Item{}
	me := w.GetObject(w.CharGUID())
	if me == nil {
		return out
	}
	add := func(bag, slot uint8, guid uint64) {
		o := w.GetObject(guid)
		if guid == 0 || o == nil {
			return
		}
		it := itemTemplate(w, o.Value(3))
		it.GUID, it.Count, it.Bag, it.BagSlot = types.GUID(guid), max(o.Value(fieldItemStack), 1), bag, slot
		it.Equipped = bag == 255 && slot < 19
		it.Soulbound = o.Value(fieldItemFlags)&1 != 0
		it.Durability, it.MaxDurability = o.Value(fieldItemDur), o.Value(fieldItemMaxDur)
		it.Broken = it.MaxDurability > 0 && it.Durability == 0
		out = append(out, it)
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

// inBags: carried, not worn (and not a bag in a bag slot)
func inBags(it types.Item) bool { return it.Bag != 255 || it.BagSlot >= 23 }

// findItem: an item in your bags (not worn) by guid or, when key is small, by entry.
func findItem(w *client.WorldClient, key uint64) *types.Item {
	for _, it := range bagItems(w) {
		if inBags(it) && (uint64(it.GUID) == key || (key < 1<<32 && it.Entry == uint32(key))) {
			return &it
		}
	}
	return nil
}

// ---------- spells ----------

func spellOf(w *client.WorldClient, id uint32) *types.Spell {
	sp := spellInfo(id)
	if sp == nil {
		return nil
	}
	return &types.Spell{ID: id, Name: sp.Name, Rank: sp.Rank, CastMS: sp.CastMS, CooldownMS: sp.Cooldown,
		CategoryCooldownMS: sp.CategoryCooldown, GCDMS: sp.GCD, Range: sp.Range, Cost: sp.Cost,
		PowerType: name(powerNames, uint8(sp.PowerType)), Known: w.KnowsSpell(id)}
}

// ---------- quests ----------

const (
	cmsgQuestQuery     = 0x05C
	smsgQuestQueryResp = 0x05D
)

type questProto struct {
	title      string
	summary    string // the quest log's "what to do" text
	level      int32
	next       uint32
	npcOrGo    [4]uint32 // 0x80000000 | object entry for game objects
	needKill   [4]uint32
	items      [6][2]uint32 // item, count
	objectives [4]string
	minLevel   uint32
	details    string // the story the quest giver tells
	completed  string // where to go when it's done
	money      uint32 // reward copper (negative values, money the quest takes, read as 0)
	rewSpell   uint32
	rewards    [4][2]uint32 // item, count: all of them
	choices    [6][2]uint32 // item, count: pick one
}

var quests = struct {
	sync.Mutex
	proto map[uint32]*questProto
	asked map[uint32]time.Time
}{proto: map[uint32]*questProto{}, asked: map[uint32]time.Time{}}

func questInfo(w *client.WorldClient, id uint32) *questProto {
	quests.Lock()
	defer quests.Unlock()
	if p, ok := quests.proto[id]; ok {
		return p
	}
	if time.Since(quests.asked[id]) > 3*time.Second {
		quests.asked[id] = time.Now()
		go w.SendPacketRaw(cmsgQuestQuery, binary.LittleEndian.AppendUint32(nil, id))
	}
	return nil
}

// parseQuestQuery: SMSG_QUEST_QUERY_RESPONSE (AzerothCore PlayerMenu::SendQuestQueryResponse)
func parseQuestQuery(data []byte) {
	r := bytes.NewReader(data)
	u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
	id := u32()
	u32() // method
	p := &questProto{level: int32(u32()), minLevel: u32()}
	for i := 0; i < 7; i++ { // zone, type, suggested players, rep objectives x4
		u32()
	}
	p.next = u32()
	u32() // xp id
	if m := int32(u32()); m > 0 {
		p.money = uint32(m)
	}
	u32() // money at max level
	p.rewSpell = u32()
	if c := int32(u32()); c > 0 { // the spell cast on you, when it isn't the one shown
		p.rewSpell = uint32(c)
	}
	for i := 0; i < 9; i++ { // honor x2, source item, flags, title, players slain, talents, arena points, rep mask
		u32()
	}
	for i := range p.rewards {
		p.rewards[i] = [2]uint32{u32(), u32()}
	}
	for i := range p.choices {
		p.choices[i] = [2]uint32{u32(), u32()}
	}
	for i := 0; i < 15+4; i++ { // reputations (15), POI (4)
		u32()
	}
	p.title, p.summary, p.details = readCString(r), readCString(r), readCString(r)
	readCString(r) // area description
	p.completed = readCString(r)
	for i := 0; i < 4; i++ {
		p.npcOrGo[i], p.needKill[i] = u32(), u32()
		u32() // item drop
		u32()
	}
	for i := 0; i < 6; i++ {
		p.items[i] = [2]uint32{u32(), u32()}
	}
	for i := 0; i < 4; i++ {
		p.objectives[i] = readCString(r)
	}
	quests.Lock()
	quests.proto[id] = p
	quests.Unlock()
}

// questLog: your quests with their objectives (titles and objectives fill in once the server answers)
func (b *Bot) questLog() []types.Quest {
	w := b.world
	out := []types.Quest{}
	o := w.GetObject(w.CharGUID())
	if o == nil {
		return out
	}
	carried := map[uint32]uint32{}
	for _, it := range bagItems(w) {
		carried[it.Entry] += it.Count
	}
	for slot := uint16(0); slot < 25; slot++ {
		f := fieldQuestLog + slot*5
		id := o.Value(f)
		if id == 0 {
			continue
		}
		state, c1, c2 := o.Value(f+1), o.Value(f+2), o.Value(f+3)
		counts := [4]uint32{c1 & 0xFFFF, c1 >> 16, c2 & 0xFFFF, c2 >> 16}
		q := types.Quest{ID: id, Complete: state&1 != 0, Failed: state&2 != 0, Objectives: []types.Objective{}}
		if p := questInfo(w, id); p != nil {
			q.Title, q.Summary, q.Level, q.NextQuest = p.title, p.summary, p.level, p.next
			for i := 0; i < 4; i++ {
				if (p.npcOrGo[i] == 0 || p.needKill[i] == 0) && p.objectives[i] != "" { // explore / event objectives
					done := uint32(0)
					if q.Complete {
						done = 1
					}
					q.Objectives = append(q.Objectives, types.Objective{Text: p.objectives[i], Kind: "explore", Have: done, Need: 1})
					continue
				}
				if p.npcOrGo[i] == 0 || p.needKill[i] == 0 {
					continue
				}
				ob := types.Objective{Text: p.objectives[i], Kind: "kill", Entry: p.npcOrGo[i], Have: counts[i], Need: p.needKill[i]}
				if ob.Entry&0x80000000 != 0 {
					ob.Kind, ob.Entry = "object", ob.Entry&^0x80000000
				}
				if ob.Text == "" {
					creatures.Lock()
					n := creatures.name[ob.Entry]
					creatures.Unlock()
					if ob.Kind == "kill" && n != "" {
						ob.Text = n + " slain"
					}
				}
				q.Objectives = append(q.Objectives, ob)
			}
			for _, it := range p.items {
				if it[0] == 0 || it[1] == 0 {
					continue
				}
				ob := types.Objective{Kind: "item", Entry: it[0], Have: min(carried[it[0]], it[1]), Need: it[1]}
				if t := itemTemplate(w, it[0]); t.Name != "" {
					ob.Text = t.Name
				}
				q.Objectives = append(q.Objectives, ob)
			}
		}
		if len(q.Objectives) == 0 && q.Summary != "" { // explore quests: the summary is the objective
			done := uint32(0)
			if q.Complete {
				done = 1
			}
			q.Objectives = append(q.Objectives, types.Objective{Text: q.Summary, Kind: "explore", Have: done, Need: 1})
		}
		for i := range q.Objectives {
			q.Objectives[i].Done = q.Objectives[i].Have >= q.Objectives[i].Need
		}
		out = append(out, q)
	}
	return out
}

func questTitle(w *client.WorldClient, id uint32) string {
	if p := questInfo(w, id); p != nil {
		return p.title
	}
	return ""
}
