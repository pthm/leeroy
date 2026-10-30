package session

import (
	"math"
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
