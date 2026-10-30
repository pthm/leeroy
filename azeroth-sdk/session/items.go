package session

// Items, vendors and game objects: the inventory side of the protocol the client doesn't implement.
// Registered from setupControl. Item templates come from CMSG_ITEM_QUERY_SINGLE and are cached for the
// process (they're static server data); vendor lists from CMSG_LIST_INVENTORY. Both answers arrive
// asynchronously, so the first call returns nil and a call a moment later has the data.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

const (
	cmsgItemQuery         = 0x056
	smsgItemQuery         = 0x058
	cmsgUseItem           = 0x0AB
	cmsgAutoStoreBagItem  = 0x10B
	cmsgAutoEquipItemSlot = 0x10F
	cmsgDestroyItem       = 0x111
	cmsgListInventory     = 0x19E
	smsgListInventory     = 0x19F
	smsgBuyItem           = 0x1A4
	cmsgSellItem          = 0x1A0
	cmsgBuyItem           = 0x1A2
	cmsgCastSpellOp       = 0x12E

	fieldGOBytes1 = 0x11 // GAMEOBJECT_BYTES_1: state, type, art kit, anim progress
	goTypeChest   = 3
	spellOpening  = 6478 // "Opening": what the game client casts on locked chests and quest crates
)

type itemProto struct {
	Entry      uint32           `json:"entry"`
	Name       string           `json:"name"`
	Quality    uint32           `json:"quality"` // 0 poor (grey, junk) 1 common 2 uncommon 3 rare 4 epic
	Class      uint32           `json:"class"`   // 0 consumable 2 weapon 4 armor 12 quest 15 misc
	SubClass   uint32           `json:"subclass"`
	InvType    uint32           `json:"inv_type"` // equip slot type; 0 = not equippable
	BuyPrice   uint32           `json:"buy_price"`
	SellPrice  uint32           `json:"sell_price"` // copper; 0 = vendors won't buy it
	ItemLevel  uint32           `json:"item_level"`
	ReqLevel   uint32           `json:"required_level"`
	Stack      int32            `json:"max_stack"`
	Armor      uint32           `json:"armor"`
	DPS        float64          `json:"dps"`
	UseSpell   uint32           `json:"use_spell"` // spell cast by use_item (food, drink, potions...); 0 = none
	StartQuest uint32           `json:"start_quest"`
	Stats      map[string]int32 `json:"stats,omitempty"` // e.g. {strength = 3, stamina = 2}
	Slot       string           `json:"slot,omitempty"`  // where it's worn ("" = not equippable)
	allowClass int32            // class bitmask, -1 = any
	useCD      cdRule           // UseSpell's cooldown
}

var statNames = map[uint32]string{0: "mana", 1: "health", 3: "agility", 4: "strength", 5: "intellect", 6: "spirit", 7: "stamina",
	12: "defense", 13: "dodge", 14: "parry", 15: "block", 16: "melee_hit", 17: "ranged_hit", 18: "spell_hit", 19: "melee_crit",
	20: "ranged_crit", 21: "spell_crit", 28: "melee_haste", 31: "hit", 32: "crit", 35: "resilience", 36: "haste", 37: "expertise",
	38: "attack_power", 39: "ranged_attack_power", 41: "healing", 42: "spell_damage", 43: "mana_per_5s", 44: "armor_penetration",
	45: "spell_power", 46: "health_per_5s", 47: "spell_penetration", 48: "block_value"}

// inventory type -> slot name and the equipment slots (0-18) it can go in
// equipSlots: equipment slot names agents can pass (inventory slot index)
var equipSlots = map[string]uint8{"head": 0, "neck": 1, "shoulder": 2, "shirt": 3, "chest": 4, "waist": 5, "legs": 6, "feet": 7,
	"wrists": 8, "hands": 9, "finger1": 10, "finger2": 11, "trinket1": 12, "trinket2": 13, "back": 14, "main_hand": 15,
	"off_hand": 16, "ranged": 17, "tabard": 18}

var invSlots = map[uint32]struct {
	name  string
	slots []uint8
}{1: {"head", []uint8{0}}, 2: {"neck", []uint8{1}}, 3: {"shoulder", []uint8{2}}, 4: {"shirt", []uint8{3}}, 5: {"chest", []uint8{4}},
	20: {"chest", []uint8{4}}, 6: {"waist", []uint8{5}}, 7: {"legs", []uint8{6}}, 8: {"feet", []uint8{7}}, 9: {"wrists", []uint8{8}},
	10: {"hands", []uint8{9}}, 11: {"finger", []uint8{10, 11}}, 12: {"trinket", []uint8{12, 13}}, 13: {"one_hand", []uint8{15, 16}},
	14: {"shield", []uint8{16}}, 15: {"ranged", []uint8{17}}, 16: {"back", []uint8{14}}, 17: {"two_hand", []uint8{15}},
	19: {"tabard", []uint8{18}}, 21: {"main_hand", []uint8{15}}, 22: {"off_hand", []uint8{16}}, 23: {"held_in_off_hand", []uint8{16}},
	25: {"thrown", []uint8{17}}, 26: {"ranged", []uint8{17}}, 28: {"relic", []uint8{17}}}

// proficiency spells: item class (2 weapon, 4 armor) -> subclass -> the skill spell you need to know
var proficiency = map[uint32]map[uint32]uint32{
	2: {0: 196, 1: 197, 2: 264, 3: 266, 4: 198, 5: 199, 6: 200, 7: 201, 8: 202, 10: 227, 13: 15590, 15: 1180, 16: 2567, 18: 5011, 19: 5009},
	4: {1: 9078, 2: 9077, 3: 8737, 4: 750, 6: 9116},
}

