package main

// judge report <run dir | team dir>: the report card for each agent in a run, next to its score, and (for a team)
// team.json. How the agent spent the run, not what it scored: how much of the time a script was playing (duty
// cycle), progress by what the agent was doing (waiting on wow wait / thinking / in a call), what woke it and
// whether a script restart followed, restarts and their warm-up cost, deaths, stalls and tokens; plus its combat
// totals, cooperation (assist share, contested hits) and time use, from the shared metrics package. Writes
// report.json in the dir and prints a table. Reported, not scored. Progress is the judge's value (XP for score
// goals), per hour of sampled time.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/metrics"
)

var (
	restartRe = regexp.MustCompile(`task\.(spawn|keep|stop|kill)|wow -f|\.stop\(|dofile`)
	waitRe    = regexp.MustCompile(`wow wait|(^|[;&| ])sleep \d|wow -w`)
	waitArgs  = regexp.MustCompile(`wow wait([^;|&]*)`)
	stampRe   = regexp.MustCompile(`^(\d{4})(\d\d)(\d\d)-(\d\d)(\d\d)(\d\d)`)
)

// what woke a wait with no --types: the default subscription, plus the old default (level_up, error, any chat)
var wakers = []string{"attention", "death", "stall", "script_error", "task_finished", "party_invite", "trade",
	"gear_broken", "chat", "level_up", "error", "resurrected"}

func jsonl(f string) []map[string]any {
	fh, err := os.Open(f)
	if err != nil {
		return nil
	}
	defer fh.Close()
	var out []map[string]any
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func readJSON(f string) map[string]any {
	b, err := os.ReadFile(f)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return map[string]any{}
	}
	return m
}

func str(v any) string   { s, _ := v.(string); return s }
func fnum(v any) float64 { f, _ := v.(float64); return f }
func get(m any, k string) any {
	if mm, ok := m.(map[string]any); ok {
		return mm[k]
	}
	return nil
}
func isoMs(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}
func stampMs(name string) int64 {
	m := stampRe.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	t, _ := time.ParseInLocation("20060102-150405", strings.Join(m[1:4], "")+"-"+strings.Join(m[4:7], ""), time.Local)
	return t.UnixMilli()
}
func median(a []float64) any {
	if len(a) == 0 {
		return nil
	}
	s := slices.Clone(a)
	slices.Sort(s)
	return math.Round(s[len(s)/2])
}
func p90(a []float64) any {
	if len(a) == 0 {
		return nil
	}
	s := slices.Clone(a)
	slices.Sort(s)
	return math.Round(s[int(float64(len(s))*0.9)])
}
func r1(v float64) any {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return math.Round(v*10) / 10
}

type agentRun struct{ agent, run, record, evdir string }

// the agents in a dir: a team (it has go) or one run
func reportAgents(dir string) []agentRun {
	if _, err := os.Stat(filepath.Join(dir, "go")); err == nil {
		runs, t0 := filepath.Join(dir, ".."), stampMs(filepath.Base(dir))
		var out []agentRun
		recs, _ := filepath.Glob(filepath.Join(dir, "*.record.jsonl"))
		sort.Strings(recs)
		for _, f := range recs {
			a := strings.Split(filepath.Base(f), ".")[0]
			b, _ := os.ReadFile(filepath.Join(dir, "go."+a+".ready")) // bench.sh writes its run dir there
			run := strings.TrimSpace(string(b))
			if run == "" { // older runs: by time
				ds, _ := os.ReadDir(runs)
				for _, d := range ds {
					if strings.HasSuffix(d.Name(), "-"+a) && math.Abs(float64(stampMs(d.Name())-t0)) < 600e3 {
						run = filepath.Join(runs, d.Name())
						break
					}
				}
			}
			out = append(out, agentRun{a, run, f, dir})
		}
		return out
	}
	meta := readJSON(filepath.Join(dir, "meta.json"))
	a := str(meta["agent"])
	if a == "" {
		a = filepath.Base(dir)
	}
	return []agentRun{{a, dir, filepath.Join(dir, "record.jsonl"), dir}}
}

type call struct {
	s, e int64
	cmd  string
}

