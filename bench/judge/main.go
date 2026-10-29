// judge <character> <goal> [--timeout 4h] [--every 15s] [--samples file.jsonl] ...
// judge signals <character>: print the character's progress signals (signals.go).
//
// Goals: level:<n> | item:<entry> | quality:<0-5> (any owned item of that quality or better; 3 = rare)
//
//	| money:<copper> | quests:<n> (quests turned in) | quest:<id> (that quest turned in)
//	| skill:<id>:<n> (that skill line at n or more, e.g. skill:164:75 = Blacksmithing 75)
//	| achpoints:<n> (achievement points earned since the judge started; needs bin/azworld setup.
//	  Use a large n to score "as many as possible" by the value at timeout)
//	| score:<n> (composite: weighted change in every signal since the judge started; weights in score.json,
//	  signals in signals.go. Same "large n" convention)
//
// Reads the character DB (saved every 15s, see server/override.yml), so it can't be gamed from the client.
// Prints one JSON line when the goal is met or the timeout hits; exit 0 = met, 1 = timed out.
// --samples appends {elapsed_s, level, xp, money, value, rate, ..., signals} each poll, for partial credit and curves.
// --record also appends each sample as a {"ev":"score"} event to a recorder log, so the replay/live viewers chart it.
//
// Rate of progress (all goals, from the value curve):
//
//	rate         value per hour since the start (the first 10 minutes count as 10)
//	recent_rate  value per hour over the last 10 minutes
//	pace         the time-average of value so far (area under the curve / time)
//	best_window  the best rate over any 15-minute window
//	score        (rate + best_window) / 2: the headline for score goals. Sustained progress counts, and so does
//	             finding a better strategy: time spent trying one lowers the rate but not the best window. Deaths
//	             and stalls cost through the time they lose. For score goals, progress is the game's own rewards:
//	             XP earned (score.json); gear, money and the rest are reported, not scored.
//
// --board <dir> writes <dir>/<character>.json with the latest result each poll (the agents' `standings` tool reads
// them), including minutes_left on the run's clock.
// --resume <samples.jsonl> continues a paused run from its samples.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(2)
}

const usage = "usage: judge <character> <level|item|quality|money|quests|quest|achpoints|score>:<n> | skill:<id>:<n> [--timeout 4h] [--every 15s] [--samples f] [--resume f] [--record f] [--board dir] [--events f] [--role r] [--runtime r]\n       judge signals <character>"

var goalRe = regexp.MustCompile(`^((level|item|quality|money|quests|quest|achpoints|score):\d+|skill:\d+:\d+)$`)

func dur(s string) time.Duration {
	m := regexp.MustCompile(`^(\d+)(s|m|h)$`).FindStringSubmatch(s)
	if m == nil {
		fail("bad duration %q", s)
	}
	n, _ := strconv.Atoi(m[1])
	return time.Duration(n) * map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour}[m[2]]
}

func appendLine(file string, v any) {
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fail("%v", err)
	}
	defer f.Close()
	b, _ := json.Marshal(v)
	f.Write(append(b, '\n'))
}