// usable: can this character equip and use the item? ("" = yes, else why not)
func usable(w *client.WorldClient, p *itemProto) string {
	if p.InvType == 0 {
		return "not equippable"
	}
	cls := uint32(0)
	if me := w.GetObject(w.CharGUID()); me != nil {
		cls = me.Value(client.UnitFieldBytes0) >> 8 & 0xFF
	}
	if p.allowClass != -1 && p.allowClass != 0 && cls > 0 && p.allowClass&(1<<(cls-1)) == 0 {
		return "not for your class"
	}
	if p.ReqLevel > w.PlayerLevel() {
		return fmt.Sprintf("requires level %d", p.ReqLevel)
	}
	if sp, ok := proficiency[p.Class][p.SubClass]; ok && !w.KnowsSpell(sp) {
		return "you lack the proficiency (armor type or weapon skill; trainers teach some weapon skills)"
	}
	return ""
}

var items = struct {
	sync.Mutex
	proto map[uint32]*itemProto // nil value: the server doesn't know the item
	asked map[uint32]time.Time
}{proto: map[uint32]*itemProto{}, asked: map[uint32]time.Time{}}

// itemInfo returns the cached template, asking the server if it isn't cached yet.
func itemInfo(w *client.WorldClient, entry uint32) *itemProto {
	items.Lock()
	defer items.Unlock()
	if p, ok := items.proto[entry]; ok {
		return p
	}
	if time.Since(items.asked[entry]) > 3*time.Second {
		items.asked[entry] = time.Now()
		w.SendPacketRaw(cmsgItemQuery, binary.LittleEndian.AppendUint32(nil, entry))
	}
	return nil
}

func parseItemQuery(data []byte) {
	r := bytes.NewReader(data)
	u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
	f32 := func() (v float32) { binary.Read(r, binary.LittleEndian, &v); return }
	entry := u32()
	items.Lock()
	defer items.Unlock()
	if entry&0x80000000 != 0 { // unknown item
		items.proto[entry&^0x80000000] = nil
		return
	}
	p := &itemProto{Entry: entry, Class: u32(), SubClass: u32()}
	u32() // sound override
	p.Name = readCString(r)
	readCString(r)
	readCString(r)
	readCString(r)
	u32() // display
	p.Quality = u32()
	u32()
	u32() // flags
	p.BuyPrice, p.SellPrice, p.InvType = u32(), u32(), u32()
	p.allowClass = int32(u32())
	u32() // allowable race
	p.ItemLevel, p.ReqLevel = u32(), u32()
	r.Seek(4*8, io.SeekCurrent) // skill, skill rank, spell, honor, city, rep faction, rep rank, max count
	p.Stack = int32(u32())
	u32() // container slots
	for n := u32(); n > 0 && n <= 10; n-- {
		t, v := u32(), int32(u32())
		if p.Stats == nil {
			p.Stats = map[string]int32{}
		}
		if name, ok := statNames[t]; ok {
			p.Stats[name] += v
		} else {
			p.Stats[fmt.Sprintf("stat%d", t)] += v
		}
	}
	r.Seek(8, io.SeekCurrent) // scaling stats
	var dmg float32
	for i := 0; i < 2; i++ {
		lo, hi := f32(), f32()
		u32()
		dmg += (lo + hi) / 2
	}
	p.Armor = u32()
	r.Seek(6*4, io.SeekCurrent) // resistances
	if delay := u32(); delay > 0 {
		p.DPS = math.Round(float64(dmg)/(float64(delay)/1000)*10) / 10
	}
	r.Seek(8, io.SeekCurrent) // ammo type, ranged range
	for i := 0; i < 5; i++ {
		id, trigger := u32(), u32()
		u32() // charges
		c := cdRule{rec: int32(u32()), cat: u32(), catrec: int32(u32())}
		if trigger == 0 && id != 0 && p.UseSpell == 0 { // ITEM_SPELLTRIGGER_ON_USE
			p.UseSpell = id
			c.onEvent = p.Class == 0 && p.SubClass == 1 // ItemTemplate::IsPotion: its cooldown starts out of combat
			p.useCD = c
		}
	}
	u32() // bonding
	readCString(r)
	r.Seek(12, io.SeekCurrent) // page text, language, page material
	p.StartQuest = u32()
	p.Slot = invSlots[p.InvType].name
	items.proto[entry] = p
}

// LootError (LootMgr.h)
var lootErrors = map[int][2]string{0: {"not_yours", "you don't have permission to loot it (someone else's kill, or nothing for you)"},
	4: {"out_of_range", "too far away: get within 5 yd"}, 5: {"bad_facing", "face it to loot it"},
	6: {"being_looted", "someone else is looting it"}, 8: {"not_standing", "stand up to loot"}, 9: {"stunned", "you're stunned"},
	10: {"player_not_found", "player not found"}, 11: {"play_time_exceeded", "maximum play time exceeded"},
	12: {"master_inventory_full", "that player's bags are full"}, 13: {"master_unique_item", "that player has too many of that item"},
	14: {"master_other", "can't assign the item to that player"}, 15: {"already_pickpocketed", "its pockets were already picked"},
	16: {"not_while_shapeshifted", "you can't loot while shapeshifted"}}

