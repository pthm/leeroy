package main

// How an agent played, from its trajectory (agents/<agent>/trajectory.json, ATIF; rebuilt from the transcript when a
// run has none): its tool calls by what they ran, how many failed and how long they took, its model calls and output
// per call, and its cost over the run where the steps price their model calls.

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pthm/leeroy/runfiles"

	"github.com/pthm/leeroy/internal/bench/run"
	"github.com/pthm/leeroy/trajectory"
	"golang.org/x/exp/eval/trajectory/atif"
)

// toolUse is one kind of tool call: a wow subcommand ("wow eval"), another of the agents' programs, or a runtime's
// own tool (read, edit, ...).
type toolUse struct {
	Name    string  `json:"name"`
	Calls   int     `json:"calls"`
	Failed  int     `json:"failed"`
	MedianS float64 `json:"median_s,omitempty"` // none when the runtime gives no durations (Codex)
}

// play is what publish keeps of a trajectory. ModelCalls and OutputPerCall only where the steps are model calls
// (Claude Code, opencode; Codex's steps are its items).
type play struct {
	Tools         []toolUse `json:"tools"`
	Calls         int       `json:"calls"`
	FailedPct     float64   `json:"failed_pct"`
	ModelCalls    int       `json:"model_calls,omitempty"`
	OutputPerCall float64   `json:"output_per_call,omitempty"`
}

// readTrajectory reads an agent's trajectory, or builds it from the transcript.
func readTrajectory(adir string, info trajectory.Info) *atif.Trajectory {
	var t atif.Trajectory
	if b, err := os.ReadFile(filepath.Join(adir, "trajectory.json")); err == nil && json.Unmarshal(b, &t) == nil {
		return &t
	}
	info.Usage = filepath.Join(adir, "usage.json")
	if p, err := trajectory.Read(filepath.Join(adir, "transcript.jsonl"), info); err == nil && len(p.Steps) > 1 {
		return p
	}
	return nil
}

// wowCommands: wow's subcommands (azsdk/cmd/wow); anything else is Lua to evaluate
var wowCommands = map[string]bool{
	"status": true, "look": true, "fights": true, "fight": true, "tasks": true, "answer": true,
	"events": true, "wait": true, "subscribe": true, "help": true,
}

// toolName: what a call ran: "wow <subcommand>" for wow, the program for the agents' other tools, else the tool.
func toolName(c atif.ToolCall) string {
	cmd, _ := c.Arguments["command"].(string)
	if cmd == "" {
		return strings.ToLower(c.FunctionName)
	}
	fam := run.CommandFamily(cmd)
	if fam == "wow" {
		words := strings.Fields(strings.NewReplacer("'", " ", `"`, " ").Replace(cmd))
		if i := slices.IndexFunc(words, func(w string) bool { return filepath.Base(w) == "wow" }); i >= 0 && i+1 < len(words) && wowCommands[words[i+1]] {
			return "wow " + words[i+1]
		}
		return "wow eval" // Lua, inline or from a file (-f)
	}
	if fam == "unknown" {
		return "other command"
	}
	return fam
}

func playOf(t *atif.Trajectory) *play {
	if t == nil {
		return nil
	}
	by := map[string]*toolUse{}
	durs := map[string][]float64{}
	p := &play{}
	failed, out := 0, 0
	for _, s := range t.Steps {
		if s.Metrics != nil {
			p.ModelCalls++
			out += s.Metrics.CompletionTokens
		}
		res := map[string]atif.ObservationResult{}
		if s.Observation != nil {
			for _, r := range s.Observation.Results {
				res[r.SourceCallID] = r
			}
		}
		for _, c := range s.ToolCalls {
			n := toolName(c)
			u := by[n]
			if u == nil {
				u = &toolUse{Name: n}
				by[n] = u
			}
			u.Calls++
			p.Calls++
			if r, ok := res[c.ToolCallID]; ok {
				if ok, _ := r.Extra["ok"].(bool); !ok {
					u.Failed++
					failed++
				}
				if d, ok := r.Extra["duration_ms"].(float64); ok {
					durs[n] = append(durs[n], d/1000)
				}
			}
		}
	}
	for n, u := range by {
		if d := durs[n]; len(d) > 0 {
			sort.Float64s(d)
			u.MedianS = math.Round(d[len(d)/2]*10) / 10
		}
		p.Tools = append(p.Tools, *u)
	}
	sort.Slice(p.Tools, func(i, j int) bool {
		return p.Tools[i].Calls > p.Tools[j].Calls || p.Tools[i].Calls == p.Tools[j].Calls && p.Tools[i].Name < p.Tools[j].Name
	})
	if p.Calls > 0 {
		p.FailedPct = math.Round(1000*float64(failed)/float64(p.Calls)) / 10
	}
	if p.ModelCalls > 0 {
		p.OutputPerCall = math.Round(float64(out) / float64(p.ModelCalls))
	} else {
		p.ModelCalls = 0
	}
	return p
}

