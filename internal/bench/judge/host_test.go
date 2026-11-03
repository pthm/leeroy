package judge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pthm/leeroy/runfiles"
)

func TestHostSummary(t *testing.T) {
	f := runfiles.Run(t.TempDir())
	f.WriteManifest(runfiles.Manifest{Host: runfiles.Host{Name: "leeroy-1", CPUs: 8, Provider: "hetzner", Type: "ccx33", PriceHour: 0.5}})
	os.MkdirAll(filepath.Dir(f.Resources()), 0o755)
	var lines []byte
	for i, cpu := range []float64{10, 50, 90} {
		b, _ := json.Marshal(map[string]any{"t": int64(i) * 1800_000, "cpu_pct": cpu, "mem_used_mb": 1000 * float64(i+1), "mem_total_mb": 32000,
			"groups": map[string]any{"azsdk": map[string]any{"cpu_pct": cpu / 2, "mem_mb": 400, "n": 2}}})
		lines = append(append(lines, b...), '\n')
	}
	os.WriteFile(f.Resources(), lines, 0o644)
	b, _ := json.Marshal(hostSummary(string(f)))
	var h struct {
		Machine   runfiles.Host
		Resources struct {
			Samples       int
			Hours         float64
			CPUPct        struct{ Mean, P95, Peak float64 } `json:"cpu_pct"`
			MemUsedMBPeak float64                           `json:"mem_used_mb_peak"`
			CostEUR       float64                           `json:"cost_eur"`
			Groups        map[string]map[string]float64
		}
	}
	json.Unmarshal(b, &h)
	r := h.Resources
	if h.Machine.Type != "ccx33" || r.Samples != 3 || r.Hours != 1 || r.CPUPct.Mean != 50 || r.CPUPct.Peak != 90 || r.MemUsedMBPeak != 3000 ||
		r.CostEUR != 0.5 || r.Groups["azsdk"]["mem_mb_peak"] != 400 || r.Groups["azsdk"]["cpu_pct_mean"] != 25 {
		t.Fatalf("%s", b)
	}
}
