package session

// Player-to-player trade: the trade window as Lua calls. Either side can put items (up to 6) and money in; the trade
// happens when both accept, and any change un-accepts both. Within ~10 yards of each other.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type tradeState struct {
	mu           sync.Mutex
	state        string // none, requested (someone asked you), open, complete, cancelled, ...
	with         uint64
	mine, theirs types.TradeSide
	theyAccepted bool
	err          [2]string // code, message: why the trade ended or the last step was refused
	updated      int64
	stepAt       time.Time // when trade.offer or trade.accept last sent
}

// snapshot is the trade as types.Trade (t.mu held).
func (b *Bot) tradeSnapshot(t *tradeState) types.Trade {
	tr := types.Trade{State: t.state, Mine: t.mine, Theirs: t.theirs, TheyAccepted: t.theyAccepted, Error: t.err[1]}
	if t.with != 0 {
		tr.With = b.unitName(t.with)
	}
	return tr
}

func (b *Bot) setupTrade() {
	t := &tradeState{state: "none"}
	w := b.world
	w.AddTradeStatusHook(func(info client.TradeStatusInfo) {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.updated = time.Now().UnixMilli()
		switch info.Status {
		case client.TradeStatusBeginTrade: // someone opened a trade with you
			t.state, t.with, t.mine, t.theirs, t.theyAccepted, t.err = "requested", info.TraderGUID, types.TradeSide{}, types.TradeSide{}, false, [2]string{}
			go func(g uint64) {
				for i := 0; b.unitName(g) == "" && i < 10; i++ { // the name query may still be on its way
					time.Sleep(200 * time.Millisecond)
				}
				t.mu.Lock()
				tr := b.tradeSnapshot(t)
				t.mu.Unlock()
				b.emit("trade", tr)
			}(info.TraderGUID)
			return
		case client.TradeStatusOpenWindow:
			t.state = "open"
		case client.TradeStatusTradeAccept:
			t.theyAccepted = true
		case client.TradeStatusBackToTrade:
			t.theyAccepted = false
		case client.TradeStatusTradeComplete:
			t.state = "complete"
		default:
			t.refused(info)
		}
		b.emit("trade", b.tradeSnapshot(t))
	})
	w.AddPacketHook(func(op uint16, data []byte) {
		if op != client.SmsgTradeStatusExt {
			return
		}
		r := bytes.NewReader(data)
		u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
		theirs, _ := r.ReadByte()
		u32()
		u32()
		u32()
		offer := types.TradeSide{Money: u32(), Items: []types.Item{}}
		u32() // spell
		for r.Len() > 0 {
			slot, err := r.ReadByte()
			if err != nil {
				break
			}
			var f [18]uint32
			binary.Read(r, binary.LittleEndian, &f)
			if f[0] != 0 && slot < 6 {
				it := itemTemplate(w, f[0])
				it.Count = f[2]
				offer.Items = append(offer.Items, it)
			}
		}
		t.mu.Lock()
		if theirs != 0 {
			t.theirs = offer
		} else {
			t.mine = offer
		}
		t.mu.Unlock()
	})
	e := b.lua
	// a request that resolves when the trade reaches one of the ok states, or fails on an ending state
	until := func(op string, args map[string]any, timeout time.Duration, ok ...string) map[string]any {
		return b.request(op, args, timeout, func() *script.Outcome {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.err[0] != "" {
				return script.Fail(t.err[0], t.err[1], nil)
			}
			for _, s := range ok {
				if t.state == s {
					return script.Ok(b.tradeSnapshot(t))
				}
			}
			return nil
		}, nil)
	}
	clearErr := func() { t.mu.Lock(); t.err, t.stepAt = [2]string{}, time.Now(); t.mu.Unlock() }
	e.Func("trade.start", "(player) → request<Trade>", "ask a player within ~10 yd (name or guid) to trade: ok once they open the window (trade.open on their side)", func(who string) map[string]any {
		args := map[string]any{"player": who}
		var g uint64
		if _, err := fmt.Sscan(who, &g); err != nil || g == 0 {
			for _, p := range b.GetNearbyPlayers(15) {
				if p.Name == who {
					g = p.GUID
				}
			}
		}
		if g == 0 {
			return b.refuse("trade.start", args, "player_not_found", fmt.Sprintf("no player %q within 15 yards", who))
		}
		t.mu.Lock()
		t.state, t.with, t.mine, t.theirs, t.theyAccepted, t.err = "waiting", g, types.TradeSide{}, types.TradeSide{}, false, [2]string{}
		t.mu.Unlock()
		if err := w.InitiateTrade(g); err != nil {
			return b.refuse("trade.start", args, "failed", err.Error())
		}
		return until("trade.start", args, 30*time.Second, "open")
	})
	e.Func("trade.open", "() → request<Trade>", "open the window for someone's trade request (a trade event with state requested)", func() map[string]any {
		clearErr()
		if err := w.BeginTrade(); err != nil {
			return b.refuse("trade.open", nil, "failed", err.Error())
		}
		return until("trade.open", nil, 5*time.Second, "open")
	})
	e.Func("trade.offer", "{items = {entry or guid, ...}, money = copper} → request", "set your side of the open trade (up to 6 items; replaces what you offered). Soulbound items can't be traded",
		func(o map[string]any) map[string]any {
			items, _ := o["items"].([]any)
			money, _ := o["money"].(float64)
			clearErr()
			for slot := uint8(0); slot < 6; slot++ {
				if int(slot) >= len(items) {
					w.ClearTradeItem(slot)
					continue
				}
				key := guidArg(items[slot])
				it := findItem(w, key)
				if it == nil {
					return b.refuse("trade.offer", nil, "item_not_found", fmt.Sprintf("item %v not in your bags", items[slot]))
				}
				if it.Soulbound {
					return b.refuse("trade.offer", nil, "soulbound", it.Name+" is soulbound: it can't be traded")
				}
				if err := w.SetTradeItem(slot, it.Bag, it.BagSlot); err != nil {
					return b.refuse("trade.offer", nil, "failed", err.Error())
				}
			}
			return b.sent("trade.offer", nil, w.SetTradeGold(uint32(money)))
		})
	e.Func("trade.accept", "() → request<Trade>", "accept the trade: ok when it completes (both sides accepted; any change un-accepts both), else why not (not_enough_money, inventory_full, too_far, cancelled, ...)", func() map[string]any {
		clearErr()
		if err := w.AcceptTrade(); err != nil {
			return b.refuse("trade.accept", nil, "failed", err.Error())
		}
		return until("trade.accept", nil, 60*time.Second, "complete")
	})
	e.Func("trade.cancel", "() → request", "close the trade, or refuse a request", func() map[string]any { return b.sent("trade.cancel", nil, w.CancelTrade()) })
	e.Func("trade.status", "() → Trade", "the trade window: state, who with, both sides' offers", func() types.Trade {
		t.mu.Lock()
		defer t.mu.Unlock()
		return b.tradeSnapshot(t)
	})
}

