package world

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// DETAIL_SHOT=<out prefix> DETAIL_TILES="gx,gy,level,sx,sy,n": an n x n block of detail tiles, files <out>_<row><col>.jpg
func TestDetailShot(t *testing.T) {
	out := os.Getenv("DETAIL_SHOT")
	if out == "" || !HasDetail(0) {
		t.Skip()
	}
	var k [6]int
	for i, v := range strings.Split(os.Getenv("DETAIL_TILES"), ",") {
		k[i], _ = strconv.Atoi(v)
	}
	for dx := 0; dx < k[5]; dx++ {
		for dy := 0; dy < k[5]; dy++ {
			os.WriteFile(fmt.Sprintf("%s_%d%d.jpg", out, dx, dy), DetailTile(0, k[0], k[1], k[2], k[3]+dx, k[4]+dy), 0o644)
		}
	}
}
