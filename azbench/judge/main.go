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
package judge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/pthm/leeroy/azeroth-sdk/metrics"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// failure is a judge error: a bad argument, or the database. Main prints it and exits 2; Run skips a failed poll.
type failure string

func fail(f string, a ...any) { panic(failure(fmt.Sprintf(f, a...))) }

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

// Main is the judge's command line (azbench judge|report|signals): it returns the exit code.
func Main(args []string) (code int) {
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(failure)
			if !ok {
				panic(r)
			}
			fmt.Fprintln(os.Stderr, string(f))
			code = 2
		}
	}()
	if len(args) == 2 && args[0] == "report" {
		Report(args[1])
		return 0
	}
	if len(args) >= 2 && args[0] == "compare" {
		Compare(args[1:])
		return 0
	}
	if len(args) == 2 && args[0] == "signals" {
		b, _ := json.MarshalIndent(signals(args[1]), "", " ")
		fmt.Println(string(b))
		return 0
	}
	if len(args) < 2 {
		fail(usage)
	}
	rest := args[2:]
	flag := func(name, def string) string {
		for i, a := range rest {
			if a == "--"+name && i+1 < len(rest) {
				return rest[i+1]
			}
		}
		return def
	}
	o := Options{Char: args[0], Goal: args[1], Timeout: dur(flag("timeout", "4h")), Every: dur(flag("every", "15s")),
		Samples: flag("samples", ""), Record: flag("record", ""), Board: flag("board", ""), Events: flag("events", ""),
		Role: flag("role", ""), Runtime: flag("runtime", ""), Resume: flag("resume", "")}
	res, met, err := Run(context.Background(), o)
	if err != nil {
		fail("%v", err)
	}
	fmt.Println(string(res))
	if met {
		return 0
	}
	return 1
}

// Options for one judged character.
type Options struct {
	Char, Goal     string
	Timeout, Every time.Duration
	Samples        string // append each sample (JSONL)
	Record         string // append each sample as an {"ev":"score"} event (the viewer's recorder log)
	Board          string // dir: write <Char>.json with the latest result each poll (standings)
	Events         string // the character's event stream: combat totals and, from its neighbours, cooperation
	Role, Runtime  string // added to the board (and the runtime to the result)
	Resume         string // samples.jsonl of a paused run: same baseline and curve; the clock resumes at the last one
}

// Run judges until the goal is met, the timeout passes or ctx is done, and returns the final result line (JSON)
// and whether the goal was met. A poll that fails (the database) is skipped and retried at the next one.
func Run(ctx context.Context, o Options) (result []byte, met bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(failure)
			if !ok {
				panic(r)
			}
			err = fmt.Errorf("%s", string(f))
		}
	}()
	if !nameRe.MatchString(o.Char) || !goalRe.MatchString(o.Goal) {
		return nil, false, fmt.Errorf("%s", usage)
	}
	char, goal := o.Char, o.Goal
	timeout, every := o.Timeout, o.Every
	if every == 0 {
		every = 15 * time.Second
	}
	samples, record, board := o.Samples, o.Record, o.Board
	eventsFile, role, runtime := o.Events, o.Role, o.Runtime
	// cooperation needs everyone's streams: the other <Char>.events.jsonl beside this one (a team dir)
	var team *streams
	if eventsFile != "" {
		team = newStreams(filepath.Dir(eventsFile))
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
	if f := o.Resume; f != "" {
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
	var curve metrics.Curve // a resumed run replays its samples: same curve, area and best window
	for _, p := range prior {
		curve.Add(p.ElapsedS/3600, p.Value)
	}
	poll := func() ([]byte, bool, bool) {
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
		r := curve.Add(elapsedS/3600, value)
		rates := obj{{"rate", round1(r.Rate)}, {"recent_rate", round1(r.Recent)}, {"best_window", round1(r.BestWindow)},
			{"pace", round1(r.Pace)}, {"score", round1(r.Score)}}
		smp := append(append(obj{{"elapsed_s", elapsedS}, {"level", level}, {"xp", xp}, {"money", money}, {"value", value}}, rates...),
			kv{"signals", sig})
		if samples != "" {
			appendLine(samples, smp)
		}
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
				team.read()
				mine := team.since(start)
				var combat metrics.Combat
				for _, e := range mine[char] {
					combat.Add(e)
				}
				co := metrics.Team(mine)[char]
				b = append(b, kv{"damage_dealt", combat.DamageDealt}, kv{"damage_taken", combat.DamageTaken},
					kv{"healing_done", combat.HealingDone}, kv{"assist_share", round3(co.AssistShare)},
					kv{"contested_hits", co.Contested})
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
			return j, value >= need, true
		}
		return nil, false, false

	}
	for {
		var done bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(failure); !ok {
						panic(r)
					}
					fmt.Fprintf(os.Stderr, "judge %s: %v (skipping this poll)\n", char, r)
				}
			}()
			result, met, done = poll()
		}()
		if done {
			return result, met, nil
		}
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-time.After(every):
		}
	}
}
