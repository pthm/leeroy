package session

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func TestVendorPartialOutcomes(t *testing.T) {
	failed := []script.Outcome{*script.Fail("cannot_sell", "refused", nil)}
	if r := saleOutcome(2, 0, 10, 10, failed); r != nil {
		t.Fatalf("resolved before both stacks answered: %+v", r)
	}
	if r := saleOutcome(2, 1, 10, 10, failed); r != nil {
		t.Fatalf("resolved before money update: %+v", r)
	}
	r := saleOutcome(2, 1, 10, 17, failed)
	if r.OK || r.Code != "cannot_sell" || r.Data.(types.Sold) != (types.Sold{Money: 7, Count: 1, Failed: 1}) {
		t.Fatalf("partial sale: %+v", r)
	}
	if r := saleOutcome(1, 0, 10, 10, failed); r.OK || r.Data.(types.Sold).Failed != 1 {
		t.Fatalf("total refusal: %+v", r)
	}
	if r := saleOutcome(2, 2, 10, 17, nil); !r.OK || r.Data.(types.Sold).Count != 2 {
		t.Fatalf("complete sale: %+v", r)
	}
	for _, remaining := range []int{0, 1, 2} {
		r := repairOutcome(2, remaining, 100, 90)
		if r.OK != (remaining == 0) || r.Data.(types.Repaired).StillDamaged != remaining {
			t.Fatalf("repair with %d remaining: %+v", remaining, r)
		}
	}
}

func TestVendorListLoadingAndEmpty(t *testing.T) {
	s := &itemState{vendors: map[uint64][]vendorItem{}}
	if r, _ := s.vendorList(nil, 42); r != nil {
		t.Fatal("an unseen vendor list is still loading")
	}
	packet := append(binary.LittleEndian.AppendUint64(nil, 42), 0)
	s.onPacket(smsgListInventory, packet)
	r, limited := s.vendorList(nil, 42)
	if r == nil || !r.OK || limited || len(r.Data.([]types.VendorItem)) != 0 {
		t.Fatalf("confirmed empty list: %+v", r)
	}
}

// SMSG_ITEM_QUERY_SINGLE_RESPONSE as WorldSession::HandleItemQuerySingleOpcode writes it.
func TestParseItemQuery(t *testing.T) {
	le := binary.LittleEndian
	u := func(b []byte, v ...uint32) []byte {
		for _, x := range v {
			b = le.AppendUint32(b, x)
		}
		return b
	}
	f := func(b []byte, v float32) []byte { return le.AppendUint32(b, math.Float32bits(v)) }
	p := u(nil, 90001, 2, 7, 0) // entry, class, subclass, sound override
	p = append(p, "Test Sword\x00\x00\x00\x00"...)
	p = u(p, 1234, 2, 0, 0, 500, 100, 13, 0xFFFFFFFF, 0xFFFFFFFF, 20, 15) // display .. required level
	p = u(p, 0, 0, 0, 0, 0, 0, 0, 0)                                      // skill .. max count
	p = u(p, 1, 0, 1, 4, 5)                                               // stack, container slots, 1 stat: strength 5
	p = u(p, 0, 0)                                                        // scaling
	p = f(f(p, 10), 20)
	p = u(p, 0)
	p = f(f(p, 0), 0)
	p = u(p, 0)
	p = u(p, 30, 0, 0, 0, 0, 0, 0, 2000, 0, 0) // armor, 6 resistances, delay, ammo, range
	for i := 0; i < 5; i++ {
		p = u(p, 0, 0, 0, 0xFFFFFFFF, 0, 0xFFFFFFFF)
	}
	p = u(p, 1)
	p = append(p, "desc\x00"...)
	p = u(p, 0, 0, 0, 777, 42) // page text, language, page material, start quest, lock id
	parseItemQuery(p)
	items.Lock()
	it := items.proto[90001]
	items.Unlock()
	if it == nil || it.Name != "Test Sword" || it.ReqLevel != 15 || it.Stats["strength"] != 5 || it.Armor != 30 ||
		it.DPS != 7.5 || it.StartQuest != 777 || it.Slot != "one_hand" {
		t.Fatalf("%+v", it)
	}
}

