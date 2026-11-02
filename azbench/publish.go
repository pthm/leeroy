package main

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pthm/leeroy/azbench/config"
	"github.com/pthm/leeroy/azbench/judge"
	"github.com/pthm/leeroy/azbench/run"
)

// publish writes a run's results for the website (site/src/data/runs/<run>.json): the scenario, then each player's
// model, outcome, time, cost and tokens, from the run's scenario.yaml, report.json and team.json. Small and committed:
// the site builds from these, never from bench/runs.
func publish(root, runDir, task, out, model, runtime string) (string, error) {
	id := filepath.Base(filepath.Clean(runDir))
	started, err := time.ParseInLocation("20060102-150405", id[:min(len(id), 15)], time.Local)
	if err != nil {
		return "", fmt.Errorf("%s: not a run directory (<time>-<scenario>)", runDir)
	}
	name := strings.TrimPrefix(id[15:], "-")
	b, err := config.LoadBench(filepath.Join(root, "bench"))
	if err != nil {
		return "", err
	}
	s, err := b.LoadScenario(filepath.Join(runDir, "scenario.yaml"))
	if err != nil { // older runs didn't keep a copy: what the run's own files say
		s = &config.Scenario{Conditions: config.Conditions{Name: name}}
	}
	var report struct {
		Agents []map[string]any `json:"agents"`
	}
	var team struct {
		Agents []map[string]any `json:"agents"`
	}
	if err := readJSON(filepath.Join(runDir, "report.json"), &report); err != nil {
		return "", err
	}
	if err := readJSON(filepath.Join(runDir, "team.json"), &team); err != nil {
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
	for _, r := range report.Agents { // agents the scenario doesn't list (no scenario kept): from the report
		if c := fmt.Sprint(r["char"]); agents[c].Name == "" {
			agents[c] = config.Agent{Name: fmt.Sprint(r["agent"]), Char: c}
		}
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
		for k, v := range map[string]string{"model": model, "runtime": runtime} { // the first runs didn't record them
			if v != "" && (r[k] == nil || r[k] == "") {
				r[k] = v
			}
		}
		p := pick(r, "char", "runtime", "model", "effort", "hours", "deaths", "cost_usd", "tokens", "calls", "time_pct_by_state", "time_use", "combat")
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
			adir := agentDir(runDir, id, a.Name)
			var meta struct{ Spec, Role string }
			if readJSON(filepath.Join(adir, "meta.json"), &meta) == nil {
				p["spec_name"], p["role"] = meta.Spec, meta.Role
				if a.Class == "" { // "Warrior - Protection"
					p["class"] = strings.ToLower(strings.TrimSpace(strings.Split(meta.Spec, " - ")[0]))
				}
			}
			p["series"], p["deaths_at"] = series(filepath.Join(adir, "samples.jsonl"))
			cs := costSeries(fmt.Sprint(p["runtime"]), fmt.Sprint(r["provider"]), fmt.Sprint(p["model"]), adir, started)
			// the transcript misses some requests (Claude Code's background calls): keep its shape, end at the run's cost
			if total, ok := p["cost_usd"].(float64); ok && len(cs) > 0 && cs[len(cs)-1][1] > 0 {
				k := total / cs[len(cs)-1][1]
				for i := range cs {
					cs[i][1] = math.Round(cs[i][1]*k*1000) / 1000
				}
			}
			p["cost_series"] = cs
			if k := kitOf(root, runDir, adir, char, started); k != nil {
				kitIcons(k, filepath.Join(out, "../../../public/icons"))
				p["kit"] = k
			}
			if b, err := os.ReadFile(filepath.Join(adir, "outages.jsonl")); err == nil { // provider outages waited out
				var outs []map[string]any
				for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
					var o map[string]any
					if json.Unmarshal([]byte(line), &o) == nil {
						outs = append(outs, o)
					}
				}
				p["outages"] = outs
			}
			if p["effort"] == nil || p["effort"] == "" || p["effort"] == "default" { // unrecorded, or read before Codex wrote it: its session says
				p["effort"] = run.DetectEffort(fmt.Sprint(p["runtime"]), filepath.Join(adir, "agent.jsonl"))
			}
			if t := trajectory(filepath.Join(runDir, char+".events.jsonl"), started.UnixMilli()); len(t) > 0 {
				p["trajectory"] = t
			}
			if rec, err := readRecording(filepath.Join(runDir, a.Name+".record.jsonl"), char, started.UnixMilli(), chat); err == nil {
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
	var ver struct {
		Version string `json:"version"`
		SDKRev  string `json:"sdk_rev"`
	}
	if readJSON(filepath.Join(runDir, "version.json"), &ver) != nil || ver.Version == "" {
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
	f := filepath.Join(out, id+".json")
	data, _ := json.Marshal(doc) // compact: routes and series are thousands of numbers
	return f, os.WriteFile(f, append(data, '\n'), 0o644)
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

// agentDir: an agent's files, inside the run (<run>/<agent>), or for the first team runs beside it
// (<time>-<agent>, started seconds after the run's own <time>-team): the first such after the run's time.
func agentDir(runDir, id, name string) string {
	in := filepath.Join(runDir, name)
	if _, err := os.Stat(in); err == nil {
		return in
	}
	ms, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Clean(runDir)), id[:9]+"*-"+name))
	sort.Strings(ms)
	for _, m := range ms {
		if filepath.Base(m) >= id[:15] {
			return m
		}
	}
	return in
}

func readJSON(f string, v any) error {
	data, err := os.ReadFile(f)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// kitOf: the character's gear, talents and abilities as its run left them: kit.json, which the harness saves at the
// end of each run; for older runs, the characters database, but only if no later run played a character of that name
// (else its kit would be that run's).
func kitOf(root, runDir, adir, char string, started time.Time) *run.Kit {
	var k run.Kit
	if readJSON(filepath.Join(adir, "kit.json"), &k) == nil {
		return &k
	}
	later, _ := filepath.Glob(filepath.Join(root, "bench/runs/*"))
	for _, d := range later {
		t, err := time.ParseInLocation("20060102-150405", filepath.Base(d)[:min(15, len(filepath.Base(d)))], time.Local)
		if err != nil || !t.After(started) {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(d, "report.json")); err == nil &&
			(strings.Contains(string(b), `"char":"`+char+`"`) || strings.Contains(string(b), `"char": "`+char+`"`)) {
			return nil
		}
		if b, err := os.ReadFile(filepath.Join(d, "scenario.yaml")); err == nil && strings.Contains(string(b), "char: "+char+"\n") {
			return nil
		}
	}
	kk, err := run.CharKit(char)
	if err != nil || len(kk.Gear) == 0 {
		return nil
	}
	return kk
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
