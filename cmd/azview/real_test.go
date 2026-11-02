package main

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// AZVIEW_RUN=<run dir> go test ./azview -run TestRealRun -v: load a real run, report the time and heap it takes.
func TestRealRun(t *testing.T) {
	dir := os.Getenv("AZVIEW_RUN")
	if dir == "" {
		t.Skip("set AZVIEW_RUN to a run dir")
	}
	start := time.Now()
	s, src := OpenRun(dir, nil, 0)
	s.Poll(src)
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	n := 0
	for _, p := range s.players {
		n += len(p.Track)
	}
	t.Logf("%d players, %d samples, %d events in %v; heap %d MB", len(s.players), n, len(s.events), time.Since(start), m.HeapAlloc>>20)
}
