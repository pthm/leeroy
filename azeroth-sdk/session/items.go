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
	"github.com/pthm/wowbench/azeroth-sdk/types"
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
	e.Func("items.use", "(item [, guid]) → request<CastDone>", "use an item you carry (entry or guid): food, drink, potions, quest items; optionally on a unit. Food and drink work out of combat, and you must sit still while they tick",
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
			return b.request("items.use", args, 5*time.Second, nil, castMatch(it.UseSpell))
		})
	e.Func("items.equip", "(item) → request<Equipped>", "wear an item you carry (entry or guid) in its slot", func(item uint64) map[string]any {
		args := map[string]any{"item": gs(item)}
		it := findItem(w, item)
		if it == nil {
			return b.refuse("items.equip", args, "item_not_found", "no such item in your bags")
		}
		if it.Name != "" && !it.Usable {
			return b.refuse("items.equip", args, "not_usable", it.WhyNot)
		}
		g := it.GUID
		w.AutoEquipItem(it.Bag, it.BagSlot)
		return b.request("items.equip", args, 3*time.Second, func() *script.Outcome {
			for _, x := range bagItems(w) {
				if x.GUID == g && x.Equipped {
					return script.Ok(types.Equipped{Slot: x.Slot})
				}
			}
			return nil
		}, func(ev script.Event) *script.Outcome { return failOn(ev, "items") })
	})
	e.Func("items.destroy", "(item [, count = all]) → request", "destroy an item you carry (entry or guid): gone for good", func(item uint64, count uint32) map[string]any {
		args := map[string]any{"item": gs(item)}
		it := findItem(w, item)
		if it == nil {
			return b.refuse("items.destroy", args, "item_not_found", "no such item in your bags")
		}
		if count == 0 {
			count = it.Count
		}
		w.SendPacketRaw(cmsgDestroyItem, []byte{it.Bag, it.BagSlot, byte(min(count, 255)), 0, 0, 0})
		return b.done("items.destroy", args, nil)
	})
	e.Func("items.loot", "(guid) → request<Looted>", "loot a corpse within 5 yd: takes every item and the money (waits a moment for a fresh corpse to become lootable)",
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
					if d.Op != "items" {
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

	// ---------- objects ----------
	e.Func("objects.use", "(guid [, spell]) → request<CastDone>", "use a game object within ~5 yd: doors, quest objects, chests and crates (opened with the Opening spell like the real client; the loot is taken). Plain objects resolve at once: watch for item_received / quest_progress",
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
		if !ok {
			w.SendPacketRaw(cmsgListInventory, binary.LittleEndian.AppendUint64(nil, vendor))
			return nil
		}
		out := []types.VendorItem{}
		for _, v := range list {
			out = append(out, types.VendorItem{Item: itemTemplate(w, v.Entry), VendorSlot: v.Slot, Price: v.Price, BuyCount: v.Count, Stock: v.Stock})
		}
		return out
	})
	e.Func("vendor.buy", "(npc, entry [, count = 1]) → request<ItemReceived>", "buy from a vendor within ~5 yd (count in purchases of buy_count each). Call vendor.items first",
		func(vendor uint64, entry, count uint32) map[string]any {
			args := map[string]any{"npc": gs(vendor), "item": entry, "count": max(count, 1)}
			s.mu.Lock()
			list, ok := s.vendors[vendor]
			s.mu.Unlock()
			if !ok {
				w.SendPacketRaw(cmsgListInventory, binary.LittleEndian.AppendUint64(nil, vendor))
				return b.refuse("vendor.buy", args, "vendor_list_not_loaded", "call vendor.items(npc) first, then buy again")
			}
			for _, v := range list {
				if v.Entry == entry {
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
	moneyUp := func(before uint32, count int) func() *script.Outcome {
		return func() *script.Outcome {
			if w.Money() > before {
				return script.Ok(types.Sold{Money: w.Money() - before, Count: count})
			}
			return nil
		}
	}
	e.Func("vendor.sell", "(npc, item [, count = all]) → request<Sold>", "sell an item you carry (entry or guid) to a vendor within ~5 yd", func(vendor, item uint64, count uint32) map[string]any {
		args := map[string]any{"npc": gs(vendor), "item": gs(item)}
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
		return b.request("vendor.sell", args, 3*time.Second, moneyUp(before, 0), func(ev script.Event) *script.Outcome { return failOn(ev, "vendor.sell") })
	})
	e.Func("vendor.sell_junk", "(npc) → request<Sold>", "sell every grey (poor) item you carry. Items whose info hasn't loaded are skipped", func(vendor uint64) map[string]any {
		args := map[string]any{"npc": gs(vendor)}
		n, before := 0, w.Money()
		for _, it := range bagItems(w) {
			if inBags(it) && it.Quality == "poor" && it.SellPrice > 0 {
				sell(vendor, it, it.Count)
				n++
			}
		}
		if n == 0 {
			return b.done("vendor.sell_junk", args, types.Sold{})
		}
		return b.request("vendor.sell_junk", args, 3*time.Second, moneyUp(before, n), func(ev script.Event) *script.Outcome { return failOn(ev, "vendor.sell") })
	})
	e.Func("vendor.repair", "(npc) → request<Repaired>", "repair everything you wear and carry at an NPC that repairs (role repair: armorers, weaponsmiths) within ~5 yd",
		func(npc uint64) map[string]any {
			args := map[string]any{"npc": gs(npc)}
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
	e.Func("trainer.learn", "(npc, spell) → request<Learned>", "learn a spell from a trainer within ~5 yd (costs money; wowdb npc <trainer> lists spells, levels and costs)",
		func(trainer uint64, spell uint32) map[string]any {
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
