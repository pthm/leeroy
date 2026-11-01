package main

import (
	"os"
	"testing"
)

// AZVIEW_RUN=<run dir> go test ./azview -run '^$' -bench StatsAll: the Stats panel over a whole real run
func BenchmarkStatsAll(b *testing.B) {
	dir := os.Getenv("AZVIEW_RUN")
	if dir == "" {
		b.Skip("set AZVIEW_RUN")
	}
	s, src := OpenRun(dir, nil, 0)
	s.Poll(src)
	q := Query{T: s.last, Tab: "stats", Metric: "dmg", Range: "all", Chart: "total", Width: 480}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.mu.RLock()
		s.Stats(q)
		s.mu.RUnlock()
	}
}
