package world

import (
	"os"
	"testing"
	"time"
)

// DETAIL_SWEEP=1: render every level-2 detail tile over Elwynn, Westfall and Stormwind, failing on any panic.
func TestDetailSweep(t *testing.T) {
	if os.Getenv("DETAIL_SWEEP") == "" || !HasDetail(0) {
		t.Skip("set DETAIL_SWEEP=1")
	}
	start, n := time.Now(), 0
	for gx := 46; gx <= 52; gx++ {
		for gy := 28; gy <= 35; gy++ {
			for sx := 0; sx < 4; sx++ {
				for sy := 0; sy < 4; sy++ {
					func() {
						defer func() {
							if e := recover(); e != nil {
								t.Errorf("tile %d,%d level 2 %d,%d: %v", gx, gy, sx, sy, e)
							}
						}()
						DetailTile(0, gx, gy, 2, sx, sy)
						n++
					}()
				}
			}
		}
	}
	t.Logf("%d tiles in %v", n, time.Since(start))
}
