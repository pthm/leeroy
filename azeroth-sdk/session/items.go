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
	"math"
	"sync"
	"time"

	"github.com/pthm/wowbench/azeroth-sdk/client"
	"github.com/pthm/wowbench/azeroth-sdk/navigation"
	"github.com/pthm/wowbench/azeroth-sdk/script"
)

const (
	cmsgItemQuery     = 0x056
	smsgItemQuery     = 0x058
	cmsgUseItem       = 0x0AB
	cmsgDestroyItem   = 0x111
	cmsgListInventory = 0x19E
	smsgListInventory = 0x19F
	cmsgSellItem      = 0x1A0
	cmsgBuyItem       = 0x1A2
	cmsgCastSpellOp   = 0x12E

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
}

var statNames = map[uint32]string{0: "mana", 1: "health", 3: "agility", 4: "strength", 5: "intellect", 6: "spirit", 7: "stamina",
	12: "defense", 13: "dodge", 14: "parry", 15: "block", 16: "melee_hit", 17: "ranged_hit", 18: "spell_hit", 19: "melee_crit",
	20: "ranged_crit", 21: "spell_crit", 28: "melee_haste", 31: "hit", 32: "crit", 35: "resilience", 36: "haste", 37: "expertise",
	38: "attack_power", 39: "ranged_attack_power", 41: "healing", 42: "spell_damage", 43: "mana_per_5s", 44: "armor_penetration",
	45: "spell_power", 46: "health_per_5s", 47: "spell_penetration", 48: "block_value"}

// inventory type -> slot name and the equipment slots (0-18) it can go in
var invSlots = map[uint32]struct {
	name  string
	slots []uint8
}{1: {"head", []uint8{0}}, 2: {"neck", []uint8{1}}, 3: {"shoulder", []uint8{2}}, 4: {"shirt", []uint8{3}}, 5: {"chest", []uint8{4}},
	20: {"chest", []uint8{4}}, 6: {"waist", []uint8{5}}, 7: {"legs", []uint8{6}}, 8: {"feet", []uint8{7}}, 9: {"wrists", []uint8{8}},
	10: {"hands", []uint8{9}}, 11: {"finger", []uint8{10, 11}}, 12: {"trinket", []uint8{12, 13}}, 13: {"one-hand", []uint8{15, 16}},
	14: {"shield", []uint8{16}}, 15: {"ranged", []uint8{17}}, 16: {"back", []uint8{14}}, 17: {"two-hand", []uint8{15}},
	19: {"tabard", []uint8{18}}, 21: {"main hand", []uint8{15}}, 22: {"off hand", []uint8{16}}, 23: {"held in off-hand", []uint8{16}},
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
		r.Seek(16, io.SeekCurrent)                      // charges, cooldowns
		if trigger == 0 && id != 0 && p.UseSpell == 0 { // ITEM_SPELLTRIGGER_ON_USE
			p.UseSpell = id
		}
	}
	u32() // bonding
	readCString(r)
	r.Seek(16, io.SeekCurrent) // page text, language, page material
	p.StartQuest = u32()
	p.Slot = invSlots[p.InvType].name
	items.proto[entry] = p
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
	}
}

// findItem: an item in your bags (not equipped) by guid or, when key is small, by entry.
func findItem(w *client.WorldClient, key uint64) map[string]any {
	for _, it := range bagItems(w) {
		if bag, slot := it["bag"].(uint8), it["slot"].(uint8); bag == 255 && slot < 23 {
			continue
		}
		if it["guid"] == fmt.Sprint(key) || (key < 1<<32 && it["entry"] == uint32(key)) {
			return it
		}
	}
	return nil
}

func guidOf(it map[string]any) (g uint64) {
	fmt.Sscan(it["guid"].(string), &g)
	return
}

