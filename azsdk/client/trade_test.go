package client

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestParseTradeStatus_BeginTrade(t *testing.T) {
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, TradeStatusBeginTrade)
	_ = binary.Write(buf, binary.LittleEndian, uint64(0xAABBCCDDEEFF0011))
	info, err := ParseTradeStatus(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != TradeStatusBeginTrade {
		t.Fatalf("status=%d", info.Status)
	}
	if info.TraderGUID != 0xAABBCCDDEEFF0011 {
		t.Fatalf("trader=%x", info.TraderGUID)
	}
}

func TestParseTradeStatus_Complete(t *testing.T) {
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, TradeStatusTradeComplete)
	info, err := ParseTradeStatus(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != TradeStatusTradeComplete {
		t.Fatalf("status=%d", info.Status)
	}
	if TradeStatusName(info.Status) != "TRADE_COMPLETE" {
		t.Fatalf("name=%s", TradeStatusName(info.Status))
	}
}

func TestParseLootStartRoll(t *testing.T) {
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, uint64(99))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))    // map
	_ = binary.Write(buf, binary.LittleEndian, uint32(2))    // slot
	_ = binary.Write(buf, binary.LittleEndian, uint32(2589)) // linen
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))
	_ = binary.Write(buf, binary.LittleEndian, uint32(1))
	_ = binary.Write(buf, binary.LittleEndian, uint32(60000))
	_ = binary.Write(buf, binary.LittleEndian, uint8(7))
	r, err := ParseLootStartRoll(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if r.ItemID != 2589 || r.ItemSlot != 2 || r.CountdownMS != 60000 {
		t.Fatalf("%+v", r)
	}
}

func TestParseLootRollWon(t *testing.T) {
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, uint64(1))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))
	_ = binary.Write(buf, binary.LittleEndian, uint32(19019))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))
	_ = binary.Write(buf, binary.LittleEndian, uint64(42))
	_ = binary.Write(buf, binary.LittleEndian, uint8(77))
	_ = binary.Write(buf, binary.LittleEndian, RollNeed)
	r, err := ParseLootRollWon(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if r.WinnerGUID != 42 || r.ItemID != 19019 || r.RollType != RollNeed {
		t.Fatalf("%+v", r)
	}
}

// Player::SendLoot / SendLootError as AzerothCore writes them.
func TestParseLootResponse(t *testing.T) {
	le := binary.LittleEndian
	p := append(le.AppendUint64(nil, 0xF130000000000042), 1) // corpse
	p = append(le.AppendUint32(p, 57), 2)
	slot := func(i uint8, item uint32, typ uint8) {
		p = append(le.AppendUint32(le.AppendUint32(le.AppendUint32(le.AppendUint32(le.AppendUint32(append(p, i), item), 1), 7), 0), 0), typ)
	}
	slot(0, 2589, LootSlotTypeAllowLoot)
	slot(1, 4306, LootSlotTypeRollOngoing)
	lr, err := ParseLootResponse(p)
	if err != nil || lr.Error != -1 || lr.Gold != 57 || len(lr.Items) != 2 ||
		lr.Items[1] != (LootItem{Index: 1, ItemID: 4306, Quantity: 1, SlotType: LootSlotTypeRollOngoing}) {
		t.Fatalf("%+v %v", lr, err)
	}

	lr, err = ParseLootResponse(append(le.AppendUint64(nil, 0xF130000000000042), 0, 4))
	if err != nil || lr.Error != 4 || LootErrorName(lr.Error) != "TOO_FAR" || lr.Items != nil {
		t.Fatalf("error form: %+v %v", lr, err)
	}
}

// An accept the server refuses (CLOSE_WINDOW + inventory result, TARGET_TO_FAR) leaves the trade open.
func TestTradeStatusRefusedAcceptKeepsTradeOpen(t *testing.T) {
	le := binary.LittleEndian
	w := NewWorldClient("t", nil, nil)
	w.handleTradeStatus(le.AppendUint32(nil, TradeStatusOpenWindow))
	w.handleTradeStatus(append(le.AppendUint32(le.AppendUint32(nil, TradeStatusCloseWindow), 29), 0, 0, 0, 0, 0))
	if !w.TradeOpen() || w.LastTradeStatus().Result != 29 {
		t.Fatalf("open=%v %+v", w.TradeOpen(), w.LastTradeStatus())
	}
	w.handleTradeStatus(le.AppendUint32(nil, TradeStatusTargetTooFar))
	if !w.TradeOpen() {
		t.Fatal("too far on accept closed the trade")
	}
	w.handleTradeStatus(le.AppendUint32(nil, TradeStatusIgnoreYou))
	if w.TradeOpen() {
		t.Fatal("ignore should close the trade")
	}
}
