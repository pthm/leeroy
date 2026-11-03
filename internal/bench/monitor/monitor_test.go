package monitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	busy, total := parseStat("cpu  100 0 50 800 50 0 0 0 0 0\ncpu0 1 2 3\n")
	if busy != 150 || total != 1000 {
		t.Fatalf("stat: %d %d", busy, total)
	}
	m := parseMeminfo("MemTotal:       32000000 kB\nMemAvailable:   20000000 kB\n")
	if m["MemTotal"] != 32e6 || m["MemAvailable"] != 20e6 {
		t.Fatalf("meminfo: %v", m)
	}
	rx, tx := parseNetDev("Inter-|   Receive\n face |bytes\n    lo: 999 1 0 0 0 0 0 0 999 1 0 0 0 0 0 0\n  eth0: 2048 10 0 0 0 0 0 0 1024 5 0 0 0 0 0 0\n")
	if rx != 2048 || tx != 1024 {
		t.Fatalf("net: %d %d", rx, tx)
	}
	p, ok := parsePidStat("4242 (azsdk) S 1 4242 4242 0 -1 4194560 100 0 0 0 300 200 0 0 20 0 12 0 5000 900000000 2500 18446744073709551615")
	if !ok || p.group != "azsdk" || p.ticks != 500 || p.rss != 2500*float64(os.Getpagesize())/1024 {
		t.Fatalf("pid stat: %+v %v", p, ok)
	}
	if _, ok := parsePidStat("1 (systemd) S 0 1 1 0 -1 4194560 0 0 0 0 1 1 0 0 20 0 1 0 1 1 1 1"); ok {
		t.Fatal("a process outside the groups was kept")
	}
}

func TestDiff(t *testing.T) {
	t0 := time.Unix(1000, 0)
	a := snapshot{at: t0, busy: 100, total: 1000, rx: 0, tx: 0, procs: map[int]proc{1: {"azsdk", 100, 1024}}}
	b := snapshot{at: t0.Add(10 * time.Second), busy: 400, total: 2000, memTotal: 4096 * 1024, memAvail: 1024 * 1024, rx: 10240, tx: 20480,
		procs: map[int]proc{1: {"azsdk", 300, 2048}, 2: {"azsdk", 50, 1024}}}
	s := diff(a, b)
	if s.CPUPct != 30 || s.MemUsedMB != 3072 || s.NetRxKBs != 1 || s.NetTxKBs != 2 {
		t.Fatalf("host: %+v", s)
	}
	// pid 1 used 200 ticks in 10 s = 20% of a core; pid 2 is new: its memory counts, its CPU next time
	if g := s.Groups["azsdk"]; g.CPUPct != 20 || g.MemMB != 3 || g.N != 2 {
		t.Fatalf("group: %+v", g)
	}
}

// On Linux, sampling this machine for a few seconds gives sane numbers.
func TestRunLive(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	path := filepath.Join(t.TempDir(), "resources.jsonl")
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	if err := Run(ctx, path, time.Second); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var s Sample
	if len(lines) < 2 || json.Unmarshal([]byte(lines[len(lines)-1]), &s) != nil || s.MemTotalMB <= 0 || s.DiskTotalGB <= 0 ||
		s.CPUPct < 0 || s.CPUPct > 100 || s.Groups == nil {
		t.Fatalf("%d lines, last %+v", len(lines), s)
	}
	if MemoryGB() <= 0 {
		t.Fatal("no memory")
	}
}