// handleLootResponse takes what the loot window lets you take, then the money, and releases (non-blocking for the
// read loop). Locked, master-looter and rolled-for slots stay: a roll arrives as SMSG_LOOT_START_ROLL (items.roll).
func (b *Bot) handleLootResponse(data []byte) {
	lr, err := client.ParseLootResponse(data)
	if err != nil {
		return
	}
	if lr.Error >= 0 {
		e, ok := lootErrors[lr.Error]
		if !ok {
			e = [2]string{fmt.Sprintf("loot_%d", lr.Error), "can't loot that"}
		}
		b.emit("error", types.ErrorEvent{Op: "items.loot", Code: e[0], Message: e[1]})
		return
	}
	for _, it := range lr.Items {
		if it.SlotType == 0 || it.SlotType == 4 { // LootSlotType: allow, owner (not roll ongoing, master looter's, locked)
			b.world.LootItem(it.Index)
		}
	}
	b.world.LootMoney()
	b.world.LootRelease(lr.GUID)
}

// rollChoices: RollType (LootMgr.h); a roll's vote mask has bit 1<<type for each choice allowed.
var rollChoices = []string{"pass", "need", "greed", "disenchant"}

func rollChoice(t uint8) string {
	if int(t) < len(rollChoices) {
		return rollChoices[t]
	}
	return fmt.Sprintf("roll_%d", t)
}

// lootRolls: group loot rolls by roll guid (SMSG_LOOT_START_ROLL's item guid), until won, all passed or expired.
type lootRolls struct {
	sync.Mutex
	m map[uint64]*pendingRoll
}

type pendingRoll struct {
	client.LootStartRoll
	until time.Time
	sent  bool // items.roll sent a choice
	voted bool // the server counted yours
}

func (s *lootRolls) start(r client.LootStartRoll) pendingRoll {
	p := pendingRoll{LootStartRoll: r, until: time.Now().Add(time.Duration(r.CountdownMS) * time.Millisecond)}
	s.Lock()
	defer s.Unlock()
	for g, q := range s.m {
		if time.Since(q.until) > 10*time.Second {
			delete(s.m, g)
		}
	}
	s.m[r.ItemGUID] = &p
	return p
}

// vote: SMSG_LOOT_ROLL's roll, choice and number (0 until the roll is counted). Group::CountRollVote sends each
// choice with an empty guid, need as number 0 type 0 and the others as number 128; CountTheRoll the numbers.
func (s *lootRolls) vote(r client.LootRollEvent, me uint64) (uint64, string, uint8) {
	s.Lock()
	defer s.Unlock()
	g := r.ItemGUID
	if g == 0 { // ponytail: two corpses' rolls for one item in one slot can't be told apart; yours goes to the one you rolled on
		for k, q := range s.m {
			if q.ItemSlot == r.ItemSlot && q.ItemID == r.ItemID && q.RandomSuffix == r.RandomSuffix && q.RandomPropID == r.RandomPropID &&
				(g == 0 || r.TargetGUID == me && q.sent && !q.voted) {
				g = k
			}
		}
	}
	if q := s.m[g]; q != nil && r.TargetGUID == me {
		q.voted = true
	}
	switch r.RollNumber {
	case 0:
		return g, "need", 0
	case 128:
		return g, rollChoice(r.RollType), 0
	}
	return g, rollChoice(r.RollType), r.RollNumber
}

func (s *lootRolls) sent(g uint64) {
	s.Lock()
	if p := s.m[g]; p != nil {
		p.sent = true
	}
	s.Unlock()
}

func (s *lootRolls) end(g uint64) {
	s.Lock()
	delete(s.m, g)
	s.Unlock()
}

func (s *lootRolls) get(g uint64) (pendingRoll, bool) {
	s.Lock()
	defer s.Unlock()
	if p := s.m[g]; p != nil {
		return *p, true
	}
	return pendingRoll{}, false
}

// open: the rolls you haven't chosen on yet, still counting down.
func (s *lootRolls) open() []pendingRoll {
	s.Lock()
	defer s.Unlock()
	var out []pendingRoll
	for _, p := range s.m {
		if !p.voted && time.Now().Before(p.until) {
			out = append(out, *p)
		}
	}
	return out
}

func (p pendingRoll) view(w *client.WorldClient) types.LootRoll {
	it := itemTemplate(w, p.ItemID)
	it.Count = p.ItemCount
	lr := types.LootRoll{Roll: types.GUID(p.ItemGUID), Item: it, Choices: []string{},
		SecondsLeft: math.Round(time.Until(p.until).Seconds()*10) / 10}
	for i, c := range rollChoices {
		if p.RollVoteMask&(1<<i) != 0 {
			lr.Choices = append(lr.Choices, c)
		}
	}
	return lr
}

type vendorItem struct {
	Slot  uint32 `json:"slot"`
	Entry uint32 `json:"entry"`
	Price uint32 `json:"price"` // copper per buy_count
	Stock int32  `json:"stock"` // -1 unlimited
	Count uint32 `json:"buy_count"`
	Name  string `json:"name,omitempty"`
}

type itemState struct {
	mu      sync.Mutex
	vendors map[uint64][]vendorItem
}

func (s *itemState) onPacket(op uint16, data []byte) {
	switch op {
	case smsgItemQuery:
		parseItemQuery(data)
	case smsgListInventory:
		r := bytes.NewReader(data)
		var guid uint64
		binary.Read(r, binary.LittleEndian, &guid)
		n, _ := r.ReadByte()
		list := []vendorItem{}
		for i := 0; i < int(n); i++ {
			var f [8]uint32
			binary.Read(r, binary.LittleEndian, &f)
			list = append(list, vendorItem{Slot: f[0], Entry: f[1], Stock: int32(f[3]), Price: f[4], Count: f[6]})
		}
		s.mu.Lock()
		s.vendors[guid] = list
		s.mu.Unlock()
	case smsgBuyItem: // vendor, slot (from 1), stock left (-1 unlimited), count (Player::BuyItemFromVendorSlot)
		if len(data) < 16 {
			return
		}
		guid, slot := binary.LittleEndian.Uint64(data), binary.LittleEndian.Uint32(data[8:])
		s.mu.Lock()
		for i, v := range s.vendors[guid] {
			if v.Slot == slot {
				s.vendors[guid][i].Stock = int32(binary.LittleEndian.Uint32(data[12:]))
			}
		}
		s.mu.Unlock()
	}
}