func card(ar agentRun, teamEvents map[string][]metrics.Event, coop map[string]metrics.Coop, pos map[string][]metrics.Pos) obj {
	meta := readJSON(filepath.Join(ar.run, "meta.json"))
	var score []map[string]any
	for _, e := range jsonl(ar.record) {
		if e["ev"] == "score" {
			score = append(score, e)
		}
	}
	char := str(meta["char"])
	if char == "" && len(score) > 0 {
		char = str(score[0]["n"])
	}
	events := jsonl(filepath.Join(ar.evdir, char+".events.jsonl"))

	// the agent's tool calls (Claude: tool_use -> tool_result; Codex: item.started -> completed)
	var calls []call
	type pend struct {
		t   int64
		cmd string
	}
	pending := map[string]pend{}
	type tokens struct{ in, out, cr, cw float64 }
	usage := map[string]struct {
		u       tokens
		session string
	}{}
	results := map[string]tokens{} // Claude's result line per session: its real totals (none if it was killed)
	cost, costSeen := 0.0, false
	for _, e := range jsonl(filepath.Join(ar.run, "agent.jsonl")) {
		t := isoMs(str(e["timestamp"]))
		typ := str(e["type"])
		msg, _ := e["message"].(map[string]any)
		item, _ := e["item"].(map[string]any)
		switch {
		case typ == "assistant" && t != 0:
			cs, _ := msg["content"].([]any)
			for _, c := range cs {
				if get(c, "type") == "tool_use" {
					cmd := str(get(get(c, "input"), "command"))
					if cmd == "" {
						cmd = str(get(c, "name"))
					}
					pending[str(get(c, "id"))] = pend{t, cmd}
				}
			}
			if u, ok := msg["usage"].(map[string]any); ok {
				usage[str(msg["id"])] = struct {
					u       tokens
					session string
				}{tokens{fnum(u["input_tokens"]), fnum(u["output_tokens"]), fnum(u["cache_read_input_tokens"]),
					fnum(u["cache_creation_input_tokens"])}, str(e["session_id"])}
			}
		case typ == "user" && t != 0:
			cs, _ := msg["content"].([]any)
			for _, c := range cs {
				id := str(get(c, "tool_use_id"))
				if p, ok := pending[id]; ok && get(c, "type") == "tool_result" {
					calls = append(calls, call{p.t, t, p.cmd})
					delete(pending, id)
				}
			}
		case typ == "item.started" && get(item, "type") == "command_execution":
			pending[str(item["id"])] = pend{t, str(item["command"])}
		case typ == "item.completed":
			if p, ok := pending[str(item["id"])]; ok {
				calls = append(calls, call{p.t, t, p.cmd})
				delete(pending, str(item["id"]))
			}
		case typ == "result":
			if c, ok := e["total_cost_usd"].(float64); ok {
				cost += c
				costSeen = true
				var tk tokens // per model; usage is partial
				if mu, ok := e["modelUsage"].(map[string]any); ok {
					for _, m := range mu {
						tk.in += fnum(get(m, "inputTokens"))
						tk.out += fnum(get(m, "outputTokens"))
						tk.cr += fnum(get(m, "cacheReadInputTokens"))
						tk.cw += fnum(get(m, "cacheCreationInputTokens"))
					}
				}
				results[str(e["session_id"])] = tk
			}
		}
	}
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].s < calls[j].s })

	// tasks running over time, from task_started/task_finished
	running := map[any]bool{}
	type change struct {
		t int64
		n int
	}
	var changes []change
	var starts []int64
	for _, e := range events {
		t := int64(fnum(e["t"]))
		switch e["type"] {
		case "task_started":
			running[get(e["data"], "task")] = true
			starts = append(starts, t)
		case "task_finished":
			delete(running, get(e["data"], "task"))
		default:
			continue
		}
		changes = append(changes, change{t, len(running)})
	}
	at := func(m float64) int {
		i := sort.Search(len(changes), func(i int) bool { return float64(changes[i].t) > m })
		if i == 0 {
			return 0
		}
		return changes[i-1].n
	}
	state := func(m float64) string {
		var c *call
		for i := range calls {
			if float64(calls[i].s) > m {
				break
			}
			c = &calls[i]
		}
		switch {
		case c == nil:
			return "before"
		case m > float64(c.e):
			return "thinking"
		case waitRe.MatchString(c.cmd) && !restartRe.MatchString(c.cmd):
			return "waiting"
		}
		return "in_call"
	}
	// 15 s samples -> progress per hour by state; gaps over a minute (a pause, a resume) are skipped
	by, sinceStart := map[string]*[2]float64{}, map[string]*[2]float64{}
	add := func(m map[string]*[2]float64, k string, dv, dh float64) {
		if m[k] == nil {
			m[k] = &[2]float64{}
		}
		m[k][0] += dv
		m[k][1] += dh
	}
	var gaps []float64
	gapStart := int64(-1)
	for i := 1; i < len(score); i++ {
		a, b := score[i-1], score[i]
		at0, bt := int64(fnum(a["t"])), int64(fnum(b["t"]))
		if bt-at0 > 60e3 {
			gapStart = -1
			continue
		}
		m, dv, dh := float64(at0+bt)/2, fnum(b["value"])-fnum(a["value"]), float64(bt-at0)/3.6e6
		n := at(m)
		add(by, "all", dv, dh)
		if n > 0 {
			add(by, "script", dv, dh)
			add(by, state(m), dv, dh)
		} else {
			add(by, "no_script", dv, dh)
		}
		s := int64(-1)
		for _, t := range starts {
			if float64(t) <= m {
				s = t
			}
		}
		if s >= 0 && n > 0 {
			k := "long_running"
			if m-float64(s) < 60e3 {
				k = "first_minute"
			} else if m-float64(s) < 600e3 {
				k = "settled"
			}
			add(sinceStart, k, dv, dh)
		}
		if n == 0 && gapStart < 0 {
			gapStart = at0
		}
		if n > 0 && gapStart >= 0 {
			gaps = append(gaps, float64(at0-gapStart)/1000)
			gapStart = -1
		}
	}
	if gapStart >= 0 && len(score) > 0 { // still idle at the end
		gaps = append(gaps, float64(int64(fnum(score[len(score)-1]["t"]))-gapStart)/1000)
	}
	H := 0.0
	if by["all"] != nil {
		H = by["all"][1]
	}
	rate := func(k *[2]float64) any {
		if k == nil || k[1] <= 0 {
			return nil
		}
		return math.Round(k[0] / k[1])
	}
	share := func(k string) any {
		if H == 0 {
			return nil
		}
		v := 0.0
		if by[k] != nil {
			v = by[k][1]
		}
		return r1(100 * v / H)
	}
	perH := func(n float64) any {
		if H == 0 {
			return nil
		}
		return r1(n / H)
	}

	// waits: early wakes, what woke them, and whether a restart followed within 3 calls
	type wake struct{ n, restart int }
	wakes := map[string]*wake{}
	waits, early := 0, 0
	for k, c := range calls {
		m := waitArgs.FindStringSubmatch(c.cmd)
		if m == nil {
			continue
		}
		waits++
		// its arguments, also inside a quoted wrapper (Codex: zsh -lc 'wow wait 30 --types death'): the bare number
		// is the timeout (the server's default is 60 s), --types the list
		arg := strings.NewReplacer(`'`, " ", `"`, " ").Replace(strings.Split(m[1], ">")[0])
		tok := strings.Fields(arg)
		limit, kinds := 60.0, wakers
		for i := 0; i < len(tok); i++ {
			switch {
			case tok[i] == "--types" && i+1 < len(tok):
				i++
				kinds = strings.Split(tok[i], ",")
			case strings.HasPrefix(tok[i], "--"):
				i++
			case regexp.MustCompile(`^\d+$`).MatchString(tok[i]):
				fmt.Sscan(tok[i], &limit)
			}
		}
		if float64(c.e-c.s)/1000 > limit-3 {
			continue
		}
		early++
		glob := func(pat, v string) bool {
			return pat == v || pat == "*" || (strings.HasSuffix(pat, "*") && strings.HasPrefix(v, pat[:len(pat)-1]))
		}
		why := "?"
		for _, ev := range events {
			t := int64(fnum(ev["t"]))
			if t < c.s || t > c.e+1500 {
				continue
			}
			typ, kind := str(ev["type"]), str(get(ev["data"], "kind"))
			if slices.ContainsFunc(kinds, func(p string) bool {
				t, k, hasK := strings.Cut(p, ":")
				return glob(t, typ) && (!hasK || glob(k, kind))
			}) {
				why = typ
				if typ == "chat" {
					why = "chat:" + kind
				}
				break
			}
		}
		if wakes[why] == nil {
			wakes[why] = &wake{}
		}
		wakes[why].n++
		for _, next := range calls[k+1 : min(len(calls), k+4)] {
			if restartRe.MatchString(next.cmd) {
				wakes[why].restart++
				break
			}
		}
	}
	trig := make([]string, 0, len(wakes))
	for k := range wakes {
		trig = append(trig, k)
	}
	sort.SliceStable(trig, func(i, j int) bool { return wakes[trig[i]].n > wakes[trig[j]].n })
	byTrigger := obj{}
	for _, k := range trig {
		byTrigger = append(byTrigger, kv{k, obj{{"n", wakes[k].n}, {"restart", wakes[k].restart}}})
	}

	// tokens per session (a resumed run has several): its result line's totals, else the sum over its messages,
	// whose usage is recorded as each message starts: right for input and cache, not for output (null then)
	var tok tokens
	parts, outputKnown := 0, true
	for _, t := range results {
		tok.in, tok.out, tok.cr, tok.cw = tok.in+t.in, tok.out+t.out, tok.cr+t.cr, tok.cw+t.cw
		parts++
	}
	for _, u := range usage {
		if _, ok := results[u.session]; !ok {
			tok.in, tok.cr, tok.cw = tok.in+u.u.in, tok.cr+u.u.cr, tok.cw+u.u.cw
			outputKnown = false
			parts++
		}
	}
	var tokOut any
	if parts > 0 {
		var out any = tok.out
		if !outputKnown {
			out = nil
		}
		tokOut = obj{{"input", tok.in}, {"output", out}, {"cache_read", tok.cr}, {"cache_write", tok.cw}}
	}
	deaths, stalls, restarts := 0, 0, 0
	for _, e := range events {
		switch e["type"] {
		case "death":
			deaths++
		case "stall":
			stalls++
		}
	}
	for _, c := range calls {
		if restartRe.MatchString(c.cmd) {
			restarts++
		}
	}
	var thinks []float64
	for i := 1; i < len(calls); i++ {
		thinks = append(thinks, float64(calls[i].s-calls[i-1].e)/1000)
	}
	var costOut any
	if costSeen {
		costOut = r1(cost)
	}
	progress := 0.0
	if by["all"] != nil {
		progress = by["all"][0]
	}

	// the shared measures: the judge's last result, combat totals, cooperation and time use
	var final any
	if len(score) > 0 {
		l := score[len(score)-1]
		final = obj{{"value", l["value"]}, {"rate", l["rate"]}, {"recent_rate", l["recent_rate"]},
			{"best_window", l["best_window"]}, {"pace", l["pace"]}, {"score", l["score"]}}
	}
	var combat metrics.Combat
	for _, e := range teamEvents[char] {
		combat.Add(e)
	}
	co, use := coop[char], metrics.Use(char, pos)

	return obj{
		{"agent", ar.agent}, {"char", char}, {"runtime", meta["runtime"]}, {"model", meta["model"]},
		{"hours", r1(H)}, {"progress", progress}, {"progress_per_hour", rate(by["all"])},
		{"score", final},
		{"duty_cycle_pct", share("script")}, // time with a script task running
		{"idle_gaps", obj{{"n", len(gaps)}, {"median_s", median(gaps)}, {"p90_s", p90(gaps)},
			{"total_min", r1(sum(gaps) / 60)}}},
		// while a script runs: the agent blocked in wow wait/sleep, between calls, or in a call
		{"progress_per_hour_by_state", obj{{"waiting", rate(by["waiting"])}, {"thinking", rate(by["thinking"])},
			{"in_call", rate(by["in_call"])}, {"no_script", rate(by["no_script"])}}},
		{"time_pct_by_state", obj{{"waiting", share("waiting")}, {"thinking", share("thinking")},
			{"in_call", share("in_call")}, {"no_script", share("no_script")}}},
		{"wakes", obj{{"waits", waits}, {"early", early}, {"per_hour", perH(float64(early))}, {"by_trigger", byTrigger}}},
		{"restarts", obj{{"agent_calls", restarts}, {"per_hour", perH(float64(restarts))},
			{"script_starts_per_hour", perH(float64(len(starts)))},
			{"warmup_progress_per_hour", obj{{"first_minute", rate(sinceStart["first_minute"])},
				{"settled", rate(sinceStart["settled"])}}}}},
		{"deaths", deaths}, {"deaths_per_hour", perH(float64(deaths))}, {"stalls", stalls},
		{"calls", len(calls)}, {"think_s", obj{{"median", median(thinks)}}},
		{"tokens", tokOut}, {"cost_usd", costOut},
		{"combat", obj{{"damage_dealt", combat.DamageDealt}, {"damage_taken", combat.DamageTaken},
			{"healing_done", combat.HealingDone}, {"kills", combat.Kills}, {"party_kills", combat.PartyKills},
			{"deaths", combat.Deaths}, {"xp", combat.XP}}},
		{"coop", obj{{"assist_share", round3(co.AssistShare)}, {"assists", co.Assists},
			{"useful", co.Useful}, {"hits", co.Hits}, {"contested_hits", co.Contested}}},
		{"time_use", obj{{"seconds", use.Seconds}, {"fighting_pct", use.FightingPct}, {"travelling_pct", use.TravellingPct},
			{"idle_pct", use.IdlePct}, {"dead_pct", use.DeadPct}, {"with_group_pct", use.WithGroupPct}}},
	}
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func sum(a []float64) (s float64) {
	for _, v := range a {
		s += v
	}
	return s
}

