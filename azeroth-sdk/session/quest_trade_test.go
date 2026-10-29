package session

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
)

// SMSG_QUESTGIVER_REQUEST_ITEMS as PlayerMenu::SendQuestGiverRequestItems writes it.
func TestQuestRequestItems(t *testing.T) {
	le := binary.LittleEndian
	pkt := func(completable, closeOnCancel uint32, items ...[2]uint32) []byte {
		p := le.AppendUint64(nil, 0xF130000000000042)
		p = le.AppendUint32(p, 33)
		p = append(p, "Wolves Across the Border\x00Bring me the meat.\x00"...)
		for _, v := range []uint32{0, 6, closeOnCancel, 8, 0, 0} { // unknown, emote, close on cancel, flags, suggested, money
			p = le.AppendUint32(p, v)
		}
		p = le.AppendUint32(p, uint32(len(items)))
		for _, it := range items {
			p = le.AppendUint32(le.AppendUint32(le.AppendUint32(p, it[0]), it[1]), 1234)
		}
		for _, v := range []uint32{completable, 4, 8, 0x10} {
			p = le.AppendUint32(p, v)
		}
		return p
	}
	q, done, turnIn, need := parseQuestRequestItems(pkt(0, 0, [2]uint32{750, 8}))
	if q != 33 || done || !turnIn || len(need) != 1 || need[0] != [2]uint32{750, 8} {
		t.Fatalf("incomplete: %d %v %v %v", q, done, turnIn, need)
	}
	if q, done, turnIn, need = parseQuestRequestItems(pkt(3, 0)); q != 33 || !done || !turnIn || len(need) != 0 {
		t.Fatalf("completable: %d %v %v", q, done, need)
	}
	if _, _, turnIn, _ = parseQuestRequestItems(pkt(0, 1)); turnIn { // a hello's (PlayerQuest.cpp SendPreparedQuest)
		t.Fatal("hello taken for a turn-in")
	}
}

// SMSG_TRADE_STATUS as WorldSession::SendTradeStatus writes it.
func TestTradeRefusals(t *testing.T) {
	le := binary.LittleEndian
	status := func(st uint32, rest ...byte) client.TradeStatusInfo {
		info, err := client.ParseTradeStatus(append(le.AppendUint32(nil, st), rest...))
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	ts := &tradeState{state: "waiting"}
	ts.refused(status(client.TradeStatusWrongFaction))
	if ts.state != "wrong_faction" || ts.err[0] != "wrong_faction" {
		t.Fatalf("wrong faction: %+v", ts)
	}
	// accept refused for money: CLOSE_WINDOW, result 29 (EQUIP_ERR_NOT_ENOUGH_MONEY), not target, no item
	ts = &tradeState{state: "open"}
	ts.refused(status(client.TradeStatusCloseWindow, append(le.AppendUint32(nil, 29), 0, 0, 0, 0, 0)...))
	if ts.state != "open" || ts.err[0] != "not_enough_money" {
		t.Fatalf("not enough money: %+v", ts)
	}
	ts.refused(status(client.TradeStatusTargetTooFar))
	if ts.state != "open" || ts.err[0] != "too_far" {
		t.Fatalf("accept too far: %+v", ts)
	}
	ts.stepAt = time.Now() // trade.offer just sent an untradeable item: HandleSetTradeItemOpcode answers CANCELED, the trade stays
	ts.refused(status(client.TradeStatusTradeCanceled))
	if ts.state != "open" || ts.err[0] != "untradeable" {
		t.Fatalf("refused item: %+v", ts)
	}
	ts.stepAt = time.Time{}
	ts.refused(status(client.TradeStatusTradeCanceled))
	if ts.state != "cancelled" || ts.err[1] != "the trade was cancelled" {
		t.Fatalf("cancelled: %+v", ts)
	}
}
