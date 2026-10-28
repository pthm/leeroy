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
	Entry      uint32  `json:"entry"`
	Name       string  `json:"name"`
	Quality    uint32  `json:"quality"` // 0 poor (grey, junk) 1 common 2 uncommon 3 rare 4 epic
	Class      uint32  `json:"class"`   // 0 consumable 2 weapon 4 armor 12 quest 15 misc
	SubClass   uint32  `json:"subclass"`
	InvType    uint32  `json:"inv_type"` // equip slot type; 0 = not equippable
	BuyPrice   uint32  `json:"buy_price"`
	SellPrice  uint32  `json:"sell_price"` // copper; 0 = vendors won't buy it
	ItemLevel  uint32  `json:"item_level"`
	ReqLevel   uint32  `json:"required_level"`
	Stack      int32   `json:"max_stack"`
	Armor      uint32  `json:"armor"`
	DPS        float64 `json:"dps"`
	UseSpell   uint32  `json:"use_spell"` // spell cast by use_item (food, drink, potions...); 0 = none
	StartQuest uint32  `json:"start_quest"`
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
	u32()
	u32() // allowable class/race
	p.ItemLevel, p.ReqLevel = u32(), u32()
	r.Seek(4*8, io.SeekCurrent) // skill, skill rank, spell, honor, city, rep faction, rep rank, max count
	p.Stack = int32(u32())
	u32()                                  // container slots
	r.Seek(int64(u32())*8, io.SeekCurrent) // stats
	r.Seek(8, io.SeekCurrent)              // scaling stats
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
	e.RegisterFunc("loot_all", func(guid uint64) any {
		b.LootAll(guid)
		if freeSlots(w) == 0 {
			return "bags full: only money is looted; sell or destroy something"
		}
		return nil
	})
	e.RegisterFunc("item_info", func(entry uint32) *itemProto { return itemInfo(w, entry) })
	// free_slots() -> empty slots in the backpack and equipped bags. At 0, loot and quest rewards are refused.
	e.RegisterFunc("free_slots", func() int { return freeSlots(w) })
	// sell(vendor, item [, count]): item = guid or entry; count defaults to the whole stack
	e.RegisterFunc("sell", func(vendor, item uint64, count uint32) error {
		it := findItem(w, item)
		if it == nil {
			return fmt.Errorf("no such item in your bags")
		}
		if count == 0 {
			count = it["count"].(uint32)
		}
		body := binary.LittleEndian.AppendUint64(nil, vendor)
		body = binary.LittleEndian.AppendUint64(body, guidOf(it))
		w.SendPacketRaw(cmsgSellItem, binary.LittleEndian.AppendUint32(body, count))
		return nil
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
	e.RegisterFunc("buy", func(vendor uint64, entry, count uint32) error {
		s.mu.Lock()
		list, ok := s.vendors[vendor]
		s.mu.Unlock()
		if !ok {
			w.SendPacketRaw(cmsgListInventory, binary.LittleEndian.AppendUint64(nil, vendor))
			return fmt.Errorf("vendor list not loaded yet: call bot.vendor_items(vendor), then buy again")
		}
		for _, v := range list {
			if v.Entry == entry {
				body := binary.LittleEndian.AppendUint64(nil, vendor)
				body = binary.LittleEndian.AppendUint32(body, entry)
				body = binary.LittleEndian.AppendUint32(body, v.Slot)
				body = binary.LittleEndian.AppendUint32(body, max(count, 1))
				w.SendPacketRaw(cmsgBuyItem, append(body, 0))
				return nil
			}
		}
		return fmt.Errorf("vendor doesn't sell item %d", entry)
	})
	// destroy(item [, count]): item = guid or entry; gone for good
	e.RegisterFunc("destroy", func(item uint64, count uint32) error {
		it := findItem(w, item)
		if it == nil {
			return fmt.Errorf("no such item in your bags")
		}
		if count == 0 {
			count = it["count"].(uint32)
		}
		w.SendPacketRaw(cmsgDestroyItem, []byte{it["bag"].(uint8), it["slot"].(uint8), byte(min(count, 255)), 0, 0, 0})
		return nil
	})
	// use_item(item [, target guid]): item = guid or entry. Eat, drink, potions, quest items. Food and drink
	// only work out of combat and you must sit still while they tick.
	e.RegisterFunc("use_item", func(item, target uint64) error {
		it := findItem(w, item)
		if it == nil {
			return fmt.Errorf("no such item in your bags")
		}
		p := itemInfo(w, it["entry"].(uint32))
		if p == nil {
			return fmt.Errorf("item info not loaded yet, try again")
		}
		if p.UseSpell == 0 {
			return fmt.Errorf("%s has no use effect", p.Name)
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
		return nil
	})
	// use_object(guid [, spell]): interact with a game object (door, quest object, chest, herb). Chests and
	// quest crates are opened with a spell like the real client does (default Opening, 6478); the loot is
	// taken automatically. Stand within ~5 yards.
	e.RegisterFunc("use_object", func(guid uint64, spell uint32) error {
		o := w.GetObject(guid)
		if o == nil {
			return fmt.Errorf("no such object nearby")
		}
		if spell == 0 && (o.Value(fieldGOBytes1)>>8)&0xFF == goTypeChest {
			spell = spellOpening
		}
		if spell == 0 {
			return w.GameObjectUse(guid)
		}
		var buf bytes.Buffer
		buf.WriteByte(0)
		binary.Write(&buf, binary.LittleEndian, spell)
		buf.WriteByte(0)
		binary.Write(&buf, binary.LittleEndian, uint32(0x800)) // TARGET_FLAG_GAMEOBJECT
		buf.Write(packGUID(guid))
		b.agent.castSent(spell)
		w.SendPacketRaw(cmsgCastSpellOp, buf.Bytes())
		return nil
	})
	// train(trainer, spell): learn a spell from a trainer (costs money; ids from `wowdb npc <trainer>`)
	e.RegisterFunc("train", func(trainer uint64, spell uint32) {
		body := binary.LittleEndian.AppendUint64(nil, trainer)
		w.SendPacketRaw(0x1B2, binary.LittleEndian.AppendUint32(body, spell)) // CMSG_TRAINER_BUY_SPELL
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
