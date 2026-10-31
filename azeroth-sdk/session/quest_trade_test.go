package session

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/types"
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

// The server never echoes your own money or item offer (TradeData::Update(true) goes to the trader only), so
// trade.offer resolves on silence; a refusal still fails it. An offer change un-accepts both sides, and the server
// tells both: our pending accept fails as unaccepted, unless the change raced it. No accept without a trade.
func TestTradeOfferAndAccept(t *testing.T) {
	le := binary.LittleEndian
	status := func(st uint32, rest ...byte) client.TradeStatusInfo {
		info, err := client.ParseTradeStatus(append(le.AppendUint32(nil, st), rest...))
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	b := testBot()
	ts := &tradeState{state: "open", stepAt: time.Now()}
	ts.mine = types.TradeSide{Money: 100}
	if o := b.offerDone(ts, ts.stepAt); o != nil {
		t.Fatalf("resolved before the server could refuse: %+v", o)
	}
	if o := b.offerDone(ts, ts.stepAt.Add(-600*time.Millisecond)); o == nil || !o.OK {
		t.Fatalf("no echo comes: the offer stands after half a second, got %+v", o)
	}
	b.tradeStatus(ts, status(client.TradeStatusCloseWindow, append(le.AppendUint32(nil, 29), 0, 0, 0, 0, 0)...)) // NOT_ENOUGH_MONEY
	if o := b.offerDone(ts, ts.stepAt); o == nil || o.OK || o.Code != "not_enough_money" {
		t.Fatalf("refused offer: %+v", o)
	}

	ts = &tradeState{state: "open", accepting: true, stepAt: time.Now()}
	b.tradeStatus(ts, status(client.TradeStatusBackToTrade)) // the change our accept raced
	if ts.err[0] != "" || !ts.accepting {
		t.Fatalf("raced change undid a fresh accept: %+v", ts)
	}
	ts.stepAt = time.Now().Add(-time.Second)
	b.tradeStatus(ts, status(client.TradeStatusBackToTrade))
	if ts.err[0] != "unaccepted" || ts.accepting {
		t.Fatalf("an offer changed after our accept: %+v", ts)
	}

	b.setupTrade()
	v, err := b.lua.Eval(`return trade.accept().id`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := strconv.Atoi(fmt.Sprint(v))
	if r := b.lua.Reqs.Result(id); r["ok"] != false || r["error"].(map[string]any)["code"] != "not_open" {
		t.Fatalf("accept with no trade: %v", r)
	}
}