// positions of every character in a dir's record files (client side: each agent's own samples)
func readPositions(dir string) map[string][]metrics.Pos {
	out := map[string][]metrics.Pos{}
	files, _ := filepath.Glob(filepath.Join(dir, "*record.jsonl"))
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			l := sc.Bytes()
			if !strings.Contains(string(l), `"ev":"pos"`) {
				continue
			}
			var p struct {
				T             int64
				N             string
				X, Y, M, Grp  float64
				Alive, Combat any
			}
			if json.Unmarshal(l, &p) == nil {
				out[p.N] = append(out[p.N], metrics.Pos{T: p.T, X: p.X, Y: p.Y, Map: p.M, Alive: truthy(p.Alive),
					Combat: truthy(p.Combat), Group: p.Grp})
			}
		}
		fh.Close()
	}
	return out
}

// truthy is JS truthiness: the recorder's flags are 0/1 or booleans, and a victim is an object.
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return v != nil
}

// teamResults: team.json from each agent's judge result (the last JSON line of <agent>.out).
func teamResults(dir string, n int) obj {
	var res []map[string]any
	outs, _ := filepath.Glob(filepath.Join(dir, "*.out"))
	sort.Strings(outs)
	for _, f := range outs {
		b, _ := os.ReadFile(f)
		var last map[string]any
		for _, l := range strings.Split(string(b), "\n") {
			var m map[string]any
			if strings.HasPrefix(l, "{") && json.Unmarshal([]byte(l), &m) == nil {
				last = m
			}
		}
		if last != nil {
			res = append(res, last)
		}
	}
	sc := func(m map[string]any) float64 {
		if v, ok := m["score"].(float64); ok {
			return v
		}
		return 0
	}
	sort.SliceStable(res, func(i, j int) bool {
		a, b := res[i]["score"], res[j]["score"]
		if a == nil {
			a = res[i]["value"]
		}
		if b == nil {
			b = res[j]["value"]
		}
		return fnum(a) > fnum(b)
	})
	faction := 0.0
	byRT := map[string][]map[string]any{}
	var rts []string
	allMet, maxS := len(res) == n, 0.0
	for _, r := range res {
		faction += sc(r)
		rt := str(r["runtime"])
		if rt == "" {
			rt = "claude"
		}
		if byRT[rt] == nil {
			rts = append(rts, rt)
		}
		byRT[rt] = append(byRT[rt], r)
		if r["met"] != true {
			allMet = false
		}
		maxS = math.Max(maxS, fnum(r["elapsed_s"]))
	}
	sort.Strings(rts)
	var groups []obj
	for _, rt := range rts {
		s, best := 0.0, 0.0
		for _, r := range byRT[rt] {
			s += sc(r)
			best = math.Max(best, sc(r))
		}
		groups = append(groups, obj{{"runtime", rt}, {"players", len(byRT[rt])}, {"score", round1(s)}, {"best", best}})
	}
	var teamS any
	if allMet {
		teamS = maxS
	}
	agents := make([]any, len(res))
	for i, r := range res {
		agents[i] = r
	}
	return obj{{"agents", agents}, {"faction_score", round1(faction)}, {"by_runtime", groups}, {"team_s", teamS}}
}