func (b *Bot) setupItems() {
	s := &itemState{vendors: map[uint64][]vendorItem{}}
	w := b.world
	w.AddPacketHook(func(op uint16, data []byte) { s.onPacket(op, data) })
	e := b.lua

	// loot_all(guid): as before, plus a warning when there's no bag space (items stay on the corpse)
	// loot_all(guid) -> request "loot": {items = {{item, name, count}...}, money} after the loot window closes
	e.RegisterFunc("loot_all", func(guid uint64) map[string]any {
		args := map[string]any{"guid": fmt.Sprint(guid)}
		full := freeSlots(w) == 0
		u := b.GetUnitInfo(guid)
		if u != nil && !u.IsPlayer && u.Distance > 5 {
			return b.refuse("loot", args, "out_of_range", fmt.Sprintf("%.0f yd away: get within 5 yd", u.Distance))
		}
		if u != nil && !u.IsPlayer && u.IsAlive {
			return b.refuse("loot", args, "not_lootable", "it's alive")
		}
		got, money, last, start, sent := []any{}, uint32(0), time.Now(), time.Now(), false
		var mu sync.Mutex
		send := func() { b.LootAll(guid); sent, last = true, time.Now() }
		// a fresh corpse turns lootable a moment after it dies: wait up to 2s for the flag before asking
		if u == nil || u.IsPlayer || u.Lootable {
			send()
		}
		return b.request("loot", args, 10*time.Second, func() *script.Outcome {
			mu.Lock()
			defer mu.Unlock()
			if !sent {
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
			case len(got) > 0 || money > 0:
				return script.Ok(map[string]any{"items": got, "money": money})
			case full:
				return script.Fail("bags_full", "bags full: nothing looted; sell or destroy something", nil)
			}
			return script.Fail("nothing_looted", "no loot: too far (~5 yd), not lootable, already looted, or not yours", nil)
		}, func(ev script.Event) *script.Outcome {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case ev.Type == "item_received" && ev.Data["from"] == "loot":
				got = append(got, ev.Data)
			case ev.Type == "money" && ev.Data["source"] == "loot":
				money += ev.Data["amount"].(uint32)
			case ev.Type == "error" && ev.Data["op"] == "inventory":
				return script.Fail(fmt.Sprint(ev.Data["code"]), fmt.Sprint(ev.Data["message"]), map[string]any{"items": got})
			default:
				return nil
			}
			last = time.Now()
			return nil
		})
	})
	e.RegisterFunc("item_info", func(entry uint32) any {
		p := itemInfo(w, entry)
		if p == nil {
			return nil
		}
		m := map[string]any{"entry": p.Entry, "name": p.Name, "quality": p.Quality, "class": p.Class, "subclass": p.SubClass,
			"inv_type": p.InvType, "slot": p.Slot, "item_level": p.ItemLevel, "required_level": p.ReqLevel, "buy_price": p.BuyPrice,
			"sell_price": p.SellPrice, "max_stack": p.Stack, "armor": p.Armor, "dps": p.DPS, "use_spell": p.UseSpell,
			"start_quest": p.StartQuest, "stats": p.Stats}
		if p.InvType != 0 {
			why := usable(w, p)
			m["usable"] = why == ""
			if why != "" {
				m["why_not"] = why
			}
		}
		return m
	})
	// compare(item): an item (entry, or guid from bag_items) against what you wear in that slot (the weaker one for
	// rings, trinkets and one-handers): changes in item_level, stats, armor and dps if you swapped, whether you can
	// use it, and what it would replace. nil until the items' info has loaded (call again a moment later).
	e.RegisterFunc("compare", func(item uint64) any {
		entry := uint32(item)
		if item >= 1<<32 {
			it := w.GetObject(item)
			if it == nil {
				return nil
			}
			entry = it.Value(3)
		}
		p := itemInfo(w, entry)
		if p == nil {
			return nil
		}
		slots := invSlots[p.InvType].slots
		if len(slots) == 0 {
			return map[string]any{"usable": false, "why_not": "not equippable"}
		}
		if p.InvType == 13 && !w.KnowsSpell(674) { // one-hand: off hand only with Dual Wield
			slots = slots[:1]
		}
		me := w.GetObject(w.CharGUID())
		if me == nil {
			return nil
		}
		// the slot it would replace: an empty one, else the lowest item level
		var cur *itemProto
		curSlot, curEntry, found := slots[0], uint32(0), false
		for _, sl := range slots {
			o := w.GetObject(guidAt(me, fieldInvSlots+uint16(sl)*2))
			if o == nil {
				cur, curSlot, curEntry, found = nil, sl, 0, true
				break
			}
			cp := itemInfo(w, o.Value(3))
			if cp == nil {
				return nil
			}
			if !found || cp.ItemLevel < cur.ItemLevel {
				cur, curSlot, curEntry, found = cp, sl, o.Value(3), true
			}
		}
		why := usable(w, p)
		d := map[string]any{"slot": p.Slot, "equip_slot": curSlot, "usable": why == ""}
		if why != "" {
			d["why_not"] = why
		}
		stats := map[string]int32{}
		for k, v := range p.Stats {
			stats[k] = v
		}
		base := &itemProto{}
		if cur != nil {
			base = cur
			d["replaces"] = map[string]any{"entry": curEntry, "name": cur.Name, "item_level": cur.ItemLevel}
			for k, v := range cur.Stats {
				stats[k] -= v
			}
		} else {
			d["replaces"] = "nothing (empty slot)"
		}
		for k, v := range stats {
			if v == 0 {
				delete(stats, k)
			}
		}
		d["item_level"], d["armor"] = int(p.ItemLevel)-int(base.ItemLevel), int(p.Armor)-int(base.Armor)
		d["dps"], d["stats"] = math.Round((p.DPS-base.DPS)*10)/10, stats
		return d
	})
	// free_slots() -> empty slots in the backpack and equipped bags. At 0, loot and quest rewards are refused.
	e.RegisterFunc("free_slots", func() int { return freeSlots(w) })
	// sell(vendor, item [, count]): item = guid or entry; count defaults to the whole stack
	e.RegisterFunc("sell", func(vendor, item uint64, count uint32) map[string]any {
		args := map[string]any{"vendor": fmt.Sprint(vendor), "item": fmt.Sprint(item)}
		it := findItem(w, item)
		if it == nil {
			return b.refuse("sell", args, "item_not_found", "no such item in your bags")
		}
		if count == 0 {
			count = it["count"].(uint32)
		}
		body := binary.LittleEndian.AppendUint64(nil, vendor)
		body = binary.LittleEndian.AppendUint64(body, guidOf(it))
		w.SendPacketRaw(cmsgSellItem, binary.LittleEndian.AppendUint32(body, count))
		money := w.Money()
		return b.request("sell", args, 3*time.Second, func() *script.Outcome {
			if w.Money() > money {
				return script.Ok(map[string]any{"money": w.Money() - money})
			}
			return nil
		}, func(ev script.Event) *script.Outcome { return failOn(ev, "sell") })
	})
	// sell_junk(vendor) -> number of stacks sold: everything grey (quality 0) with a sell price. Items whose
	// info hasn't loaded yet are skipped; call again a second later to catch them.
	e.RegisterFunc("sell_junk", func(vendor uint64) int {
		n := 0
		for _, it := range bagItems(w) {
			if it["bag"] == uint8(255) && it["slot"].(uint8) < 23 {
				continue
			}
			if p := itemInfo(w, it["entry"].(uint32)); p != nil && p.Quality == 0 && p.SellPrice > 0 {
				body := binary.LittleEndian.AppendUint64(nil, vendor)
				body = binary.LittleEndian.AppendUint64(body, guidOf(it))
				w.SendPacketRaw(cmsgSellItem, binary.LittleEndian.AppendUint32(body, it["count"].(uint32)))
				n++
			}
		}
		return n
	})
	// vendor_items(vendor) -> what a vendor sells, or nil the first time (asks; call again in a moment)
	e.RegisterFunc("vendor_items", func(vendor uint64) []vendorItem {
		s.mu.Lock()
		list, ok := s.vendors[vendor]
		s.mu.Unlock()
		if !ok {
			w.SendPacketRaw(cmsgListInventory, binary.LittleEndian.AppendUint64(nil, vendor))
			return nil
		}
		for i := range list {
			if p := itemInfo(w, list[i].Entry); p != nil {
				list[i].Name = p.Name
			}
		}
		return list
	})
	// buy(vendor, entry [, count]): count is in vendor units (buy_count each). Needs vendor_items first.
	e.RegisterFunc("buy", func(vendor uint64, entry, count uint32) map[string]any {
		args := map[string]any{"vendor": fmt.Sprint(vendor), "item": entry, "count": max(count, 1)}
		s.mu.Lock()
		list, ok := s.vendors[vendor]
		s.mu.Unlock()
		if !ok {
			w.SendPacketRaw(cmsgListInventory, binary.LittleEndian.AppendUint64(nil, vendor))
			return b.refuse("buy", args, "vendor_list_not_loaded", "vendor list not loaded yet: call bot.vendor_items(vendor), then buy again")
		}
		for _, v := range list {
			if v.Entry == entry {
				body := binary.LittleEndian.AppendUint64(nil, vendor)
				body = binary.LittleEndian.AppendUint32(body, entry)
				body = binary.LittleEndian.AppendUint32(body, v.Slot)
				body = binary.LittleEndian.AppendUint32(body, max(count, 1))
				w.SendPacketRaw(cmsgBuyItem, append(body, 0))
				return b.request("buy", args, 5*time.Second, nil, func(ev script.Event) *script.Outcome {
					if ev.Type == "item_received" && same(ev.Data["item"], entry) {
						return script.Ok(ev.Data)
					}
					if o := failOn(ev, "buy"); o != nil {
						return o
					}
					return failOn(ev, "inventory")
				})
			}
		}
		return b.refuse("buy", args, "not_sold_here", fmt.Sprintf("vendor doesn't sell item %d", entry))
	})
	// destroy(item [, count]): item = guid or entry; gone for good
	e.RegisterFunc("destroy", func(item uint64, count uint32) map[string]any {
		args := map[string]any{"item": fmt.Sprint(item)}
		it := findItem(w, item)
		if it == nil {
			return b.refuse("destroy", args, "item_not_found", "no such item in your bags")
		}
		if count == 0 {
			count = it["count"].(uint32)
		}
		w.SendPacketRaw(cmsgDestroyItem, []byte{it["bag"].(uint8), it["slot"].(uint8), byte(min(count, 255)), 0, 0, 0})
		return b.done("destroy", args, map[string]any{"count": count})
	})
	// use_item(item [, target guid]): item = guid or entry. Eat, drink, potions, quest items. Food and drink
	// only work out of combat and you must sit still while they tick.
	e.RegisterFunc("use_item", func(item, target uint64) map[string]any {
		args := map[string]any{"item": fmt.Sprint(item)}
		it := findItem(w, item)
		if it == nil {
			return b.refuse("use_item", args, "item_not_found", "no such item in your bags")
		}
		p := itemInfo(w, it["entry"].(uint32))
		if p == nil {
			return b.refuse("use_item", args, "not_loaded", "item info not loaded yet, try again")
		}
		if p.UseSpell == 0 {
			return b.refuse("use_item", args, "no_use_effect", p.Name+" has no use effect")
		}
		var buf bytes.Buffer
		buf.Write([]byte{it["bag"].(uint8), it["slot"].(uint8), 1})
		binary.Write(&buf, binary.LittleEndian, p.UseSpell)
		binary.Write(&buf, binary.LittleEndian, guidOf(it))
		binary.Write(&buf, binary.LittleEndian, uint32(0)) // glyph index
		buf.WriteByte(0)                                   // cast flags
		writeTarget(&buf, target)
		b.agent.castSent(p.UseSpell)
		w.SendPacketRaw(cmsgUseItem, buf.Bytes())
		return b.request("use_item", args, 5*time.Second, nil, castMatch(p.UseSpell))
	})
	// use_object(guid [, spell]): interact with a game object (door, quest object, chest, herb). Chests and
	// quest crates are opened with a spell like the real client does (default Opening, 6478); the loot is
	// taken automatically. Stand within ~5 yards.
	e.RegisterFunc("use_object", func(guid uint64, spell uint32) map[string]any {
		args := map[string]any{"guid": fmt.Sprint(guid)}
		o := w.GetObject(guid)
		if o == nil {
			return b.refuse("use_object", args, "not_found", "no such object nearby")
		}
		if spell == 0 && (o.Value(fieldGOBytes1)>>8)&0xFF == goTypeChest {
			spell = spellOpening
		}
		if spell == 0 { // the server doesn't confirm a plain use: watch for item_received / quest_progress
			if err := w.GameObjectUse(guid); err != nil {
				return b.refuse("use_object", args, "refused", err.Error())
			}
			return b.done("use_object", args, nil)
		}
		var buf bytes.Buffer
		buf.WriteByte(0)
		binary.Write(&buf, binary.LittleEndian, spell)
		buf.WriteByte(0)
		binary.Write(&buf, binary.LittleEndian, uint32(0x800)) // TARGET_FLAG_GAMEOBJECT
		buf.Write(packGUID(guid))
		b.agent.castSent(spell)
		w.SendPacketRaw(cmsgCastSpellOp, buf.Bytes())
		return b.request("use_object", args, 10*time.Second, nil, castMatch(spell))
	})
	// repair(npc): repair everything you wear and carry at an NPC with npc_flags 0x1000 (armorers, weaponsmiths)
	// The server says nothing when a repair fails, so the result is checked a moment later and reported in read_chat.
	e.RegisterFunc("repair", func(npc uint64) map[string]any {
		args := map[string]any{"npc": fmt.Sprint(npc)}
		damaged := func() int {
			n := 0
			for _, it := range bagItems(w) {
				if mx, ok := it["max_durability"].(uint32); ok && it["durability"].(uint32) < mx {
					n++
				}
			}
			return n
		}
		before, money := damaged(), w.Money()
		if before == 0 {
			return b.done("repair", args, map[string]any{"fixed": 0})
		}
		body := binary.LittleEndian.AppendUint64(nil, npc)
		body = binary.LittleEndian.AppendUint64(body, 0) // 0 = all items
		w.SendPacketRaw(0x2A8, append(body, 0))          // CMSG_REPAIR_ITEM; no guild bank
		// the server says nothing about a repair: compare durability a moment later
		start := time.Now()
		return b.request("repair", args, 5*time.Second, func() *script.Outcome {
			if time.Since(start) < 1500*time.Millisecond {
				return nil
			}
			after, spent := damaged(), int64(money)-int64(w.Money())
			if after < before {
				return script.Ok(map[string]any{"fixed": before - after, "cost": spent, "still_damaged": after})
			}
			return script.Fail("not_repaired", fmt.Sprintf("%d item(s) still damaged: stand within ~5 yd of an NPC that repairs (npc_flags 0x1000) with enough money (you have %dc)", after, w.Money()), nil)
		}, nil)
	})
	// broken_gear() -> equipped items at 0 durability (they give no stats; a broken weapon means no weapon skills)
	e.RegisterFunc("broken_gear", func() []map[string]any {
		out := []map[string]any{}
		for _, it := range bagItems(w) {
			if it["bag"] == uint8(255) && it["slot"].(uint8) < 19 && it["max_durability"] != nil && it["durability"] == uint32(0) {
				out = append(out, it)
			}
		}
		return out
	})
	// train(trainer, spell): learn a spell from a trainer (costs money; ids from `wowdb npc <trainer>`)
	e.RegisterFunc("train", func(trainer uint64, spell uint32) map[string]any {
		body := binary.LittleEndian.AppendUint64(nil, trainer)
		w.SendPacketRaw(0x1B2, binary.LittleEndian.AppendUint32(body, spell)) // CMSG_TRAINER_BUY_SPELL
		return b.request("train", map[string]any{"trainer": fmt.Sprint(trainer), "spell": spell}, 5*time.Second, nil,
			func(ev script.Event) *script.Outcome {
				if !same(ev.Data["spell"], spell) {
					return nil
				}
				if ev.Type == "learned" {
					return script.Ok(nil)
				}
				return failOn(ev, "train")
			})
	})
	// path(x, y, z) -> walking distance, gap: gap is how far the best path ends from the target. A gap over a
	// few yards means you can't get there (mob on a ledge, in a closed building, under the map); nil if no path.
	e.RegisterFunc("path", func(x, y, z float32) (any, any) {
		if b.nav == nil {
			return nil, nil
		}
		px, py, pz, _, m := w.Position()
		res, err := b.nav.FindPath(m, navigation.Point3D{X: px, Y: py, Z: pz}, navigation.Point3D{X: x, Y: y, Z: z})
		if err != nil || res == nil || len(res.Points) == 0 {
			return nil, nil
		}
		length, prev := 0.0, navigation.Point3D{X: px, Y: py, Z: pz}
		for _, p := range res.Points {
			length += dist3(prev, p)
			prev = p
		}
		return math.Round(length*10) / 10, math.Round(dist3(prev, navigation.Point3D{X: x, Y: y, Z: z})*10) / 10
	})
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