func (b *Bot) setupItems() {
	s := &itemState{vendors: map[uint64][]vendorItem{}}
	w := b.world
	w.AddPacketHook(func(op uint16, data []byte) { s.onPacket(op, data) })
	e := b.lua

	// ---------- items ----------
	e.Func("items.list", "() → Item[]", "everything you wear and carry (equipped, bags, backpack)", func() []types.Item { return bagItems(w) })
	e.Func("items.count", "(entry) → number", "how many of an item you carry", func(entry uint32) uint32 {
		n := uint32(0)
		for _, it := range bagItems(w) {
			if it.Entry == entry {
				n += it.Count
			}
		}
		return n
	})
	e.Func("items.info", "(entry) → Item?", "an item's template: stats, slot, prices, whether you can use it; nil the first time (it asks the server: call again a moment later)",
		func(entry uint32) any {
			if it := itemTemplate(w, entry); it.Name != "" {
				return it
			}
			return nil
		})
	e.Func("items.broken", "() → Item[]", "equipped items at 0 durability (they give nothing; a broken weapon means fighting bare-handed)", func() []types.Item {
		out := []types.Item{}
		for _, it := range bagItems(w) {
			if it.Equipped && it.Broken {
				out = append(out, it)
			}
		}
		return out
	})
	e.Func("items.free_slots", "() → number", "empty bag slots; at 0, loot and item rewards are refused", func() int { return freeSlots(w) })
	e.Func("items.compare", "(item) → Comparison?", "an item (entry, or the guid of one you carry) against what you wear in its slot (the weaker one for rings, trinkets and one-handers); nil until the items' info has loaded",
		func(item uint64) any {
			if c := b.compareItem(item); c != nil {
				return *c
			}
			return nil
		})
	e.Func("items.use", "(item [, guid]) → Request<CastDone>", "use an item you carry (entry or guid): food, drink, potions, quest items; optionally on a unit. Food and drink work out of combat, and you must sit still while they tick",
		func(item, target uint64) map[string]any {
			args := map[string]any{"item": gs(item)}
			it := findItem(w, item)
			if it == nil {
				return b.refuse("items.use", args, "item_not_found", "no such item in your bags")
			}
			if it.Name == "" {
				return b.refuse("items.use", args, "not_loaded", "item info not loaded yet, try again")
			}
			if it.UseSpell == 0 {
				return b.refuse("items.use", args, "no_use_effect", it.Name+" has no use effect")
			}
			var buf bytes.Buffer
			buf.Write([]byte{it.Bag, it.BagSlot, 1})
			binary.Write(&buf, binary.LittleEndian, it.UseSpell)
			binary.Write(&buf, binary.LittleEndian, uint64(it.GUID))
			binary.Write(&buf, binary.LittleEndian, uint32(0)) // glyph index
			buf.WriteByte(0)                                   // cast flags
			writeTarget(&buf, target)
			b.agent.castSent(it.UseSpell)
			w.SendPacketRaw(cmsgUseItem, buf.Bytes())
			return b.request("items.use", args, castTimeout(it.UseSpell), nil, castMatch(it.UseSpell)) // a Hearthstone takes 10 s
		})
	e.Func("items.equip", "(item [, slot]) → Request<Equipped>", "wear an item you carry (entry or guid) in its slot, or in the slot you name: main_hand, off_hand (a second one-hander, with Dual Wield), finger1, finger2, trinket1, trinket2",
		func(item uint64, slot string) map[string]any {
			args := map[string]any{"item": gs(item)}
			it := findItem(w, item)
			if it == nil {
				return b.refuse("items.equip", args, "item_not_found", "no such item in your bags")
			}
			if it.Name != "" && !it.Usable {
				return b.refuse("items.equip", args, "not_usable", it.WhyNot)
			}
			g := it.GUID
			want := -1
			if slot != "" {
				args["slot"] = slot
				idx, ok := equipSlots[slot]
				if !ok {
					return b.refuse("items.equip", args, "bad_slot", "slot: one of "+strings.Join(slices.Sorted(maps.Keys(equipSlots)), ", "))
				}
				if p := itemInfo(w, it.Entry); p != nil && !slices.Contains(invSlots[p.InvType].slots, idx) {
					return b.refuse("items.equip", args, "wrong_slot", fmt.Sprintf("a %s item doesn't go in %s", invSlots[p.InvType].name, slot))
				}
				want = int(idx)
				w.SendPacketRaw(cmsgAutoEquipItemSlot, append(binary.LittleEndian.AppendUint64(nil, uint64(g)), idx))
			} else {
				w.AutoEquipItem(it.Bag, it.BagSlot)
			}
			return b.request("items.equip", args, 3*time.Second, func() *script.Outcome {
				for _, x := range bagItems(w) {
					if x.GUID == g && x.Equipped && (want < 0 || int(x.BagSlot) == want) {
						return script.Ok(types.Equipped{Slot: x.Slot})
					}
				}
				return nil
			}, func(ev script.Event) *script.Outcome { return failOn(ev, "items") })
		})
	e.Func("items.unequip", "(slot) → Request", "take off what you wear in a slot (head, chest, main_hand, off_hand, finger1, trinket2, ...) into your bags",
		func(slot string) map[string]any {
			args := map[string]any{"slot": slot}
			idx, ok := equipSlots[slot]
			if !ok {
				return b.refuse("items.unequip", args, "bad_slot", "slot: one of "+strings.Join(slices.Sorted(maps.Keys(equipSlots)), ", "))
			}
			var g types.GUID
			for _, x := range bagItems(w) {
				if x.Equipped && x.BagSlot == idx {
					g = x.GUID
				}
			}
			if g == 0 {
				return b.refuse("items.unequip", args, "empty_slot", "you wear nothing there")
			}
			if freeSlots(w) == 0 {
				return b.refuse("items.unequip", args, "bags_full", "no room in your bags")
			}
			w.SendPacketRaw(cmsgAutoStoreBagItem, []byte{255, idx, 0}) // dst NULL_BAG: any bag with room (255 is the backpack only)
			return b.request("items.unequip", args, 3*time.Second, func() *script.Outcome {
				for _, x := range bagItems(w) {
					if x.GUID == g && !x.Equipped {
						return script.Ok(nil)
					}
				}
				return nil
			}, func(ev script.Event) *script.Outcome { return failOn(ev, "items") })
		})
	e.Func("items.destroy", "(item [, count = all]) → Request", "destroy an item you carry (entry or guid): gone for good", func(item uint64, count uint32) map[string]any {
		args := map[string]any{"item": gs(item)}
		it := findItem(w, item)
		if it == nil {
			return b.refuse("items.destroy", args, "item_not_found", "no such item in your bags")
		}
		if count == 0 || count >= it.Count {
			count = 0 // the whole stack
		}
		count = min(count, 255)
		g, left := it.GUID, it.Count-count
		w.SendPacketRaw(cmsgDestroyItem, []byte{it.Bag, it.BagSlot, byte(count), 0, 0, 0})
		return b.request("items.destroy", args, 3*time.Second, func() *script.Outcome {
			for _, x := range bagItems(w) {
				if x.GUID == g && (count == 0 || x.Count > left) {
					return nil
				}
			}
			return script.Ok(nil)
		}, func(ev script.Event) *script.Outcome { return failOn(ev, "items") })
	})
	e.Func("items.loot", "(guid) → Request<Looted>", "loot a corpse within 5 yd: takes every item and the money (waits a moment for a fresh corpse to become lootable)",
		func(guid uint64) map[string]any {
			args := map[string]any{"unit": gs(guid)}
			full := freeSlots(w) == 0
			u := b.GetUnitInfo(guid)
			if u != nil && !u.IsPlayer && u.Distance > 5 {
				return b.refuse("items.loot", args, "out_of_range", fmt.Sprintf("%.0f yd away: get within 5 yd", u.Distance))
			}
			if u != nil && !u.IsPlayer && u.IsAlive {
				return b.refuse("items.loot", args, "not_lootable", "it's alive")
			}
			got, last, start, sent := types.Looted{Items: []types.Item{}}, time.Now(), time.Now(), false
			var mu sync.Mutex
			send := func() { b.LootAll(guid); sent, last = true, time.Now() }
			if u == nil || u.IsPlayer || u.Lootable {
				send()
			}
			return b.request("items.loot", args, 10*time.Second, func() *script.Outcome {
				mu.Lock()
				defer mu.Unlock()
				if !sent { // a fresh corpse turns lootable a moment after it dies
					if v := b.GetUnitInfo(guid); v != nil && v.Lootable {
						send()
					} else if time.Since(start) > 2*time.Second {
						return script.Fail("not_lootable", "nothing to loot on it for you (already looted, no drops, or someone else's kill)", nil)
					}
					return nil
				}
				if time.Since(last) < 1500*time.Millisecond {
					return nil
				}
				switch {
				case len(got.Items) > 0 || got.Money > 0:
					return script.Ok(got)
				case full:
					return script.Fail("bags_full", "bags full: nothing looted; sell or destroy something", nil)
				}
				return script.Fail("nothing_looted", "no loot: too far, already looted, or not yours", nil)
			}, func(ev script.Event) *script.Outcome {
				mu.Lock()
				defer mu.Unlock()
				switch d := ev.Data.(type) {
				case types.ItemReceived:
					if d.From != "loot" {
						return nil
					}
					got.Items = append(got.Items, d.Item)
				case types.Money:
					got.Money += d.Amount
				case types.ErrorEvent:
					if (d.Op != "items" && d.Op != "items.loot") || d.Code == "already_looted" { // already_looted: a slot someone else (or a roll) took meanwhile
						return nil
					}
					return script.Fail(d.Code, d.Message, got)
				default:
					return nil
				}
				last = time.Now()
				return nil
			})
		})

	// ---------- group loot rolls ----------
	rolls := &lootRolls{m: map[uint64]*pendingRoll{}}
	w.AddLootStartRollHook(func(r client.LootStartRoll) { b.emit("loot_roll_start", rolls.start(r).view(w)) })
	w.AddLootRollHook(func(r client.LootRollEvent) {
		g, choice, n := rolls.vote(r, w.CharGUID())
		b.emit("loot_roll", types.LootRollResult{Roll: types.GUID(g), Item: itemTemplate(w, r.ItemID), Player: b.ref(r.TargetGUID),
			Choice: choice, Number: n, Auto: r.AutoPass != 0})
	})
	w.AddLootRollWonHook(func(r client.LootRollWon) {
		rolls.end(r.ItemGUID)
		b.emit("loot_roll_won", types.LootRollResult{Roll: types.GUID(r.ItemGUID), Item: itemTemplate(w, r.ItemID),
			Player: b.ref(r.WinnerGUID), Choice: rollChoice(r.RollType), Number: r.RollNumber})
	})
	w.AddLootAllPassedHook(func(r client.LootAllPassed) {
		rolls.end(r.ItemGUID)
		b.emit("loot_roll_all_passed", types.LootRollResult{Roll: types.GUID(r.ItemGUID), Item: itemTemplate(w, r.ItemID), Choice: "pass"})
	})
	e.Func("items.rolls", "() → LootRoll[]", "group loot waiting for your roll (also in me.status().loot_rolls)", func() []types.LootRoll {
		out := []types.LootRoll{}
		for _, p := range rolls.open() {
			out = append(out, p.view(w))
		}
		sortBy(out, func(r types.LootRoll) float64 { return r.SecondsLeft })
		return out
	})
	e.Func("items.roll", "(roll, choice) → Request<LootRollResult>", "roll on group loot (loot_roll_start event, items.rolls): need, greed, disenchant or pass. No choice before the countdown ends is a pass",
		func(roll uint64, choice string) map[string]any {
			args := map[string]any{"roll": gs(roll), "choice": choice}
			n := slices.Index(rollChoices, choice)
			p, ok := rolls.get(roll)
			switch {
			case n < 0:
				return b.refuse("items.roll", args, "bad_choice", "choice: need, greed, disenchant or pass")
			case !ok || time.Now().After(p.until):
				return b.refuse("items.roll", args, "no_such_roll", "no open roll with that id: items.rolls() lists them")
			case n != 0 && p.RollVoteMask&(1<<n) == 0:
				return b.refuse("items.roll", args, "not_allowed", "you may choose: "+strings.Join(p.view(w).Choices, ", "))
			}
			rolls.sent(roll)
			if err := w.LootRoll(roll, p.ItemSlot, uint8(n)); err != nil {
				return b.refuse("items.roll", args, "failed", err.Error())
			}
			return b.request("items.roll", args, 3*time.Second, nil, func(ev script.Event) *script.Outcome {
				if d, ok := ev.Data.(types.LootRollResult); ok && ev.Type == "loot_roll" && d.Player.Me && uint64(d.Roll) == roll {
					return script.Ok(d)
				}
				return nil
			})
		})

	// ---------- objects ----------
	e.Func("objects.use", "(guid [, spell]) → Request<CastDone>", "use a game object within ~5 yd: doors, quest objects, chests and crates (opened with the Opening spell like the real client; the loot is taken). Plain objects resolve at once: watch for item_received / quest_progress",
		func(guid uint64, spell uint32) map[string]any {
			args := map[string]any{"object": gs(guid)}
			o := w.GetObject(guid)
			if o == nil {
				return b.refuse("objects.use", args, "not_found", "no such object nearby")
			}
			if spell == 0 && (o.Value(fieldGOBytes1)>>8)&0xFF == goTypeChest {
				spell = spellOpening
			}
			if spell == 0 {
				return b.sent("objects.use", args, w.GameObjectUse(guid))
			}
			var buf bytes.Buffer
			buf.WriteByte(0)
			binary.Write(&buf, binary.LittleEndian, spell)
			buf.WriteByte(0)
			binary.Write(&buf, binary.LittleEndian, uint32(0x800)) // TARGET_FLAG_GAMEOBJECT
			buf.Write(packGUID(guid))
			b.agent.castSent(spell)
			w.SendPacketRaw(cmsgCastSpellOp, buf.Bytes())
			return b.request("objects.use", args, 10*time.Second, nil, castMatch(spell))
		})

	// ---------- vendor ----------
	e.Func("vendor.items", "(npc) → VendorItem[]?", "what a vendor sells; nil the first time (it asks the vendor: call again a moment later)", func(vendor uint64) any {
		s.mu.Lock()
		list, ok := s.vendors[vendor]
		s.mu.Unlock()
		if !ok || slices.ContainsFunc(list, func(v vendorItem) bool { return v.Stock >= 0 }) {
			w.SendPacketRaw(cmsgListInventory, binary.LittleEndian.AppendUint64(nil, vendor)) // limited stock is only recounted on a list request
		}
		if !ok {
			return nil
		}
		out := []types.VendorItem{}
		for _, v := range list {
			out = append(out, types.VendorItem{Item: itemTemplate(w, v.Entry), VendorSlot: v.Slot, Price: v.Price, BuyCount: v.Count, Stock: v.Stock})
		}
		return out
	})
	e.Func("vendor.buy", "(npc, entry [, count = 1]) → Request<ItemReceived>", "buy from a vendor within ~5 yd (count in purchases of buy_count each). Call vendor.items first",
		func(vendor uint64, entry, count uint32) map[string]any {
			args := map[string]any{"npc": gs(vendor), "item": entry, "count": max(count, 1)}
			if r := b.noNPC("vendor.buy", args, &vendor); r != nil {
				return r
			}
			if r := b.noRole("vendor.buy", args, vendor, roleVendor, "a vendor"); r != nil {
				return r
			}
			s.mu.Lock()
			list, ok := s.vendors[vendor]
			s.mu.Unlock()
			if !ok {
				w.SendPacketRaw(cmsgListInventory, binary.LittleEndian.AppendUint64(nil, vendor))
				return b.refuse("vendor.buy", args, "vendor_list_not_loaded", "call vendor.items(npc) first, then buy again")
			}
			for _, v := range list {
				if v.Entry == entry {
					if v.Stock == 0 {
						w.SendPacketRaw(cmsgListInventory, binary.LittleEndian.AppendUint64(nil, vendor))
						return b.refuse("vendor.buy", args, "sold_out", "sold out: limited stock comes back over time; this asked for a fresh count, buy again in a moment to retry")
					}
					body := binary.LittleEndian.AppendUint64(nil, vendor)
					body = binary.LittleEndian.AppendUint32(body, entry)
					body = binary.LittleEndian.AppendUint32(body, v.Slot)
					body = binary.LittleEndian.AppendUint32(body, max(count, 1))
					w.SendPacketRaw(cmsgBuyItem, append(body, 0))
					return b.request("vendor.buy", args, 5*time.Second, nil, func(ev script.Event) *script.Outcome {
						if d, ok := ev.Data.(types.ItemReceived); ok && d.Item.Entry == entry {
							return script.Ok(d)
						}
						if o := failOn(ev, "vendor.buy"); o != nil {
							return o
						}
						return failOn(ev, "items")
					})
				}
			}
			return b.refuse("vendor.buy", args, "not_sold_here", fmt.Sprintf("this vendor doesn't sell item %d", entry))
		})
	sell := func(vendor uint64, it types.Item, count uint32) {
		body := binary.LittleEndian.AppendUint64(nil, vendor)
		body = binary.LittleEndian.AppendUint64(body, uint64(it.GUID))
		w.SendPacketRaw(cmsgSellItem, binary.LittleEndian.AppendUint32(body, count))
	}
	moneyUp := func(before uint32) func() *script.Outcome {
		return func() *script.Outcome {
			if w.Money() > before {
				return script.Ok(types.Sold{Money: w.Money() - before})
			}
			return nil
		}
	}
	e.Func("vendor.sell", "(npc, item [, count = all]) → Request<Sold>", "sell an item you carry (entry or guid) to a vendor within ~5 yd", func(vendor, item uint64, count uint32) map[string]any {
		args := map[string]any{"npc": gs(vendor), "item": gs(item)}
		if r := b.noNPC("vendor.sell", args, &vendor); r != nil {
			return r
		}
		if r := b.noRole("vendor.sell", args, vendor, roleVendor, "a vendor"); r != nil {
			return r
		}
		it := findItem(w, item)
		if it == nil {
			return b.refuse("vendor.sell", args, "item_not_found", "no such item in your bags")
		}
		if it.Name != "" && it.SellPrice == 0 {
			return b.refuse("vendor.sell", args, "no_value", it.Name+" has no sell value")
		}
		if count == 0 {
			count = it.Count
		}
		before := w.Money()
		sell(vendor, *it, count)
		return b.request("vendor.sell", args, 3*time.Second, moneyUp(before), func(ev script.Event) *script.Outcome { return failOn(ev, "vendor.sell") })
	})
	e.Func("vendor.sell_junk", "(npc) → Request<Sold>", "sell every grey (poor) item you carry. Items whose info hasn't loaded are skipped", func(vendor uint64) map[string]any {
		args := map[string]any{"npc": gs(vendor)}
		if r := b.noNPC("vendor.sell_junk", args, &vendor); r != nil {
			return r
		}
		if r := b.noRole("vendor.sell_junk", args, vendor, roleVendor, "a vendor"); r != nil {
			return r
		}
		before, sold := w.Money(), map[types.GUID]bool{}
		for _, it := range bagItems(w) {
			if inBags(it) && it.Quality == "poor" && it.SellPrice > 0 {
				sell(vendor, it, it.Count)
				sold[it.GUID] = true
			}
		}
		if len(sold) == 0 {
			return b.done("vendor.sell_junk", args, types.Sold{})
		}
		var mu sync.Mutex
		var failed []script.Outcome
		// every stack answered: gone from the bags, or refused (SMSG_SELL_ITEM error)
		return b.request("vendor.sell_junk", args, 3*time.Second, func() *script.Outcome {
			gone := len(sold)
			for _, x := range bagItems(w) {
				if sold[x.GUID] {
					gone--
				}
			}
			mu.Lock()
			defer mu.Unlock()
			switch {
			case gone+len(failed) < len(sold):
				return nil
			case gone == 0:
				return &failed[len(failed)-1]
			case w.Money() <= before: // the money update can trail the item's removal
				return nil
			}
			return script.Ok(types.Sold{Money: w.Money() - before, Count: gone, Failed: len(failed)})
		}, func(ev script.Event) *script.Outcome {
			if o := failOn(ev, "vendor.sell"); o != nil {
				mu.Lock()
				failed = append(failed, *o)
				mu.Unlock()
			}
			return nil
		})
	})
	e.Func("vendor.repair", "(npc) → Request<Repaired>", "repair everything you wear and carry at an NPC that repairs (role repair: armorers, weaponsmiths) within ~5 yd",
		func(npc uint64) map[string]any {
			args := map[string]any{"npc": gs(npc)}
			if r := b.noNPC("vendor.repair", args, &npc); r != nil {
				return r
			}
			if r := b.noRole("vendor.repair", args, npc, roleRepair, "an NPC that repairs"); r != nil {
				return r
			}
			damaged := func() int {
				n := 0
				for _, it := range bagItems(w) {
					if it.MaxDurability > 0 && it.Durability < it.MaxDurability {
						n++
					}
				}
				return n
			}
			before, money := damaged(), w.Money()
			if before == 0 {
				return b.done("vendor.repair", args, types.Repaired{})
			}
			body := binary.LittleEndian.AppendUint64(nil, npc)
			body = binary.LittleEndian.AppendUint64(body, 0) // 0 = all items
			w.SendPacketRaw(0x2A8, append(body, 0))          // CMSG_REPAIR_ITEM; no guild bank
			// the server says nothing about a repair: compare durability a moment later
			start := time.Now()
			return b.request("vendor.repair", args, 5*time.Second, func() *script.Outcome {
				if time.Since(start) < 1500*time.Millisecond {
					return nil
				}
				after := damaged()
				if after < before {
					return script.Ok(types.Repaired{Fixed: before - after, Cost: int64(money) - int64(w.Money()), StillDamaged: after})
				}
				return script.Fail("not_repaired", fmt.Sprintf("%d item(s) still damaged: stand within ~5 yd of an NPC that repairs, with enough money (you have %dc)", after, w.Money()), nil)
			}, nil)
		})

	// ---------- trainer ----------
	e.Func("trainer.learn", "(npc, spell) → Request<Learned>", "learn a spell from a trainer within ~5 yd (costs money; trainer.list shows what it teaches you and what you can learn now)",
		func(trainer uint64, spell uint32) map[string]any {
			if r := b.noNPC("trainer.learn", map[string]any{"npc": gs(trainer), "spell": spell}, &trainer); r != nil {
				return r
			}
			if r := b.noRole("trainer.learn", map[string]any{"npc": gs(trainer), "spell": spell}, trainer, roleTrainer, "a trainer"); r != nil {
				return r
			}
			body := binary.LittleEndian.AppendUint64(nil, trainer)
			w.SendPacketRaw(0x1B2, binary.LittleEndian.AppendUint32(body, spell)) // CMSG_TRAINER_BUY_SPELL
			return b.request("trainer.learn", map[string]any{"npc": gs(trainer), "spell": spell}, 5*time.Second, nil,
				func(ev script.Event) *script.Outcome {
					if d, ok := ev.Data.(types.Learned); ok && d.Spell == spell {
						return script.Ok(d)
					}
					return failOn(ev, "trainer.learn")
				})
		})

	// ---------- world ----------
	e.Func("world.path", "(x, y, z) → Path?", "the walking route from you: its length, and the gap between where it ends and the point (over a few yards: unreachable). nil if there's no path at all",
		func(x, y, z float32) any {
			if b.nav == nil {
				return nil
			}
			px, py, pz, _, m := w.Position()
			res, err := b.nav.FindPath(m, navigation.Point3D{X: px, Y: py, Z: pz}, navigation.Point3D{X: x, Y: y, Z: z})
			if err != nil || res == nil || len(res.Points) == 0 {
				return nil
			}
			length, prev := 0.0, navigation.Point3D{X: px, Y: py, Z: pz}
			for _, p := range res.Points {
				length += dist3(prev, p)
				prev = p
			}
			return types.Path{Distance: math.Round(length*10) / 10, Gap: math.Round(dist3(prev, navigation.Point3D{X: x, Y: y, Z: z})*10) / 10}
		})
}