func main() {
	args := os.Args[1:]
	if len(args) == 2 && args[0] == "signals" {
		b, _ := json.MarshalIndent(signals(args[1]), "", " ")
		fmt.Println(string(b))
		return
	}
	if len(args) < 2 || !nameRe.MatchString(args[0]) || !goalRe.MatchString(args[1]) {
		fail(usage)
	}
	char, goal, rest := args[0], args[1], args[2:]
	flag := func(name, def string) string {
		for i, a := range rest {
			if a == "--"+name && i+1 < len(rest) {
				return rest[i+1]
			}
		}
		return def
	}
	timeout, every := dur(flag("timeout", "4h")), dur(flag("every", "15s"))
	samples, record, board := flag("samples", ""), flag("record", ""), flag("board", "")
	// --events <file> --role <DPS|Tank|Healer>: the character's event stream (client-side, azsdk -events); its damage
	// dealt / taken and healing done since the start go on the board. Reported, not scored.
	eventsFile, role, runtime := flag("events", ""), flag("role", ""), flag("runtime", "")
	var dealt, taken, healed float64
	var eventsAt int64
	var eventsTail string
	tallyEvents := func(since int64) {
		if eventsFile == "" {
			return
		}
		f, err := os.Open(eventsFile)
		if err != nil {
			return // not there yet
		}
		defer f.Close()
		st, _ := f.Stat()
		if st.Size() < eventsAt {
			eventsAt = 0
		}
		f.Seek(eventsAt, io.SeekStart)
		buf, _ := io.ReadAll(io.LimitReader(f, st.Size()-eventsAt))
		eventsAt += int64(len(buf))
		lines := strings.Split(eventsTail+string(buf), "\n")
		eventsTail = lines[len(lines)-1]
		for _, l := range lines[:len(lines)-1] {
			if !strings.Contains(l, `"type":"damage"`) && !strings.Contains(l, `"type":"heal"`) {
				continue
			}
			var e struct {
				T    int64
				Type string
				Data struct {
					Src    struct{ Me bool }
					Taken  bool
					Amount float64
				}
			}
			if json.Unmarshal([]byte(l), &e) != nil || e.T < since {
				continue
			}
			d := e.Data
			if e.Type == "damage" && d.Src.Me {
				dealt += d.Amount
			}
			if e.Type == "damage" && d.Taken {
				taken += d.Amount
			}
			if e.Type == "heal" && d.Src.Me {
				healed += d.Amount
			}
		}
	}
	if board != "" {
		os.MkdirAll(board, 0o755)
	}

	parts := strings.Split(goal, ":") // skill:<id>:<n>: n is the skill id, the third part the level
	kind, n := parts[0], parts[1]
	owned := `FROM character_inventory ci JOIN item_instance ii ON ii.guid = ci.item
  JOIN acore_world.item_template it ON it.entry = ii.itemEntry
  WHERE ci.guid = (SELECT guid FROM characters WHERE name = '` + char + `')`
	g := "(SELECT guid FROM characters WHERE name = '" + char + "')"
	query := map[string]string{
		"level":     "SELECT level FROM characters WHERE name = '" + char + "'",
		"money":     "SELECT money FROM characters WHERE name = '" + char + "'",
		"item":      "SELECT COUNT(*) " + owned + " AND it.entry = " + n,
		"quality":   "SELECT COUNT(*) " + owned + " AND it.Quality >= " + n,
		"quests":    "SELECT COUNT(*) FROM character_queststatus_rewarded WHERE guid = " + g,
		"quest":     "SELECT COUNT(*) FROM character_queststatus_rewarded WHERE quest = " + n + " AND guid = " + g,
		"skill":     "SELECT COALESCE(MAX(value), 0) FROM character_skills WHERE skill = " + n + " AND guid = " + g,
		"achpoints": "SELECT COALESCE(SUM(a.points), 0) FROM character_achievement ca JOIN wowbench.achievement a ON a.id = ca.achievement\n    WHERE ca.guid = " + g,
	}[kind]

	// --resume <samples.jsonl>: continue a paused run from its samples: same baseline, same curve, and the clock
	// picks up at the last sample (the pause doesn't count)
	type sample struct {
		ElapsedS float64            `json:"elapsed_s"`
		Value    float64            `json:"value"`
		Signals  map[string]float64 `json:"signals"`
	}
	var prior []sample
	if f := flag("resume", ""); f != "" {
		fh, err := os.Open(f)
		if err != nil {
			fail("%v", err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "" {
				continue
			}
			var s sample
			if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
				fail("%s: %v", f, err)
			}
			prior = append(prior, s)
		}
		fh.Close()
	}
	baseline := 0.0 // achpoints: only count what's earned during the run
	if kind == "achpoints" {
		baseline = num(sql(query))
		if len(prior) > 0 {
			baseline -= prior[len(prior)-1].Value
		}
	}
	var base map[string]float64
	if len(prior) > 0 {
		base = prior[0].Signals
	} else {
		base = signals(char).m()
	}
	var w map[string]float64
	if kind == "score" {
		w = weights()
	}
	need, _ := strconv.ParseFloat(n, 64)
	switch kind {
	case "item", "quality", "quest":
		need = 1
	case "skill":
		need, _ = strconv.ParseFloat(parts[2], 64)
	}

	nowMs := func() int64 { return time.Now().UnixMilli() }
	start := nowMs()
	if len(prior) > 0 {
		start -= int64(prior[len(prior)-1].ElapsedS * 1000)
	}
	type pt struct{ h, v float64 }
	var curve []pt // [elapsed h, value]
	for _, p := range prior {
		curve = append(curve, pt{p.ElapsedS / 3600, p.Value})
	}
	area := 0.0 // integral of value dt, in value-hours
	for i := 1; i < len(curve); i++ {
		area += (curve[i-1].v + curve[i].v) / 2 * (curve[i].h - curve[i-1].h)
	}
	// best_window: the best rate over any window of the run (value gained in the window / its length). Trying a new
	// strategy costs some average rate but not the best window, so the score rewards finding something better as
	// well as sustaining it.
	const window = 0.25 // hours
	windowRate := func(k int) float64 {
		hk, vk := curve[k].h, curve[k].v
		if hk < window {
			return vk / math.Max(hk, 1.0/6)
		}
		j := k
		for j > 0 && curve[j-1].h >= hk-window {
			j--
		}
		return (vk - curve[j].v) / math.Max(hk-curve[j].h, 1.0/60)
	}
	bestWindow := 0.0
	for k := range curve {
		bestWindow = math.Max(bestWindow, windowRate(k))
	}
	for {
		sig := signals(char)
		var value float64
		if kind == "score" {
			value = composite(sig, base, w)
		} else {
			value = num(sql(query)) - baseline
		}
		lxm := strings.Split(sql("SELECT level, xp, money FROM characters WHERE name = '"+char+"'"), "\t")
		for len(lxm) < 3 {
			lxm = append(lxm, "")
		}
		level, xp, money := num(lxm[0]), num(lxm[1]), num(lxm[2])
		elapsedS := math.Floor(float64(nowMs()-start)/1000 + 0.5)
		h := elapsedS / 3600
		if len(curve) > 0 {
			prev := curve[len(curve)-1]
			area += (prev.v + value) / 2 * (h - prev.h)
		}
		curve = append(curve, pt{h, value})
		ago := curve[0] // ~10 minutes back
		for _, c := range curve {
			if c.h >= h-1.0/6 {
				ago = c
				break
			}
		}
		pace := value
		if h > 0 {
			pace = area / h
		}
		// rate: progress points per hour over the run so far (the first 10 minutes count as 10, so a quick early
		// quest doesn't read as a huge rate). score = (rate + best 15-minute window rate) / 2
		rate := value / math.Max(h, 1.0/6)
		bestWindow = math.Max(bestWindow, windowRate(len(curve)-1))
		recent := 0.0
		if h-ago.h > 0 {
			recent = (value - ago.v) / (h - ago.h)
		}
		rates := obj{{"rate", round1(rate)}, {"recent_rate", round1(recent)}, {"best_window", round1(bestWindow)},
			{"pace", round1(pace)}, {"score", round1((rate + bestWindow) / 2)}}
		smp := append(append(obj{{"elapsed_s", elapsedS}, {"level", level}, {"xp", xp}, {"money", money}, {"value", value}}, rates...),
			kv{"signals", sig})
		if samples != "" {
			appendLine(samples, smp)
		}
		tallyEvents(start)
		if board != "" {
			left := math.Max(0, math.Floor(float64(timeout.Milliseconds()-(nowMs()-start))/60000+0.5))
			b := append(append(obj{{"char", char}, {"level", round1(sig.get("level"))}, {"progress", value}}, rates...),
				kv{"elapsed_s", elapsedS}, kv{"minutes_left", left}, kv{"money", sig.get("money_copper")},
				kv{"avg_item_level", sig.get("avg_item_level")})
			if role != "" {
				b = append(b, kv{"role", role})
			}
			if runtime != "" {
				b = append(b, kv{"runtime", runtime})
			}
			if eventsFile != "" {
				b = append(b, kv{"damage_dealt", dealt}, kv{"damage_taken", taken}, kv{"healing_done", healed})
			}
			j, _ := json.Marshal(b)
			os.WriteFile(board+"/"+char+".json", j, 0o644)
		}
		if record != "" {
			appendLine(record, append(obj{{"t", nowMs()}, {"ev", "score"}, {"n", char}, {"value", value}, {"level", level}}, rates...))
		}
		if value >= need || nowMs()-start > timeout.Milliseconds() {
			res := obj{{"char", char}, {"goal", goal}, {"met", value >= need}}
			if runtime != "" {
				res = append(res, kv{"runtime", runtime})
			}
			res = append(append(res, smp...), kv{"at", time.Now().UTC().Format("2006-01-02T15:04:05.000Z")})
			j, _ := json.Marshal(res)
			fmt.Println(string(j))
			if value >= need {
				os.Exit(0)
			}
			os.Exit(1)
		}
		time.Sleep(every)
	}
}
