package main

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"github.com/pthm/leeroy/azeroth-sdk/types"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pthm/leeroy/azbench/config"
	"github.com/pthm/leeroy/azbench/judge"
	"github.com/pthm/leeroy/azbench/run"
	"github.com/pthm/leeroy/azbench/runfiles"
)

// publish writes a run's results for the website (site/src/data/runs/<run>.json): the scenario, then each player's
// model, outcome, time, cost and tokens, from the run's scenario.yaml, report.json and team.json. Small and committed:
// the site builds from these, never from bench/runs.
func publish(root, runDir, task, out string) (string, error) {
	id := filepath.Base(filepath.Clean(runDir))
	f := runfiles.Run(runDir)
	man, err := f.ReadManifest()
	if err != nil || man.Started == 0 {
		return "", fmt.Errorf("%s: not a run that started (no run.json, or its clock never started)", runDir)
	}
	started := time.UnixMilli(man.Started)
	name := man.Scenario
	b, err := config.LoadBench(filepath.Join(root, "bench"))
	if err != nil {
		return "", err
	}
	s, err := b.LoadScenario(f.Scenario())
	if err != nil {
		return "", err
	}
	var report struct {
		Agents []map[string]any `json:"agents"`
	}
	var team struct {
		Agents []map[string]any `json:"agents"`
	}
	if err := readJSON(f.Report(), &report); err != nil {
		return "", err
	}
	if err := readJSON(f.Team(), &team); err != nil {
		return "", err
	}
	judged := map[string]map[string]any{}
	for _, a := range team.Agents {
		judged[fmt.Sprint(a["char"])] = a
	}
	agents := map[string]config.Agent{}
	for _, a := range s.Agents {
		agents[a.Char] = a
	}
	if s.Goal == "" {
		for _, a := range team.Agents {
			if g, ok := a["goal"].(string); ok && g != "" {
				s.Goal = g
			}
		}
	}
	pick := func(m map[string]any, keys ...string) map[string]any {
		o := map[string]any{}
		for _, k := range keys {
			if v, ok := m[k]; ok && v != nil {
				o[k] = v
			}
		}
		return o
	}
	var players []map[string]any
	chat := map[string]chatMsg{}
	var recs []*recording
	recOf := map[string]*recording{}
	for _, r := range report.Agents {
		char := fmt.Sprint(r["char"])
		p := pick(r, "char", "runtime", "model", "effort", "hours", "deaths", "cost_usd", "tokens", "calls", "time_pct_by_state", "time_use", "combat",
			"progress_per_hour_by_state", "coop", "idle_gaps", "stalls")
		if diag, ok := r["diagnostics"].(map[string]any); ok && diag["reliability"] != nil { // the tooling's: movement, routes, recovery
			p["reliability"] = diag["reliability"]
		}
		if th, ok := r["think_s"].(map[string]any); ok {
			p["think_median_s"] = th["median"]
		}
		p["failures"] = failures(r)
		if p["cost_usd"] == nil { // the first runs recorded tokens but no cost: at list price, as the judge prices them
			if t, ok := r["tokens"].(map[string]any); ok {
				f := func(k string) float64 { v, _ := t[k].(float64); return v }
				provider, _ := r["provider"].(string) // missing from the first reports
				if usd, ok := judge.Cost(fmt.Sprint(p["runtime"]), provider, fmt.Sprint(p["model"]), f("input"), f("output"), f("cache_read"), f("cache_write")); ok {
					p["cost_usd"] = math.Round(usd*10) / 10
				}
			}
		}
		a := agents[char]
		p["class"], p["spec"], p["race"] = a.Class, a.Spec, a.Race
		p["met"] = false
		for k, v := range pick(judged[char], "met", "time_to_goal_s", "value", "score", "level", "gave_up", "died") {
			p[k] = v
		}
		if _, ok := p["time_to_goal_s"]; !ok && p["met"] == true { // older judges: the clock stopped at the goal
			p["time_to_goal_s"] = judged[char]["elapsed_s"]
		}
		if judged[char] == nil { // the judge's final word never written (a paused or stopped run): its last score
			if sc, ok := r["score"].(map[string]any); ok {
				p["value"], p["score"] = sc["value"], sc["score"]
				if t, ok := sc["time_to_goal_s"].(float64); ok {
					p["met"], p["time_to_goal_s"] = true, t
				}
			}
		}
		if a.Name != "" {
			adir := f.Agent(a.Name)
			var meta struct{ Spec, Role string }
			if readJSON(filepath.Join(adir, "meta.json"), &meta) == nil {
				p["spec_name"], p["role"] = meta.Spec, meta.Role
				if a.Class == "" { // "Warrior - Protection"
					p["class"] = strings.ToLower(strings.TrimSpace(strings.Split(meta.Spec, " - ")[0]))
				}
			}
			p["series"], p["deaths_at"] = series(f.Samples(char))
			cs := costSeries(fmt.Sprint(p["runtime"]), fmt.Sprint(r["provider"]), fmt.Sprint(p["model"]), adir, started)
			// the transcript misses some requests (Claude Code's background calls): keep its shape, end at the run's cost
			if total, ok := p["cost_usd"].(float64); ok && len(cs) > 0 && cs[len(cs)-1][1] > 0 {
				k := total / cs[len(cs)-1][1]
				for i := range cs {
					cs[i][1] = math.Round(cs[i][1]*k*1000) / 1000
				}
			}
			p["cost_series"] = cs
			if k := kitOf(adir); k != nil {
				kitIcons(k, filepath.Join(out, "../../../public/icons"))
				p["kit"] = k
			}
			if outs := outages(f.Diagnostics(a.Name)); len(outs) > 0 { // provider outages waited out
				p["outages"] = outs
			}
			if p["effort"] == nil || p["effort"] == "" || p["effort"] == "default" { // unrecorded, or read before Codex wrote it: its session says
				p["effort"] = run.DetectEffort(fmt.Sprint(p["runtime"]), f.Transcript(a.Name))
			}
			if t := trajectory(f.Events(char), started.UnixMilli()); len(t) > 0 {
				p["trajectory"] = t
			}
			if rec, err := readRecording(f.Events(char), char, started.UnixMilli(), chat); err == nil {
				recs, recOf[char] = append(recs, rec), rec
			}
		}
		players = append(players, p)
	}
	nameDeaths(recs)
	var tracks, thin [][][5]float64
	end := 0.0
	for _, rec := range recs {
		if n := len(rec.track); n > 0 {
			end = math.Max(end, rec.track[n-1][0])
		}
	}
	for _, p := range players {
		rec := recOf[p["char"].(string)]
		if rec == nil {
			continue
		}
		if p["class"] == "" {
			p["class"] = rec.class
		}
		t := simplify(rec.raw)
		p["track"], p["death_list"], p["casts"] = t, rec.deaths, ownCasts(rec.casts, p["kit"])
		p["cast_icons"] = castIcons(p["casts"].(map[string]int), rec.spells, filepath.Join(out, "../../../public/icons"))
		tracks, thin = append(tracks, rec.track), append(thin, t)
	}
	maps, err := mapImages(id, thin, filepath.Join(out, "../../assets/runs"))
	if err != nil {
		return "", err
	}
	if task == "" {
		task = cmp.Or(s.Task, name)
	}
	if s.Minutes == 0 { // no scenario: the longest anyone played
		for _, p := range players {
			if h, ok := p["hours"].(float64); ok {
				s.Minutes = max(s.Minutes, int(math.Round(h*60)))
			}
		}
	}
	// the benchmark version: what the run recorded, else the version in force when it started
	ver := struct{ Version, SDKRev string }{man.Version, man.SDKRev}
	if ver.Version == "" {
		ver.Version = b.VersionAt(started).Version
	}
	vs, _ := json.MarshalIndent(b.Versions, "", "  ") // the list, for the site's version selector
	if err := os.WriteFile(filepath.Join(out, "../versions.json"), append(vs, '\n'), 0o644); err != nil {
		return "", err
	}
	doc := map[string]any{
		"id": id, "task": task, "version": ver.Version, "sdk_rev": ver.SDKRev, "scenario": name, "description": strings.TrimSpace(s.Description), "goal": s.Goal,
		"minutes": s.Minutes, "hardcore": s.Hardcore, "level": s.Level, "started": started.Format(time.RFC3339),
		"players": players, "maps": maps, "together": together(tracks, end), "chat_per_min": chatPerMinute(chat),
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(out, id+".json")
	data, _ := json.Marshal(doc) // compact: routes and series are thousands of numbers
	return dst, os.WriteFile(dst, append(data, '\n'), 0o644)
}

// benign: failures that are the SDK working as meant (a script replacing its own move, a swing that missed, a spell
// still on the global cooldown), not mistakes worth showing
var benign = map[string]bool{"replaced_by_owner": true, "cancelled": true, "task_stopped": true, "miss": true,
	"dodge": true, "parry": true, "not_ready": true}

// trajectory: a player's SDK calls minute by minute, from its session's events: [minute, succeeded, failed, the
// minute's most frequent failure ("op code"), its count], minutes with no calls left out. Benign failures count as
// succeeded.
func trajectory(f string, start int64) [][5]any {
	fh, err := os.Open(f)
	if err != nil {
		return nil
	}
	defer fh.Close()
	type minute struct {
		ok, failed int
		why        map[string]int
	}
	byMin := map[int]*minute{}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		if !bytes.Contains(sc.Bytes(), []byte(`"type":"result"`)) {
			continue
		}
		var e struct {
			T    int64
			Data struct {
				Op    string
				OK    bool
				Error struct{ Code string }
			}
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.T < start {
			continue
		}
		m := int((e.T - start) / 60000)
		if byMin[m] == nil {
			byMin[m] = &minute{why: map[string]int{}}
		}
		if e.Data.OK || benign[e.Data.Error.Code] {
			byMin[m].ok++
		} else {
			byMin[m].failed++
			byMin[m].why[e.Data.Op+" "+e.Data.Error.Code]++
		}
	}
	out := [][5]any{}
	for m, b := range byMin {
		top, n := "", 0
		for w, c := range b.why {
			if c > n || c == n && w < top {
				top, n = w, c
			}
		}
		out = append(out, [5]any{m, b.ok, b.failed, top, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0].(int) < out[j][0].(int) })
	return out
}

// failures: a player's most frequent failed calls, [{op, code, count}], at most 6.
func failures(r map[string]any) []map[string]any {
	diag, _ := r["diagnostics"].(map[string]any)
	ops, _ := diag["by_operation"].([]any)
	var out []map[string]any
	for _, o := range ops {
		m, _ := o.(map[string]any)
		if m["outcome"] == "failed" && !benign[fmt.Sprint(m["code"])] {
			out = append(out, map[string]any{"op": m["op"], "code": m["code"], "count": m["count"]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["count"].(float64) > out[j]["count"].(float64) })
	return out[:min(6, len(out))]
}

// seriesPoints: about how many points a player's progress keeps for the charts
const seriesPoints = 240

// series: a player's progress from the judge's samples (every 15 s), thinned to about seriesPoints points of
// [elapsed s, level (fractional), XP earned, deaths, quests, kills], the last always kept; and the elapsed seconds of
// every death, from each sample where the count rose.
func series(f string) ([][6]float64, []float64) {
	data, err := os.ReadFile(f)
	if err != nil {
		return nil, nil
	}
	var all [][6]float64
	deaths := []float64{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var s struct {
			Elapsed float64 `json:"elapsed_s"`
			Signals struct {
				Level, Deaths, Quests float64
				XP                    *float64 `json:"xp_earned"` // missing from the first runs' samples: -1
				Kills                 float64  `json:"creatures_killed"`
			} `json:"signals"`
		}
		if json.Unmarshal([]byte(line), &s) != nil {
			continue
		}
		g := s.Signals
		if n := len(all); n > 0 && g.Deaths > all[n-1][3] {
			deaths = append(deaths, s.Elapsed)
		}
		xp := -1.0
		if g.XP != nil {
			xp = *g.XP
		}
		all = append(all, [6]float64{s.Elapsed, math.Round(g.Level*100) / 100, xp, g.Deaths, g.Quests, g.Kills})
	}
	step := max(1, len(all)/seriesPoints)
	var out [][6]float64
	for i := 0; i < len(all); i += step {
		out = append(out, all[i])
	}
	if len(all) > 0 && out[len(out)-1] != all[len(all)-1] {
		out = append(out, all[len(all)-1])
	}
	return out, deaths
}

func readJSON(f string, v any) error {
	data, err := os.ReadFile(f)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// kitOf: the character's gear, talents and abilities as its run left them (kit.json, saved at the end of the run).
func kitOf(adir string) *run.Kit {
	var k run.Kit
	if readJSON(filepath.Join(adir, "kit.json"), &k) != nil {
		return nil
	}
	return &k
}

// ownCasts: the character's casts without the game's own effects (LOGINEFFECT, Ghost, eating and drinking). Starting
// spells aren't in the characters database, so casts aren't matched against the kit; the recording's casts can include
// effects triggered on the character (a paladin's Judgement of Light healing whoever hits its target), so the common
// ones are dropped by name. ponytail: a name list; the recorder could flag triggered casts if more turn up.
var triggered = map[string]bool{"Judgement of Light": true, "Judgement of Wisdom": true, "Chilled": true, "Resurrection Sickness": true}

func ownCasts(casts map[string]int, kit any) map[string]int {
	trained := map[string]bool{} // a triggered name the character also trained (a paladin's own Judgement of Light) stays
	if k, ok := kit.(*run.Kit); ok {
		for _, a := range k.Abilities {
			trained[a.Name] = true
		}
	}
	out := map[string]int{}
	for name, n := range casts {
		switch {
		case strings.ToUpper(name) == name, name == "Ghost", name == "Food", name == "Drink", name == "Refreshment",
			triggered[name] && !trained[name], strings.HasPrefix(name, "Sound Effect"):
			continue
		}
		out[name] = n
	}
	return out
}

// outages: the provider outages an agent's diagnostics recorded (kind outage), as the website shows them.
func outages(diag string) []map[string]any {
	b, _ := os.ReadFile(diag)
	var out []map[string]any
	for _, l := range strings.Split(string(b), "\n") {
		var d types.Diagnostic
		if json.Unmarshal([]byte(l), &d) != nil || d.Kind != "outage" || d.Details.Outage == nil {
			continue
		}
		o := d.Details.Outage
		out = append(out, map[string]any{"why": d.Code, "start": time.UnixMilli(o.Start).Format(time.RFC3339), "held_s": d.DurationMS / 1000,
			"safe_wait_s": o.SafeWaitMS / 1000, "probes": o.Probes, "outcome": d.Outcome})
	}
	return out
}