// compareItem: an item against what you wear in its slot; nil until the items' info has loaded.
func (b *Bot) compareItem(item uint64) *types.Comparison {
	w := b.world
	entry := uint32(item)
	if item >= 1<<32 {
		o := w.GetObject(item)
		if o == nil {
			return nil
		}
		entry = o.Value(3)
	}
	p := itemInfo(w, entry)
	if p == nil {
		return nil
	}
	cmp := types.Comparison{Item: itemTemplate(w, entry), Stats: map[string]int32{}}
	slots := invSlots[p.InvType].slots
	if len(slots) == 0 {
		return &cmp
	}
	if p.InvType == 13 && !w.KnowsSpell(674) { // one-hand: off hand only with Dual Wield
		slots = slots[:1]
	}
	me := w.GetObject(w.CharGUID())
	if me == nil {
		return nil
	}
	// what it would replace: an empty slot, else the lowest item level
	var cur *itemProto
	var curEntry uint32
	for _, sl := range slots {
		o := w.GetObject(guidAt(me, fieldInvSlots+uint16(sl)*2))
		if o == nil {
			cur = nil
			break
		}
		cp := itemInfo(w, o.Value(3))
		if cp == nil {
			return nil
		}
		if cur == nil || cp.ItemLevel < cur.ItemLevel {
			cur, curEntry = cp, o.Value(3)
		}
	}
	for k, v := range p.Stats {
		cmp.Stats[k] = v
	}
	base := &itemProto{}
	if cur != nil {
		base = cur
		r := itemTemplate(w, curEntry)
		cmp.Replaces = &r
		for k, v := range cur.Stats {
			cmp.Stats[k] -= v
		}
	}
	for k, v := range cmp.Stats {
		if v == 0 {
			delete(cmp.Stats, k)
		}
	}
	cmp.ItemLevel, cmp.Armor = int32(p.ItemLevel)-int32(base.ItemLevel), int32(p.Armor)-int32(base.Armor)
	cmp.DPS = math.Round((p.DPS-base.DPS)*10) / 10
	return &cmp
}

func dist3(a, b navigation.Point3D) float64 {
	dx, dy, dz := float64(a.X-b.X), float64(a.Y-b.Y), float64(a.Z-b.Z)
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

func writeTarget(buf *bytes.Buffer, target uint64) {
	if target == 0 {
		binary.Write(buf, binary.LittleEndian, uint32(0)) // self
		return
	}
	binary.Write(buf, binary.LittleEndian, uint32(0x2)) // TARGET_FLAG_UNIT
	buf.Write(packGUID(target))
}

func freeSlots(w *client.WorldClient) int {
	me := w.GetObject(w.CharGUID())
	if me == nil {
		return 0
	}
	n := 0
	for slot := uint16(23); slot < 39; slot++ {
		if guidAt(me, fieldInvSlots+slot*2) == 0 {
			n++
		}
	}
	for bag := uint16(19); bag < 23; bag++ {
		if c := w.GetObject(guidAt(me, fieldInvSlots+bag*2)); c != nil {
			for slot := uint16(0); uint32(slot) < c.Value(fieldContainerSize) && slot < 36; slot++ {
				if guidAt(c, fieldContainerSlot+slot*2) == 0 {
					n++
				}
			}
		}
	}
	return n
}
