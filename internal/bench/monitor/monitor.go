// Package monitor samples the machine a run is on, to choose servers by: CPU, load, memory, disk and network for the
// whole host, and CPU and memory for each kind of process that matters (the azsdk sessions, the agents' runtimes, the
// worldserver, authserver and MySQL, which /proc shows from inside their containers too), one JSON line per sample
// (runfiles: server/resources.jsonl). Linux only: it reads /proc; elsewhere Run returns at once.
package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Groups are the processes sampled, by their name (/proc/<pid>/comm); everything else counts only in the host totals.
var Groups = []string{"azsdk", "claude", "codex", "opencode", "node", "worldserver", "authserver", "mysqld"}

// Usage is one group's share: CPU as a percentage of one core (a busy 4-thread process is 400), resident memory.
type Usage struct {
	CPUPct float64 `json:"cpu_pct"`
	MemMB  float64 `json:"mem_mb"`
	N      int     `json:"n"` // processes
}

// Sample is the machine at one moment; rates are over the interval before it.
type Sample struct {
	T           int64            `json:"t"`       // unix ms
	CPUPct      float64          `json:"cpu_pct"` // every core busy: 100
	Load1       float64          `json:"load1"`
	MemUsedMB   float64          `json:"mem_used_mb"` // total less available
	MemTotalMB  float64          `json:"mem_total_mb"`
	DiskUsedGB  float64          `json:"disk_used_gb"` // the root filesystem
	DiskTotalGB float64          `json:"disk_total_gb"`
	NetRxKBs    float64          `json:"net_rx_kbs"` // every interface but lo
	NetTxKBs    float64          `json:"net_tx_kbs"`
	Groups      map[string]Usage `json:"groups"`
}

const clkTck = 100 // USER_HZ: /proc's CPU times are in 1/100 s on Linux

// Run appends a sample to path every interval until ctx ends.
func Run(ctx context.Context, path string, every time.Duration) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prev := read("/proc", time.Now())
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-t.C:
			cur := read("/proc", now)
			b, _ := json.Marshal(diff(prev, cur))
			f.Write(append(b, '\n'))
			prev = cur
		}
	}
}

// MemoryGB is the machine's memory (0 where /proc/meminfo isn't).
func MemoryGB() float64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	return round(parseMeminfo(string(b))["MemTotal"] / 1024 / 1024)
}

// snapshot is what one read of /proc gives: cumulative counters, turned into a Sample by diff.
type snapshot struct {
	at                  time.Time
	busy, total         uint64 // /proc/stat's cpu line
	load1               float64
	memTotal, memAvail  float64 // kB
	diskUsed, diskTotal float64 // bytes
	rx, tx              uint64
	procs               map[int]proc
}

type proc struct {
	group string
	ticks uint64  // utime + stime
	rss   float64 // kB
}

func read(root string, at time.Time) snapshot {
	s := snapshot{at: at, procs: map[int]proc{}}
	if b, err := os.ReadFile(filepath.Join(root, "stat")); err == nil {
		s.busy, s.total = parseStat(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(root, "loadavg")); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			s.load1, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, "meminfo")); err == nil {
		m := parseMeminfo(string(b))
		s.memTotal, s.memAvail = m["MemTotal"], m["MemAvailable"]
	}
	if b, err := os.ReadFile(filepath.Join(root, "net/dev")); err == nil {
		s.rx, s.tx = parseNetDev(string(b))
	}
	var fs syscall.Statfs_t
	if syscall.Statfs("/", &fs) == nil {
		s.diskTotal = float64(fs.Blocks) * float64(fs.Bsize)
		s.diskUsed = s.diskTotal - float64(fs.Bavail)*float64(fs.Bsize)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, e.Name(), "stat"))
		if err != nil {
			continue
		}
		if p, ok := parsePidStat(string(b)); ok {
			s.procs[pid] = p
		}
	}
	return s
}

func diff(a, b snapshot) Sample {
	dt := b.at.Sub(a.at).Seconds()
	out := Sample{T: b.at.UnixMilli(), Load1: b.load1, MemTotalMB: round(b.memTotal / 1024), MemUsedMB: round((b.memTotal - b.memAvail) / 1024),
		DiskUsedGB: round(b.diskUsed / 1e9), DiskTotalGB: round(b.diskTotal / 1e9), Groups: map[string]Usage{}}
	if b.total > a.total {
		out.CPUPct = round(100 * float64(b.busy-a.busy) / float64(b.total-a.total))
	}
	if dt > 0 && b.rx >= a.rx && b.tx >= a.tx {
		out.NetRxKBs, out.NetTxKBs = round(float64(b.rx-a.rx)/1024/dt), round(float64(b.tx-a.tx)/1024/dt)
	}
	for pid, p := range b.procs {
		u := out.Groups[p.group]
		u.N++
		u.MemMB += p.rss / 1024
		if q, ok := a.procs[pid]; ok && q.ticks <= p.ticks && dt > 0 { // new since the last sample: its CPU counts next time
			u.CPUPct += 100 * float64(p.ticks-q.ticks) / clkTck / dt
		}
		out.Groups[p.group] = u
	}
	for g, u := range out.Groups {
		u.CPUPct, u.MemMB = round(u.CPUPct), round(u.MemMB)
		out.Groups[g] = u
	}
	return out
}

func round(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

// parseStat: /proc/stat's first line, busy and total jiffies (idle and iowait are idle).
func parseStat(s string) (busy, total uint64) {
	line, _, _ := strings.Cut(s, "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0
	}
	for i, x := range f[1:] {
		v, _ := strconv.ParseUint(x, 10, 64)
		total += v
		if i != 3 && i != 4 { // idle, iowait
			busy += v
		}
	}
	return busy, total
}

// parseMeminfo: /proc/meminfo's values, kB.
func parseMeminfo(s string) map[string]float64 {
	out := map[string]float64{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if f := strings.Fields(v); ok && len(f) > 0 {
			out[k], _ = strconv.ParseFloat(f[0], 64)
		}
	}
	return out
}

// parseNetDev: bytes received and sent on every interface but lo.
func parseNetDev(s string) (rx, tx uint64) {
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok || strings.TrimSpace(name) == "lo" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx, tx = rx+r, tx+t
	}
	return rx, tx
}

// parsePidStat: a process's group (its comm, if one of Groups), CPU ticks and resident memory, from /proc/<pid>/stat.
func parsePidStat(s string) (proc, bool) {
	open, close := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || close < open {
		return proc{}, false
	}
	comm := s[open+1 : close]
	group := ""
	for _, g := range Groups {
		if comm == g || strings.HasPrefix(comm, g) && g == "mysqld" {
			group = g
		}
	}
	if group == "" {
		return proc{}, false
	}
	f := strings.Fields(s[close+1:]) // f[0] is the state, field 3 of the full line
	if len(f) < 22 {
		return proc{}, false
	}
	ut, _ := strconv.ParseUint(f[11], 10, 64) // utime, field 14
	st, _ := strconv.ParseUint(f[12], 10, 64) // stime, field 15
	rss, _ := strconv.ParseFloat(f[21], 64)   // rss in pages, field 24
	return proc{group: group, ticks: ut + st, rss: rss * float64(os.Getpagesize()) / 1024}, true
}
