package judge

import (
	"math"
	"strconv"
	"strings"

	"github.com/pthm/leeroy/azsdk/metrics"
	"github.com/pthm/leeroy/azsdk/types"
	"github.com/pthm/leeroy/internal/bench/config"
)

// A goal's verdict comes from what the run recorded, never from a value stored along the way: the judge's samples (the
// character database every poll: what the agents can't change) say whether a goal is met, and the character's game
// log (the session's events, timed to the millisecond) says when. The live judge, the report and publish all compute
// it with goalVerdict, so they agree, and a change to scoring applies to old runs too (azbench report again).

// record is what goalVerdict reads from a sample: when it was taken (unix ms, and seconds on the run's clock) and the
// goal's value then.
type record struct {
	T        int64   `json:"t"`
	ElapsedS float64 `json:"elapsed_s"`
	Value    float64 `json:"value"`
}

// goalVerdict: whether a binary goal (binaryGoal) was met, and at what second on the run's clock. It is met at the
// first sample whose value reaches need; the time is the game-log event that made it so, at or before that sample
// (mapped onto the clock through the sample, so a pause or an outage hold before it doesn't shift it), or the
// sample's own time if no event says (one the log missed, as across a session restart).
func goalVerdict(goal string, need float64, evs []metrics.Event, samples []record) (met bool, at float64) {
	kind, n, _ := strings.Cut(goal, ":")
	if kind == "duels" { // decided by the game log alone: the server's statistics leave fled duels out
		duels, _ := duelsOf(evs, samples, int(need))
		if float64(len(duels)) < need || len(samples) == 0 {
			return false, 0
		}
		return true, eventSecond(lastDuel(evs, int(need)), samples)
	}
	i := -1
	for k, s := range samples {
		if s.Value >= need {
			i = k
			break
		}
	}
	if i < 0 {
		return false, 0
	}
	confirm := samples[i]
	id, _ := strconv.ParseUint(n, 10, 32)
	var t int64 // the event's time (unix ms), 0 if none
	for _, e := range evs {
		if e.T > confirm.T {
			break
		}
		hit := false
		switch d := e.Data.(type) {
		case types.QuestDone:
			hit = kind == "quest" && e.Type == "quest_turned_in" && uint64(d.Quest) == id
		case types.AchievementEarned:
			hit = kind == "achievement" && uint64(d.ID) == id
		case types.Location:
			hit = kind == "zone" && e.Type == "area_changed" && uint64(d.ZoneID) == id
		case types.LevelUp:
			hit = kind == "level" && float64(d.Level) >= need
		case types.Kill:
			hit = kind == "kills" // the last kill before the sample is the one that reached the count
		}
		if hit && (t == 0 || kind == "kills") {
			t = e.T
		}
	}
	if t == 0 {
		return true, confirm.ElapsedS
	}
	return true, eventSecond(t, samples[:i+1])
}

// eventSecond maps an event's time (unix ms) onto the run's clock through the last sample at or after it (or the
// last sample before, for an event after them all): the sample's elapsed seconds less the time between them.
func eventSecond(t int64, samples []record) float64 {
	if len(samples) == 0 {
		return 0
	}
	ref := samples[len(samples)-1]
	for _, s := range samples {
		if s.T >= t {
			ref = s
			break
		}
	}
	return math.Max(0, math.Round((ref.ElapsedS-float64(ref.T-t)/1000)*10)/10)
}

// lastDuel: the time (unix ms) of the nth decided duel in the events, 0 if there aren't that many.
func lastDuel(evs []metrics.Event, nth int) int64 {
	k := 0
	for _, e := range evs {
		if d, ok := e.Data.(types.Duel); ok && e.Type == "duel_ended" && d.Result != "cancelled" {
			k++
			if k == nth {
				return e.T
			}
		}
	}
	return 0
}

// finalScore is a character's score card from what its run recorded (its samples and events), as the report and
// publish show it: value and rates (the last sample's), met, time_to_goal_s and score by goalVerdict, and the goal's
// checkpoints and duels from the events. Rate goals keep the last sample's score (it is a function of the samples).
func finalScore(goal string, minutes float64, cps []config.Checkpoint, samples []map[string]any, evs []metrics.Event) obj {
	if len(samples) == 0 {
		return nil
	}
	l := samples[len(samples)-1]
	recs := make([]record, 0, len(samples))
	for _, s := range samples {
		recs = append(recs, record{T: int64(fnum(s["t"])), ElapsedS: fnum(s["elapsed_s"]), Value: fnum(s["value"])})
	}
	kind, n, _ := strings.Cut(goal, ":")
	if kind == "skill" {
		_, n, _ = strings.Cut(n, ":")
	}
	need, _ := strconv.ParseFloat(n, 64)
	if kind == "item" || kind == "quality" || kind == "quest" || kind == "achievement" || kind == "zone" {
		need = 1
	}
	f := obj{{"value", l["value"]}, {"rate", l["rate"]}, {"recent_rate", l["recent_rate"]}, {"best_window", l["best_window"]}, {"pace", l["pace"]}}
	score := l["score"]
	met := fnum(l["value"]) >= need
	var ttg any
	if binaryGoal(goal) || kind == "duels" {
		var at float64
		if met, at = goalVerdict(goal, need, evs, recs); met {
			ttg = at
		}
	}
	reached := map[string]float64{}
	var cpList []obj
	if len(cps) > 0 {
		cpList = checkpoints(cps, evs, recs, reached)
	}
	switch {
	case kind == "duels":
		duels, won := duelsOf(evs, recs, int(need))
		score = float64(won)
		f[0].v = float64(len(duels))
		f = append(f, kv{"duels", duels}, kv{"duels_won", won})
	case binaryGoal(goal):
		score = timeScore(met, fnum(ttg))
		if !met && len(cps) > 0 {
			score = checkpointScore(len(reached), len(cps), minutes*60)
		}
	}
	f = append(f, kv{"score", score}, kv{"met", met})
	if binaryGoal(goal) || kind == "duels" {
		f = append(f, kv{"time_to_goal_s", ttg})
	}
	if cpList != nil {
		f = append(f, kv{"checkpoints", cpList})
	}
	return f
}
