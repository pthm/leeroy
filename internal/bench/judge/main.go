// judge <character> <goal> [--timeout 4h] [--every 15s] [--samples file.jsonl] ...
// judge signals <character>: print the character's progress signals (signals.go).
//
// Goals: level:<n> | item:<entry> | quality:<0-5> (any owned item of that quality or better; 3 = rare)
//
//	| money:<copper> | quests:<n> (quests turned in) | quest:<id> (that quest turned in)
//	| skill:<id>:<n> (that skill line at n or more, e.g. skill:164:75 = Blacksmithing 75)
//	| achievement:<id> (that achievement earned, e.g. 628 = Deadmines: Edwin VanCleef killed)
//	| zone:<id> (the character is in that zone, an AreaTable id: e.g. 1657 = Darnassus)
//	| checkpoints (every one of Options.Checkpoints reached, in any order: e.g. zones to visit; met when the last is)
//	| kills:<n> (n creatures killed since the judge started: the game's own statistic)
//	| duels:<n> (n duels fought to a result, won or lost; scored on the duels won: duels in the result lists each)
//	| achpoints:<n> (achievement points earned since the judge started; needs bin/azworld setup.
//	  Use a large n to score "as many as possible" by the value at timeout)
//	| score:<n> (composite: weighted change in every signal since the judge started; weights in score.json,
//	  signals in signals.go. Same "large n" convention)
//
// Reads the character DB (saved every 15s, see server/override.yml), so it can't be gamed from the client; a goal is
// met when a sample shows it, at the time of the game-log event that did it (goalVerdict, verdict.go).
// Prints one JSON line when the goal is met or the timeout hits; exit 0 = met, 1 = timed out.
// --samples appends {elapsed_s, level, xp, money, value, rate, ..., signals} each poll, for partial credit and curves.
//
// Rate of progress (all goals, from the value curve):
//
//	rate         value per hour since the start (the first 10 minutes count as 10)
//	recent_rate  value per hour over the last 10 minutes
//	pace         the time-average of value so far (area under the curve / time)
//	best_window  the best rate over any 15-minute window
//	score        binary goals (achievement, quest, item, quality): 3600 / time_to_goal_s, 0 until met; with
//	             checkpoints (Options.Checkpoints), unmet scores the share reached times 3600 / the timeout.
//	             Other goals: (rate + best_window) / 2, the headline for score goals. Sustained progress counts, and so does
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
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pthm/leeroy/azsdk/metrics"
	"github.com/pthm/leeroy/azsdk/types"
	"github.com/pthm/leeroy/internal/bench/config"
	"github.com/pthm/leeroy/internal/bench/tools"
)

// binaryGoal: a goal that is met or not (an achievement, a quest, an item, an item quality). Its score is by time.
func binaryGoal(goal string) bool {
	kind, _, _ := strings.Cut(goal, ":")
	return kind == "achievement" || kind == "quest" || kind == "item" || kind == "quality" || kind == "zone" || kind == "kills" || kind == "checkpoints"
}

// timeScore is a binary goal's score: 3600 / time_to_goal_s (1 = met in an hour), 0 while unmet.
func timeScore(met bool, elapsedS float64) float64 {
	if !met {
		return 0
	}
	return round3(3600 / math.Max(elapsedS, 1))
}

// checkpointScore is an unmet binary goal's partial credit: the share of its checkpoints reached, times the score of
// meeting the goal as the clock runs out (3600 / timeout_s). Meeting the goal scores at least that, so any finish
// outranks any unfinished run, and among those, more checkpoints rank higher.
func checkpointScore(reached, total int, timeoutS float64) float64 {
	if total == 0 || timeoutS <= 0 {
		return 0
	}
	return round3(float64(reached) / float64(total) * 3600 / timeoutS)
}

// failure is a judge error: a bad argument, or the database. Main prints it and exits 2; Run skips a failed poll.
type failure string

func fail(f string, a ...any) { panic(failure(fmt.Sprintf(f, a...))) }

const usage = "usage: judge <character> <level|item|quality|money|quests|quest|achievement|achpoints|zone|kills|duels|score>:<n> | skill:<id>:<n> | checkpoints [--timeout 4h] [--every 15s] [--samples f] [--resume f] [--board dir] [--events f] [--role r] [--runtime r] [--hardcore]\n       judge signals <character>"

