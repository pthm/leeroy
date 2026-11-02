package main

// A player's cost over the run, for the website: cumulative list-price cost at each point where the runtime reported
// token usage, priced the way the judge prices a whole run (judge.Cost). Claude Code reports usage per model request in
// its transcript (one request can span several transcript lines, each with the same message id: counted once); Codex
// keeps running totals in its own session files (~/.codex/sessions), named in usage.json. Thinned to about
// costPoints points of [s from the start, cumulative USD].

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pthm/leeroy/azbench/judge"
)

const costPoints = 200

type usageAt struct {
	t               float64 // s from the start
	in, out, cr, cw float64
	model           string
}

func costSeries(runtime, provider, model, adir string, start time.Time) [][2]float64 {
	var us []usageAt
	switch runtime {
	case "claude":
		us = claudeUsage(filepath.Join(adir, "transcript.jsonl"), start)
	case "codex":
		us = codexUsage(filepath.Join(adir, "usage.json"), start)
	}
	if len(us) == 0 {
		return nil
	}
	sort.Slice(us, func(i, j int) bool { return us[i].t < us[j].t })
	var out [][2]float64
	total := 0.0
	step := max(1, len(us)/costPoints)
	for i, u := range us {
		m := model
		if u.model != "" {
			m = u.model
		}
		if usd, ok := judge.Cost(runtime, provider, m, u.in, u.out, u.cr, u.cw); ok {
			total += usd
		}
		if i%step == 0 || i == len(us)-1 {
			out = append(out, [2]float64{math.Round(u.t), math.Round(total*1000) / 1000})
		}
	}
	return out
}

func claudeUsage(f string, start time.Time) []usageAt {
	fh, err := os.Open(f)
	if err != nil {
		return nil
	}
	defer fh.Close()
	seen := map[string]bool{}
	var out []usageAt
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		if !strings.Contains(sc.Text(), `"usage"`) {
			continue
		}
		var e struct {
			Type      string
			Timestamp time.Time
			Message   struct {
				ID, Model string
				Usage     struct {
					Input  float64 `json:"input_tokens"`
					Output float64 `json:"output_tokens"`
					Read   float64 `json:"cache_read_input_tokens"`
					Write  float64 `json:"cache_creation_input_tokens"`
				}
			}
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Type != "assistant" || e.Message.ID == "" || seen[e.Message.ID] {
			continue
		}
		seen[e.Message.ID] = true
		u := e.Message.Usage
		out = append(out, usageAt{t: e.Timestamp.Sub(start).Seconds(), in: u.Input, out: u.Output, cr: u.Read, cw: u.Write, model: e.Message.Model})
	}
	return out
}

func codexUsage(usageFile string, start time.Time) []usageAt {
	var sessions map[string]any
	if readJSON(usageFile, &sessions) != nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	var out []usageAt
	for k := range sessions {
		id, ok := strings.CutPrefix(k, "codex:")
		if !ok {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(home, ".codex/sessions/*/*/*/*"+id+".jsonl"))
		if len(files) == 0 {
			continue
		}
		fh, err := os.Open(files[0])
		if err != nil {
			continue
		}
		var last struct{ in, cached, out float64 }
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			if !strings.Contains(sc.Text(), `"token_count"`) {
				continue
			}
			var e struct {
				Timestamp time.Time
				Payload   struct {
					Info *struct {
						Total struct {
							Input  float64 `json:"input_tokens"`
							Cached float64 `json:"cached_input_tokens"`
							Output float64 `json:"output_tokens"`
						} `json:"total_token_usage"`
					}
				}
			}
			if json.Unmarshal(sc.Bytes(), &e) != nil || e.Payload.Info == nil {
				continue
			}
			t := e.Payload.Info.Total
			if t.Input < last.in { // a new count (a resumed session restarts its totals)
				last = struct{ in, cached, out float64 }{}
			}
			dIn, dCached, dOut := t.Input-last.in, t.Cached-last.cached, t.Output-last.out
			last = struct{ in, cached, out float64 }{t.Input, t.Cached, t.Output}
			if dIn <= 0 && dOut <= 0 {
				continue
			}
			// Codex's input count includes the cached part
			out = append(out, usageAt{t: e.Timestamp.Sub(start).Seconds(), in: dIn - dCached, cr: dCached, out: dOut})
		}
		fh.Close()
	}
	return out
}