func runReport(dir string) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		fail("usage: judge report <run dir | team dir>")
	}
	evs := newStreams(dir)
	evs.read()
	teamEvents := evs.since(0)
	coop := metrics.Team(teamEvents)
	pos := readPositions(dir)
	var cards []any
	var rows []obj
	for _, ar := range reportAgents(dir) {
		if ar.run == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(ar.run, "agent.jsonl")); err != nil {
			continue
		}
		c := card(ar, teamEvents, coop, pos)
		cards = append(cards, c)
		rows = append(rows, c)
	}
	report := obj{{"agents", cards}}
	if _, err := os.Stat(filepath.Join(dir, "go")); err == nil {
		recs, _ := filepath.Glob(filepath.Join(dir, "*.record.jsonl"))
		team := teamResults(dir, len(recs))
		report = append(report, kv{"team", team})
		j, _ := json.MarshalIndent(team, "", "  ")
		os.WriteFile(filepath.Join(dir, "team.json"), append(j, '\n'), 0o644)
		fmt.Println(string(j))
	}
	j, _ := json.MarshalIndent(report, "", "  ")
	os.WriteFile(filepath.Join(dir, "report.json"), append(j, '\n'), 0o644)

	pad := func(v any, n int) string {
		s := "-"
		if v != nil {
			s = fmt.Sprint(v)
		}
		return fmt.Sprintf("%*s", n, s)
	}
	field := func(o obj, path ...string) any {
		var v any = o
		for _, k := range path {
			oo, ok := v.(obj)
			if !ok {
				return nil
			}
			v = nil
			for _, e := range oo {
				if e.k == k {
					v = e.v
				}
			}
		}
		return v
	}
	fmt.Println("agent  char         runtime  h    prog/h  duty%  idle(n,med)  wait/think/call/none prog/h        wakes/h  restarts/h  deaths/h  assist  contested")
	for _, c := range rows {
		fmt.Printf("%-6s %-12s %-7s %s %s %s  %s,%ss  %s/%s/%s/%s  %s  %s  %s  %s  %s\n",
			field(c, "agent"), fmt.Sprint(field(c, "char")), fmt.Sprint(field(c, "runtime")), pad(field(c, "hours"), 4),
			pad(field(c, "progress_per_hour"), 7), pad(field(c, "duty_cycle_pct"), 6),
			pad(field(c, "idle_gaps", "n"), 4), pad(field(c, "idle_gaps", "median_s"), 4),
			pad(field(c, "progress_per_hour_by_state", "waiting"), 6), pad(field(c, "progress_per_hour_by_state", "thinking"), 6),
			pad(field(c, "progress_per_hour_by_state", "in_call"), 6), pad(field(c, "progress_per_hour_by_state", "no_script"), 6),
			pad(field(c, "wakes", "per_hour"), 7), pad(field(c, "restarts", "per_hour"), 10), pad(field(c, "deaths_per_hour"), 8),
			pad(field(c, "coop", "assist_share"), 7), pad(field(c, "coop", "contested_hits"), 9))
	}
	fmt.Println("wrote " + filepath.Join(dir, "report.json"))
}
