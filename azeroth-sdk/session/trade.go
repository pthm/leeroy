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
	updated      int64
}

// snapshot is the trade as types.Trade (t.mu held).
func (b *Bot) tradeSnapshot(t *tradeState) types.Trade {
	tr := types.Trade{State: t.state, Mine: t.mine, Theirs: t.theirs, TheyAccepted: t.theyAccepted}
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
			t.state, t.with, t.mine, t.theirs, t.theyAccepted = "requested", info.TraderGUID, types.TradeSide{}, types.TradeSide{}, false
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
			if !w.TradeOpen() {
				t.state = map[uint32]string{client.TradeStatusTradeCanceled: "cancelled", client.TradeStatusTargetTooFar: "too_far",
					client.TradeStatusTradeRejected: "rejected", client.TradeStatusBusy: "busy", client.TradeStatusBusy2: "busy",
					client.TradeStatusNoTarget: "no_target", client.TradeStatusCloseWindow: "closed"}[info.Status]
				if t.state == "" {
					t.state = slug(client.TradeStatusName(info.Status))
				}
			}
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
			for _, s := range ok {
				if t.state == s {
					return script.Ok(b.tradeSnapshot(t))
				}
			}
			switch t.state {
			case "cancelled", "rejected", "busy", "too_far", "no_target", "closed":
				return script.Fail(t.state, "the trade ended: "+t.state, nil)
			}
			return nil
		}, nil)
	}
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
		t.state, t.with, t.mine, t.theirs, t.theyAccepted = "waiting", g, types.TradeSide{}, types.TradeSide{}, false
		t.mu.Unlock()
		if err := w.InitiateTrade(g); err != nil {
			return b.refuse("trade.start", args, "failed", err.Error())
		}
		return until("trade.start", args, 30*time.Second, "open")
	})
	e.Func("trade.open", "() → request<Trade>", "open the window for someone's trade request (a trade event with state requested)", func() map[string]any {
		if err := w.BeginTrade(); err != nil {
			return b.refuse("trade.open", nil, "failed", err.Error())
		}
		return until("trade.open", nil, 5*time.Second, "open")
	})
	e.Func("trade.offer", "{items = {entry or guid, ...}, money = copper} → request", "set your side of the open trade (up to 6 items; replaces what you offered). Soulbound items can't be traded",
		func(o map[string]any) map[string]any {
			items, _ := o["items"].([]any)
			money, _ := o["money"].(float64)
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
	e.Func("trade.accept", "() → request<Trade>", "accept the trade: ok when it completes (both sides accepted; any change un-accepts both)", func() map[string]any {
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