// tradeEnds: TradeStatus codes that refuse or end a trade (WorldSession::HandleInitiateTradeOpcode, Player::TradeCancel)
var tradeEnds = map[uint32][2]string{client.TradeStatusBusy: {"busy", "they're busy (already trading)"},
	client.TradeStatusTradeCanceled: {"cancelled", "the trade was cancelled"}, client.TradeStatusBusy2: {"busy", "they're busy"},
	client.TradeStatusNoTarget: {"no_target", "no such player online"}, client.TradeStatusTradeRejected: {"rejected", "they declined"},
	client.TradeStatusTargetTooFar: {"too_far", "too far apart (within ~10 yd, neither of you on a flight)"},
	client.TradeStatusWrongFaction: {"wrong_faction", "they're the other faction"},
	client.TradeStatusCloseWindow:  {"closed", "the trade window closed (below the server's trade level?)"},
	client.TradeStatusIgnoreYou:    {"ignored", "they're ignoring you"}, client.TradeStatusYouStunned: {"stunned", "you're stunned"},
	client.TradeStatusTargetStunned: {"target_stunned", "they're stunned"}, client.TradeStatusYouDead: {"dead", "you're dead"},
	client.TradeStatusTargetDead: {"target_dead", "they're dead"}, client.TradeStatusYouLogout: {"logging_out", "you're logging out"},
	client.TradeStatusTargetLogout: {"target_logging_out", "they're logging out"},
	client.TradeStatusTrialAccount: {"trial_account", "trial accounts can't trade"},
	client.TradeStatusWrongRealm:   {"wrong_realm", "only conjured items can be traded across realms"},
	client.TradeStatusNotOnTapList: {"not_on_taplist", "that item is bound: only players there when it dropped can have it"}}

// refused records a status that ends the trade or refuses its last step (t.mu held). An accept the server undoes
// (CLOSE_WINDOW with an inventory result, TARGET_TO_FAR) and a refused item leave the trade open. A refused item or
// an accept with an untradeable item comes as TRADE_CANCELED to you alone (HandleSetTradeItemOpcode,
// HandleAcceptTradeOpcode), so one right after your own step is taken for that.
// ponytail: the other side cancelling in that same second reads as a refusal; the window is then shut server-side.
func (t *tradeState) refused(info client.TradeStatusInfo) {
	r, ok := tradeEnds[info.Status]
	if !ok {
		r = [2]string{slug(client.TradeStatusName(info.Status)), "the server refused the trade"}
	}
	switch {
	case info.Status == client.TradeStatusCloseWindow && info.Result != 0:
		code, msg := inventoryError([]byte{byte(info.Result)})
		t.err = [2]string{code, "the trade didn't go through: " + msg}
	case info.Status == client.TradeStatusTradeCanceled && t.state == "open" && time.Since(t.stepAt) < time.Second:
		t.err = [2]string{"untradeable", "the server refused that: an item that can't be traded, or the same item twice"}
	case info.Status == client.TradeStatusWrongRealm, info.Status == client.TradeStatusNotOnTapList,
		info.Status == client.TradeStatusTargetTooFar && t.state == "open":
		t.err, t.theyAccepted = r, false
	default:
		t.state, t.err = r[0], r
	}
}

// guidArg: an item key from Lua (an entry number or a guid string)
func guidArg(v any) uint64 {
	switch x := v.(type) {
	case float64:
		return uint64(x)
	case string:
		var g uint64
		fmt.Sscan(x, &g)
		return g
	}
	return 0
}