// A group roll as Group.cpp sends it: SendLootStartRoll, CountRollVote's need (empty guid, 0, 0), SendLootRollWon.
func TestLootRolls(t *testing.T) {
	le := binary.LittleEndian
	items.Lock()
	items.proto[90002] = &itemProto{Entry: 90002, Name: "Green Thing", Quality: 2}
	items.Unlock()
	const roll, me = 0x4000000000000123, 7
	start := le.AppendUint64(nil, roll)
	for _, v := range []uint32{0, 3, 90002, 0, 0, 1, 60000} { // map, slot, item, suffix, property, count, countdown
		start = le.AppendUint32(start, v)
	}
	r, err := client.ParseLootStartRoll(append(start, 0x07)) // pass, need, greed
	if err != nil {
		t.Fatal(err)
	}
	s := &lootRolls{m: map[uint64]*pendingRoll{}}
	v := s.start(r).view(nil)
	if v.Roll != roll || v.Item.Name != "Green Thing" || v.Item.Count != 1 || strings.Join(v.Choices, ",") != "pass,need,greed" || v.SecondsLeft < 59 {
		t.Fatalf("start: %+v", v)
	}
	vote := func(target uint64, num, typ byte) []byte {
		p := le.AppendUint32(le.AppendUint64(nil, 0), 3)
		p = le.AppendUint32(le.AppendUint64(p, target), 90002)
		return append(le.AppendUint32(le.AppendUint32(p, 0), 0), num, typ, 0)
	}
	ev, _ := client.ParseLootRoll(vote(8, 128, 2))
	if g, c, n := s.vote(ev, me); g != roll || c != "greed" || n != 0 || len(s.open()) != 1 {
		t.Fatalf("their greed: %x %s %d", g, c, n)
	}
	const twin = roll + 1 // the same item in the same slot of a second corpse
	s.start(client.LootStartRoll{ItemGUID: twin, ItemSlot: 3, ItemID: 90002, CountdownMS: 60000, RollVoteMask: 7})
	s.sent(roll)
	ev, _ = client.ParseLootRoll(vote(me, 0, 0))
	if g, c, _ := s.vote(ev, me); g != roll || c != "need" || len(s.open()) != 1 || s.open()[0].ItemGUID != twin {
		t.Fatalf("my need: %x %s, open %v", g, c, s.open())
	}
	s.end(twin)
	ev, _ = client.ParseLootRoll(vote(me, 87, 1))
	if _, c, n := s.vote(ev, me); c != "need" || n != 87 {
		t.Fatalf("counted: %s %d", c, n)
	}
	s.end(roll)
	if _, ok := s.get(roll); ok {
		t.Fatal("roll still open after it was won")
	}
}