// trajectoryCost: cumulative cost over the run from the steps that price a model call: [s from start, USD], thinned to
// about costPoints points; nil when no step does (Codex).
func trajectoryCost(t *atif.Trajectory, start time.Time) [][2]float64 {
	if t == nil {
		return nil
	}
	type pt struct{ t, usd float64 }
	var pts []pt
	for _, s := range t.Steps {
		if s.Metrics == nil || s.Metrics.CostUSD == 0 {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, s.Timestamp)
		if err != nil {
			continue
		}
		pts = append(pts, pt{at.Sub(start).Seconds(), s.Metrics.CostUSD})
	}
	if len(pts) == 0 {
		return nil
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].t < pts[j].t })
	var out [][2]float64
	total, step := 0.0, max(1, len(pts)/costPoints)
	for i, p := range pts {
		total += p.usd
		if i%step == 0 || i == len(pts)-1 {
			out = append(out, [2]float64{math.Round(p.t), math.Round(total*1000) / 1000})
		}
	}
	return out
}

// downloads: a player's files for the run page's downloads, put in the assets dir as the bucket keeps them (just
// upload copies them there): its trajectory (written into the run first when the run kept none, as publish rebuilt
// it) and its game log, gzipped. Their paths in the bucket; a file that couldn't be put there isn't listed.
func downloads(f runfiles.Run, id, agent, char string, t *atif.Trajectory, assets string) map[string]string {
	out := map[string]string{}
	if _, err := os.Stat(f.Trajectory(agent)); err != nil && t != nil {
		if b, err := json.MarshalIndent(t, "", "  "); err == nil {
			_ = os.WriteFile(f.Trajectory(agent), append(b, '\n'), 0o644) // not written: the page goes without it (below)
		}
	}
	if b, err := os.ReadFile(f.Trajectory(agent)); err == nil {
		path := "runs/" + id + "/trajectories/" + agent + ".json"
		if put(assets, path, scrub(b)) == nil {
			out["trajectory"] = path
		}
	}
	if b, err := os.ReadFile(f.Events(char)); err == nil {
		var z bytes.Buffer
		w, _ := gzip.NewWriterLevel(&z, gzip.BestCompression)
		_, _ = w.Write(scrub(b))
		path := "runs/" + id + "/game/" + char + ".jsonl.gz"
		if w.Close() == nil && put(assets, path, z.Bytes()) == nil {
			out["game_log"] = path
		}
	}
	return out
}

// homeDir: a home directory starting a path (/Users/<name>, /home/<name>), in agents' tool calls and the client's logs:
// it names the account that ran the run, which publishing leaves out.
// It starts a path after a space, quote, bracket or separator, or a JSON escape (\n, \u003e), not after a word (a URL's
// /home/ stays).
var homeDir = regexp.MustCompile(`(^|[\s"'(\[=:,;|>` + "`" + `]|\\[ntr"]|\\u00[0-9a-f]{2})/(?:Users|home)/[^/"'\\\s]+`)

// scrub: b with every home directory written ~ (/Users/pat/leeroy/var -> ~/leeroy/var), for what publish makes public.
func scrub(b []byte) []byte { return homeDir.ReplaceAll(b, []byte("${1}~")) }

// put writes b to path under the assets dir.
func put(assets, path string, b []byte) error {
	f := filepath.Join(assets, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	return os.WriteFile(f, b, 0o644)
}
