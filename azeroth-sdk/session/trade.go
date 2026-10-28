package session

// Player-to-player trade: the trade window as Lua calls. Either side can put items (up to 6) and money in; the trade
// happens when both accept, and any change un-accepts both. Within ~10 yards of each other.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/pthm/wowbench/azeroth-sdk/client"
)

type tradeOffer struct {
	Money uint32           `json:"money"`
	Items []map[string]any `json:"items"`
}

type tradeState struct {
	mu           sync.Mutex
	state        string // none, requested (someone asked you), open, complete, cancelled, ...
	with         uint64
	mine, theirs tradeOffer
	theyAccepted bool
	updated      int64
}

func (b *Bot) setupTrade(notify func(string)) {
	t := &tradeState{state: "none"}
	w := b.world
	w.AddTradeStatusHook(func(info client.TradeStatusInfo) {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.updated = time.Now().UnixMilli()
		switch info.Status {
		case client.TradeStatusBeginTrade: // someone opened a trade with you
			t.state, t.with, t.mine, t.theirs, t.theyAccepted = "requested", info.TraderGUID, tradeOffer{}, tradeOffer{}, false
			go func(g uint64) {
				name := b.unitName(g)
				for i := 0; name == "" && i < 10; i++ { // the name query may still be on its way
					time.Sleep(200 * time.Millisecond)
					name = b.unitName(g)
				}
				notify(fmt.Sprintf("%s wants to trade with you: bot.trade_open() to open the window, bot.trade_cancel() to refuse", name))
			}(info.TraderGUID)
		case client.TradeStatusOpenWindow:
			t.state = "open"
		case client.TradeStatusTradeAccept:
			t.theyAccepted = true
		case client.TradeStatusBackToTrade:
			t.theyAccepted = false
		case client.TradeStatusTradeComplete:
			t.state = "complete"
			go notify("trade complete")
		default:
			if !w.TradeOpen() {
				t.state = map[uint32]string{client.TradeStatusTradeCanceled: "cancelled", client.TradeStatusTargetTooFar: "too far",
					client.TradeStatusTradeRejected: "rejected", client.TradeStatusBusy: "busy", client.TradeStatusBusy2: "busy",
					client.TradeStatusNoTarget: "no target", client.TradeStatusCloseWindow: "closed"}[info.Status]
				if t.state == "" {
					t.state = client.TradeStatusName(info.Status)
				}
			}
		}
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
		offer := tradeOffer{Money: u32(), Items: []map[string]any{}}
		u32() // spell
		for r.Len() > 0 {
			slot, err := r.ReadByte()
			if err != nil {
				break
			}
			var f [18]uint32
			binary.Read(r, binary.LittleEndian, &f)
			if f[0] != 0 && slot < 6 {
				it := map[string]any{"slot": slot, "entry": f[0], "count": f[2]}
				if p := itemInfo(w, f[0]); p != nil {
					it["name"], it["quality"], it["item_level"] = p.Name, p.Quality, p.ItemLevel
				}
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
	// trade(player): open a trade with a player (name or guid) within ~10 yards; they get a message to accept
	e.RegisterFunc("trade", func(who string) error {
		var g uint64
		if _, err := fmt.Sscan(who, &g); err != nil || g == 0 {
			for _, p := range b.GetNearbyPlayers(15) {
				if p.Name == who {
					g = p.GUID
				}
			}
		}
		if g == 0 {
			return fmt.Errorf("no player %q within 15 yards", who)
		}
		t.mu.Lock()
		t.state, t.with, t.mine, t.theirs, t.theyAccepted = "asked", g, tradeOffer{}, tradeOffer{}, false
		t.mu.Unlock()
		return w.InitiateTrade(g)
	})
	e.RegisterFunc("trade_open", func() error { return w.BeginTrade() }) // accept someone's trade request
	// trade_item(slot, item): put an item (entry or guid) in trade slot 0-5; nil item clears the slot.
	// trade_money(copper): the money you offer. bot.trade_offer{items, money} (act.lua) wraps both.
	e.RegisterFunc("trade_item", func(slot uint8, item uint64) error {
		if slot > 5 {
			return fmt.Errorf("trade slots are 0-5")
		}
		if item == 0 {
			return w.ClearTradeItem(slot)
		}
		it := findItem(w, item)
		if it == nil {
			return fmt.Errorf("item %d not in your bags", item)
		}
		if it["soulbound"] == true {
			return fmt.Errorf("%v is soulbound: it can't be traded", it["entry"])
		}
		return w.SetTradeItem(slot, it["bag"].(uint8), it["slot"].(uint8))
	})
	e.RegisterFunc("trade_money", func(copper uint32) error { return w.SetTradeGold(copper) })
	e.RegisterFunc("trade_accept", func() error { return w.AcceptTrade() })
	e.RegisterFunc("trade_cancel", func() error { return w.CancelTrade() })
	// trade_status() -> {state, with, mine = {money, items}, theirs = {money, items}, they_accepted}
	e.RegisterFunc("trade_status", func() map[string]any {
		t.mu.Lock()
		defer t.mu.Unlock()
		out := map[string]any{"state": t.state, "mine": t.mine, "theirs": t.theirs, "they_accepted": t.theyAccepted}
		if t.with != 0 {
			out["with"] = b.unitName(t.with)
		}
		return out
	})
}