// SMSG_INVENTORY_CHANGE_FAILURE (Player::SendEquipError), SMSG_BUY_FAILED and SMSG_SELL_ITEM as the server writes them.
func TestItemErrors(t *testing.T) {
	le := binary.LittleEndian
	b := &Bot{lua: script.NewEngine(func(string, ...any) {}, nil)}
	c := &controlState{watched: map[uint16]bool{}}
	equipErr := func(code byte, extra ...byte) []byte {
		return append(append([]byte{code}, make([]byte, 17)...), extra...)
	}
	c.onPacket(b, smsgInvChangeFail, equipErr(59))                             // EQUIP_ERR_NONE: no error
	c.onPacket(b, smsgInvChangeFail, equipErr(1, le.AppendUint32(nil, 20)...)) // CANT_EQUIP_LEVEL_I + level
	c.onPacket(b, smsgInvChangeFail, equipErr(65))
	buy := le.AppendUint32(le.AppendUint64(nil, 0xF1300000000000AA), 159)
	c.onPacket(b, 0x1A5, append(buy, 12)) // BUY_ERR_REPUTATION_REQUIRE
	sell := le.AppendUint64(le.AppendUint64(nil, 0xF1300000000000AA), 0x4000000000000001)
	c.onPacket(b, 0x1A1, append(sell, 0))             // sold: no error
	c.onPacket(b, 0x1A1, append(sell, 7))             // SELL_ERR_CANT_SELL_TO_THIS_MERCHANT
	c.onPacket(b, 0x1A1, append(make([]byte, 16), 3)) // SendListInventory out of range: CANT_FIND_VENDOR, no vendor, no item
	var got []string
	for _, ev := range b.lua.Bus.Wait(script.Query{Types: []string{"error"}}, time.Second) {
		e := ev.Data.(types.ErrorEvent)
		got = append(got, e.Op+" "+e.Code+" "+e.Message)
	}
	want := []string{"items cant_equip_level_i You must reach a higher level to use that: requires level 20",
		"items too_many_special_bags too many special bags", "vendor.buy reputation_required buy failed: your reputation with their faction is too low",
		"vendor.sell vendor_doesnt_buy sell failed: this vendor doesn't buy items",
		"vendor.items vendor_not_found the vendor won't show its wares: vendor not found (stand within ~5 yd)"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
}

// SMSG_LIST_INVENTORY (WorldSession::SendListInventory) then SMSG_BUY_ITEM's stock left (Player::BuyItemFromVendorSlot).
func TestVendorStock(t *testing.T) {
	le := binary.LittleEndian
	s := &itemState{vendors: map[uint64][]vendorItem{}}
	const npc = 0xF1300000000000AA
	l := append(le.AppendUint64(nil, npc), 1)
	for _, v := range []uint32{2, 159, 18084, 3, 25, 0, 5, 0} { // slot (from 1), item, display, stock, price, durability, buy count, extended cost
		l = le.AppendUint32(l, v)
	}
	s.onPacket(smsgListInventory, l)
	buy := le.AppendUint32(le.AppendUint32(le.AppendUint32(le.AppendUint64(nil, npc), 2), 2), 1)
	s.onPacket(smsgBuyItem, buy)
	if v := s.vendors[npc]; len(v) != 1 || v[0].Entry != 159 || v[0].Price != 25 || v[0].Count != 5 || v[0].Stock != 2 {
		t.Fatalf("%+v", v)
	}
}

// SMSG_LOOT_RESPONSE: Player::SendLoot with a LootView (slot, LootItem, slot type), and SendLootError's short form.
func TestLootResponse(t *testing.T) {
	le := binary.LittleEndian
	p := append(le.AppendUint64(nil, 0xF130000000000099), 1) // LOOT_CORPSE
	p = append(le.AppendUint32(p, 35), 3)                    // gold, 3 slots
	for i, typ := range []byte{4, 1, 3} {                    // owner, roll ongoing, locked
		p = append(p, byte(i))
		p = le.AppendUint32(le.AppendUint32(p, 2589+uint32(i)), 1) // item, count
		p = append(le.AppendUint32(le.AppendUint32(le.AppendUint32(p, 0), 0), 0), typ)
	}
	lr, err := client.ParseLootResponse(p)
	if err != nil || lr.GUID != 0xF130000000000099 || lr.Error != -1 || len(lr.Items) != 3 || lr.Items[0].SlotType != 4 || lr.Items[1].SlotType != 1 ||
		lr.Items[2].Index != 2 || lr.Items[2].ItemID != 2591 || lr.Items[2].SlotType != 3 {
		t.Fatalf("%+v %v", lr, err)
	}
	b := &Bot{lua: script.NewEngine(func(string, ...any) {}, nil)}
	b.handleLootResponse(append(le.AppendUint64(nil, 0xF130000000000099), 0, 6)) // LOOT_NONE, LOOT_ERROR_LOCKED
	evs := b.lua.Bus.Wait(script.Query{Types: []string{"error"}}, time.Second)
	if len(evs) != 1 || evs[0].Data.(types.ErrorEvent).Code != "being_looted" {
		t.Fatalf("%v", evs)
	}
}
