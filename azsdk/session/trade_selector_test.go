package session

import (
	"math"
	"strings"
	"testing"
)

func TestTradeItemSelectorIsUnambiguous(t *testing.T) {
	for _, v := range []any{float64(0), float64(-1), 1.5, math.NaN(), math.Inf(1), float64(1 << 32), "117", "4294967296 trailing", "+4294967296", "", true} {
		if guidArg(v) != 0 {
			t.Fatalf("accepted invalid selector %#v", v)
		}
	}
	if guidArg(float64(117)) != 117 || guidArg("4294967296") != 1<<32 {
		t.Fatal("valid selectors rejected")
	}
}

// a selector of the wrong shape says which item and what an item is ({{858, 1}} isn't one)
func TestTradeOfferShapeError(t *testing.T) {
	b := testBot()
	b.setupTrade()
	_, err := b.lua.Eval(`trade.offer{items = {858, {858, 1}}}`)
	if err == nil || !strings.Contains(err.Error(), "items[2] is a table (an array); each item is an item entry or a carried item's GUID string, e.g. items = {858,") {
		t.Fatal(err)
	}
}