var goalRe = regexp.MustCompile(`^((level|item|quality|money|quests|quest|achievement|achpoints|zone|kills|duels|score):\d+|skill:\d+:\d+|checkpoints)$`)

func dur(s string) time.Duration {
	m := regexp.MustCompile(`^(\d+)([smh])$`).FindStringSubmatch(s)
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
	if _, err := f.Write(append(b, '\n')); err != nil {
		fail("%v", err)
	}
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
	has := func(name string) bool { return slices.Contains(rest, "--"+name) }
	o := Options{
		Char: args[0], Goal: args[1], Timeout: dur(flag("timeout", "4h")), Every: dur(flag("every", "15s")),
		Samples: flag("samples", ""), Board: flag("board", ""), Events: flag("events", ""),
		Role: flag("role", ""), Runtime: flag("runtime", ""), Resume: flag("resume", ""), Hardcore: has("hardcore"),
	}
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
	Board          string // dir: write <Char>.json with the latest result each poll (standings)
	Events         string // the character's event stream: combat totals and, from its neighbours, cooperation
	Role, Runtime  string // added to the board (and the runtime to the result)
	Resume         string // samples.jsonl of a paused run: same baseline and curve; the clock resumes at the last one
	// Hardcore: the first death ends the run, the goal unmet, and the score is the total value earned before the death
	// or the clock (how far it got without dying; a fast start that dies early doesn't score its rate)
	Hardcore bool
	// Checkpoints: each recorded (samples, result) with the seconds on the clock when the character or its party first
	// killed its creature (the Events stream's kill and party_kill), so an unfinished run shows how far it got
	Checkpoints []config.Checkpoint
	// Hold, while it returns true, stops this character's clock: no polls, and the time doesn't count (a provider
	// outage: the character is logged out until its agent can play again)
	Hold func() bool
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
	samples, board := o.Samples, o.Board
	eventsFile, role, runtime := o.Events, o.Role, o.Runtime
	// cooperation needs everyone's streams: the run's other game/<char>/events.jsonl
	var team *streams
	if eventsFile != "" {
		team = newStreams(filepath.Dir(filepath.Dir(filepath.Dir(eventsFile))))
	}
	if board != "" {
		if err := os.MkdirAll(board, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "judge %s: %v\n", char, err)
		}
	}

	parts := strings.Split(goal, ":") // skill:<id>:<n>: n is the skill id, the third part the level
	kind, n := parts[0], parts[1]
	owned := `FROM character_inventory ci JOIN item_instance ii ON ii.guid = ci.item
  JOIN acore_world.item_template it ON it.entry = ii.itemEntry
  WHERE ci.guid = (SELECT guid FROM characters WHERE name = '` + char + `')`
	g := "(SELECT guid FROM characters WHERE name = '" + char + "')"
	query := map[string]string{
		"level":       "SELECT level FROM characters WHERE name = '" + char + "'",
		"money":       "SELECT money FROM characters WHERE name = '" + char + "'",
		"item":        "SELECT COUNT(*) " + owned + " AND it.entry = " + n,
		"quality":     "SELECT COUNT(*) " + owned + " AND it.Quality >= " + n,
		"quests":      "SELECT COUNT(*) FROM character_queststatus_rewarded WHERE guid = " + g,
		"quest":       "SELECT COUNT(*) FROM character_queststatus_rewarded WHERE quest = " + n + " AND guid = " + g,
		"skill":       "SELECT COALESCE(MAX(value), 0) FROM character_skills WHERE skill = " + n + " AND guid = " + g,
		"achievement": "SELECT COUNT(*) FROM character_achievement WHERE achievement = " + n + " AND guid = " + g,
		"zone":        "SELECT COUNT(*) FROM characters WHERE zone = " + n + " AND name = '" + char + "'",
		"achpoints":   "SELECT COALESCE(SUM(a.points), 0) FROM character_achievement ca JOIN wowbench.achievement a ON a.id = ca.achievement\n    WHERE ca.guid = " + g,
	}[kind]

	// --resume <samples.jsonl>: continue a paused run from its samples: same baseline, same curve, and the clock
	// picks up at the last sample (the pause doesn't count)
	type sample struct {
		T           int64              `json:"t"`
		ElapsedS    float64            `json:"elapsed_s"`
		Value       float64            `json:"value"`
		Signals     map[string]float64 `json:"signals"`
		Checkpoints []struct {
			Name string   `json:"name"`
			AtS  *float64 `json:"at_s"` // seconds on the clock when reached, null until then
		} `json:"checkpoints"`
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
	case "item", "quality", "quest", "achievement", "zone":
		need = 1
	case "skill":
		need, _ = strconv.ParseFloat(parts[2], 64)
	case "checkpoints":
		need = float64(len(o.Checkpoints))
	}

	nowMs := func() int64 { return time.Now().UnixMilli() }
	start := nowMs()
	if len(prior) > 0 {
		start -= int64(prior[len(prior)-1].ElapsedS * 1000)
	}
	reached := map[string]float64{} // checkpoint name -> seconds on the clock when first reached
	if len(prior) > 0 {
		for _, c := range prior[len(prior)-1].Checkpoints {
			if c.AtS != nil {
				reached[c.Name] = *c.AtS
			}
		}
	}
	var duels []obj // duels: each one fought to a result, in order
	var won int
	var recs []record // every sample so far, a resumed run's too: what goalVerdict and the event times go by
	for _, p := range prior {
		recs = append(recs, record{T: p.T, ElapsedS: p.ElapsedS, Value: p.Value})
	}
	verdictGoal := binaryGoal(goal) || kind == "duels" // met and timed by goalVerdict (the others: value reaching need)
	var curve metrics.Curve                            // a resumed run replays its samples: same curve, area and best window
	for _, p := range prior {
		curve.Add(p.ElapsedS/3600, p.Value)
	}
	poll := func() ([]byte, bool, bool) {
		sig := signals(char)
		now := nowMs()
		elapsedS := math.Floor(float64(now-start)/1000 + 0.5)
		if team != nil {
			team.read()
		}
		recs = append(recs, record{T: now, ElapsedS: elapsedS})
		var evs []metrics.Event
		if team != nil {
			evs = team.evs[char]
		}
		var cps []obj
		if len(o.Checkpoints) > 0 && team != nil {
			cps = checkpoints(o.Checkpoints, evs, recs, reached)
		}
		var value float64
		switch kind {
		case "checkpoints":
			value = float64(len(reached))
		case "score":
			value = composite(sig, base, w)
		case "kills":
			value = sig.get("creatures_killed") - base["creatures_killed"]
		case "duels":
			if team != nil {
				duels, won = duelsOf(team.evs[char], recs, int(need))
			}
			value = float64(len(duels))
		default:
			value = num(sql(query)) - baseline
		}
		lxm := strings.Split(sql("SELECT level, xp, money FROM characters WHERE name = '"+char+"'"), "\t")
		for len(lxm) < 3 {
			lxm = append(lxm, "")
		}
		level, xp, money := num(lxm[0]), num(lxm[1]), num(lxm[2])
		recs[len(recs)-1].Value = value
		r := curve.Add(elapsedS/3600, value)
		met, at := value >= need, elapsedS
		if kind == "checkpoints" {
			met, at = allReached(reached, len(o.Checkpoints))
		} else if verdictGoal {
			met, at = goalVerdict(goal, need, evs, recs)
		}
		score := round1(r.Score)
		if binaryGoal(goal) {
			score = timeScore(met, at)
			if !met && len(o.Checkpoints) > 0 {
				n := 0
				for _, c := range o.Checkpoints {
					if _, ok := reached[c.Name]; ok {
						n++
					}
				}
				score = checkpointScore(n, len(o.Checkpoints), timeout.Seconds())
			}
		}
		died := o.Hardcore && sig.get("deaths") > base["deaths"]
		var gaveUp map[string]any // standings give-up: the agent's reason, and when
		if board != "" {
			if b, err := os.ReadFile(tools.GiveUpFile(board, char)); err == nil {
				gaveUp = map[string]any{}
				_ = json.Unmarshal(b, &gaveUp) // a damaged file still gives up
			}
		}
		if kind == "duels" {
			score = float64(won)
		}
		if o.Hardcore && !binaryGoal(goal) {
			score = round1(value)
		}
		rates := obj{
			{"rate", round1(r.Rate)},
			{"recent_rate", round1(r.Recent)},
			{"best_window", round1(r.BestWindow)},
			{"pace", round1(r.Pace)},
			{"score", score},
		}
		smp := append(append(obj{{"t", now}, {"elapsed_s", elapsedS}, {"level", level}, {"xp", xp}, {"money", money}, {"value", value}}, rates...),
			kv{"signals", sig})
		if cps != nil {
			smp = append(smp, kv{"checkpoints", cps})
		}
		if kind == "duels" {
			smp = append(smp, kv{"duels", duels}, kv{"duels_won", won})
		}
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
			if err := os.WriteFile(board+"/"+char+".json", j, 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "judge %s: %v\n", char, err)
			}
		}
		if met || died || gaveUp != nil || now-start > timeout.Milliseconds() {
			res := obj{{"char", char}, {"goal", goal}, {"met", met}}
			if runtime != "" {
				res = append(res, kv{"runtime", runtime})
			}
			if o.Hardcore {
				res = append(res, kv{"died", died})
			}
			if gaveUp != nil && !met {
				res = append(res, kv{"gave_up", gaveUp["reason"]})
			}
			var ttg any // null when unmet
			if met {
				ttg = at
			}
			res = append(append(res, smp...), kv{"time_to_goal_s", ttg}, kv{"at", time.Now().UTC().Format("2006-01-02T15:04:05.000Z")})
			j, _ := json.Marshal(res)
			return j, met, true
		}
		return nil, false, false
	}
	var heldAt int64 // when the current hold began (0: not held)
	for {
		if o.Hold != nil && o.Hold() {
			if heldAt == 0 {
				heldAt = nowMs()
			}
			select {
			case <-ctx.Done():
				return nil, false, ctx.Err()
			case <-time.After(time.Second):
			}
			continue
		} else if heldAt != 0 {
			start += nowMs() - heldAt // the hold doesn't count
			heldAt = 0
		}
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

// checkpoints records in reached (name -> seconds on the clock) each checkpoint first reached in evs: its creature
// dies (the character's kill and party_kill events) or the character enters its zone (area_changed), timed through
// the samples (eventSecond). It returns them all in order: {name, at_s}, at_s null until reached.
func checkpoints(cps []config.Checkpoint, evs []metrics.Event, samples []record, reached map[string]float64) []obj {
	for _, e := range evs {
		var kill, zone uint32
		switch d := e.Data.(type) {
		case types.Kill:
			if e.Type == "kill" || e.Type == "party_kill" {
				kill = d.Victim.Entry
			}
		case types.Location:
			if e.Type == "area_changed" {
				zone = d.ZoneID
			}
		}
		for _, c := range cps {
			if _, done := reached[c.Name]; !done && ((kill != 0 && kill == c.Kill) || (zone != 0 && zone == c.Zone)) {
				reached[c.Name] = eventSecond(e.T, samples)
			}
		}
	}
	out := make([]obj, 0, len(cps))
	for _, c := range cps {
		var at any
		if v, ok := reached[c.Name]; ok {
			at = v
		}
		out = append(out, obj{{"name", c.Name}, {"at_s", at}})
	}
	return out
}

// allReached is the checkpoints goal's verdict: met once all total checkpoints are reached, at the last of them.
func allReached(reached map[string]float64, total int) (met bool, at float64) {
	for _, t := range reached {
		at = math.Max(at, t)
	}
	return total > 0 && len(reached) >= total, at
}

// duelsOf lists the first most duels in a character's events fought to a result (duel_ended beaten or fled; cancelled
// ones aren't counted): {at_s, opponent, won, result}, at_s on the run's clock through the samples (eventSecond), and
// how many of them it won.
func duelsOf(evs []metrics.Event, samples []record, most int) (out []obj, won int) {
	out = []obj{}
	for _, e := range evs {
		if len(out) == most { // the goal's duels: any fought after them (between two polls) don't count
			break
		}
		d, ok := e.Data.(types.Duel)
		if !ok || e.Type != "duel_ended" || d.Result == "cancelled" {
			continue
		}
		if d.Won {
			won++
		}
		out = append(out, obj{{"at_s", eventSecond(e.T, samples)}, {"opponent", d.Opponent}, {"won", d.Won}, {"result", d.Result}})
	}
	return out, won
}
