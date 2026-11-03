package judge

// The machine a run ran on and how hard it worked (run.json's host, server/resources.jsonl), for the report: to see
// whether a server type was too small or too big for a task.

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"sort"

	"github.com/pthm/leeroy/internal/bench/monitor"
	"github.com/pthm/leeroy/runfiles"
)

// hostSummary: the host, and when the run was monitored, its samples summed up: CPU (mean, 95th percentile, peak),
// load, memory and disk (peak), network (mean), each process group's CPU (mean, peak) and memory (peak), and what the
// server cost over the samples' span at its list price.
func hostSummary(dir string) obj {
	f := runfiles.Run(dir)
	m, _ := f.ReadManifest()
	out := obj{{"machine", m.Host}}
	fh, err := os.Open(f.Resources())
	if err != nil {
		return out
	}
	defer fh.Close()
	var ss []monitor.Sample
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<16), 1<<22)
	for sc.Scan() {
		var s monitor.Sample
		if json.Unmarshal(sc.Bytes(), &s) == nil {
			ss = append(ss, s)
		}
	}
	if len(ss) == 0 {
		return out
	}
	col := func(get func(monitor.Sample) float64) []float64 {
		v := make([]float64, len(ss))
		for i, s := range ss {
			v[i] = get(s)
		}
		return v
	}
	cpu := col(func(s monitor.Sample) float64 { return s.CPUPct })
	hours := float64(ss[len(ss)-1].T-ss[0].T) / 3.6e6
	groups := obj{}
	for _, g := range monitor.Groups {
		gcpu := col(func(s monitor.Sample) float64 { return s.Groups[g].CPUPct })
		gmem := col(func(s monitor.Sample) float64 { return s.Groups[g].MemMB })
		if peak(gmem) == 0 {
			continue
		}
		groups = append(groups, kv{g, obj{
			{"cpu_pct_mean", r1(mean(gcpu))},
			{"cpu_pct_peak", r1(peak(gcpu))},
			{"mem_mb_peak", r1(peak(gmem))},
			{"processes_peak", peak(col(func(s monitor.Sample) float64 { return float64(s.Groups[g].N) }))},
		}})
	}
	res := obj{
		{"samples", len(ss)},
		{"hours", r1(hours)},
		{"cpu_pct", obj{{"mean", r1(mean(cpu))}, {"p95", r1(p95(cpu))}, {"peak", r1(peak(cpu))}}},
		{"load1_peak", r1(peak(col(func(s monitor.Sample) float64 { return s.Load1 })))},
		{"mem_used_mb_peak", r1(peak(col(func(s monitor.Sample) float64 { return s.MemUsedMB })))},
		{"mem_total_mb", ss[len(ss)-1].MemTotalMB},
		{"disk_used_gb_peak", r1(peak(col(func(s monitor.Sample) float64 { return s.DiskUsedGB })))},
		{"net_rx_kbs_mean", r1(mean(col(func(s monitor.Sample) float64 { return s.NetRxKBs })))},
		{"net_tx_kbs_mean", r1(mean(col(func(s monitor.Sample) float64 { return s.NetTxKBs })))},
		{"groups", groups},
	}
	if m.Host.PriceHour > 0 {
		res = append(res, kv{"cost_eur", math.Round(m.Host.PriceHour*hours*100) / 100})
	}
	return append(out, kv{"resources", res})
}

func peak(v []float64) float64 {
	p := 0.0
	for _, x := range v {
		p = math.Max(p, x)
	}
	return p
}

func p95(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[int(math.Ceil(0.95*float64(len(s))))-1]
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return sum(v) / float64(len(v))
}
