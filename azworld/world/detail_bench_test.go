package world

import "testing"

// go test ./azworld/world -run "^$" -bench Detail -benchtime 30x: a warm tile render, mixed levels, Goldshire and Stormwind
func BenchmarkDetail(b *testing.B) {
	if !HasDetail(0) {
		b.Skip()
	}
	tiles := [][6]int{{49, 31, 1, 0, 0}, {49, 31, 3, 5, 6}, {49, 31, 4, 11, 13}, {48, 30, 2, 2, 3}, {48, 30, 4, 8, 12}, {48, 30, 3, 4, 6}}
	for _, t := range tiles { // warm the caches: ADTs, textures, vmap tiles and models
		DetailTile(0, t[0], t[1], t[2], t[3], t[4])
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t := tiles[i%len(tiles)]
		DetailTile(0, t[0], t[1], t[2], t[3], t[4])
	}
}
