// judge party [minutes = 10] (the agents' `party`): how well your current party is working, from the server's record of the last few minutes.
// Per member: XP/hour, kills, deaths, and how their time went: fighting, travelling, idle (alive, not fighting, not
// moving: usually waiting), dead, and with the group (within 50 yd of another member). For you: XP/hour in this
// party vs before you joined it. Reads the server's recorder log (positions every second, XP, deaths).
package judge

import (
	"encoding/json"
	"fmt"
	"github.com/pthm/leeroy/azeroth-sdk/metrics"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pthm/leeroy/azworld/world"
)

type partyEv struct {
	T      int64   `json:"t"`
	Ev     string  `json:"ev"`
	N      string  `json:"n"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	M      float64 `json:"m"`
	Grp    float64 `json:"grp"`
	Lvl    any     `json:"lvl"`
	Alive  any     `json:"alive"`
	Combat any     `json:"combat"`
	Amount float64 `json:"amount"`
	Victim any     `json:"victim"`
}

type member struct {
	Name          string  `json:"name"`
	You           bool    `json:"you,omitempty"`
	Level         any     `json:"level,omitempty"`
	XPPerHour     float64 `json:"xp_per_hour"`
	Kills         int     `json:"kills"`
	Deaths        int     `json:"deaths"`
	WithGroupPct  float64 `json:"with_group_pct"`
	FightingPct   float64 `json:"fighting_pct"`
	TravellingPct float64 `json:"travelling_pct"`
	IdlePct       float64 `json:"idle_pct"`
	DeadPct       float64 `json:"dead_pct"`
}

func out(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
	os.Exit(0)
}

func round(v float64) float64 { return math.Floor(v + 0.5) }

func runParty(args []string) {
	log := filepath.Join(filepath.Dir(world.DataDir()), "server/azerothcore/env/dist/logs/recorder.jsonl")
	me := os.Getenv("WOWBENCH_CHAR")
	mins := 10.0
	if len(args) > 0 {
		mins, _ = strconv.ParseFloat(args[0], 64)
	}
	now := time.Now().UnixMilli()
	from := now - int64(math.Max(mins, 30)*60_000) // read 30 min back for the "before" comparison

	// the tail of the log only: ~0.4 MB per player-minute at worst
	f, err := os.Open(log)
	if err != nil {
		out(map[string]string{"error": "no server record: " + err.Error()})
	}
	st, _ := f.Stat()
	want := min(st.Size(), 40<<20)
	f.Seek(st.Size()-want, io.SeekStart)
	buf, _ := io.ReadAll(f)
	f.Close()
	var evs []partyEv
	for _, l := range strings.Split(string(buf), "\n") {
		if !strings.Contains(l, `"ev":"pos"`) && !strings.Contains(l, `"ev":"xp"`) && !strings.Contains(l, `"ev":"death"`) {
			continue
		}
		var e partyEv
		if json.Unmarshal([]byte(l), &e) == nil && e.T >= from {
			evs = append(evs, e)
		}
	}
	var pos []partyEv
	last := map[string]partyEv{}
	var order []string // members in order of their last sample, like a JS Map
	for _, e := range evs {
		if e.Ev == "pos" {
			pos = append(pos, e)
			if _, ok := last[e.N]; !ok {
				order = append(order, e.N)
			}
			last[e.N] = e
		}
	}
	mine, ok := last[me]
	if !ok {
		who := me
		if who == "" {
			who = "you (WOWBENCH_CHAR unset)"
		}
		out(map[string]string{"error": "no recent record of " + who})
	}
	if mine.Grp == 0 {
		out(map[string]any{"group": nil, "note": "you're not in a group"})
	}
	var members []string
	for _, n := range order {
		if last[n].Grp == mine.Grp {
			members = append(members, n)
		}
	}

	// when you joined this group: your first sample in it after the last sample outside it
	joined := from
	for _, p := range pos {
		if p.N == me && p.Grp != mine.Grp {
			joined = p.T
		}
	}
	winFrom := max(now-int64(mins*60_000), joined)
	hours := math.Max(1.0/60, float64(now-winFrom)/3_600_000)

	// each member's time use over the window (metrics.Use: fighting, travelling, idle, dead, with the group)
	inWindow := map[string][]metrics.Pos{}
	for _, p := range pos {
		if slices.Contains(members, p.N) && p.T >= winFrom {
			inWindow[p.N] = append(inWindow[p.N], metrics.Pos{T: p.T, X: p.X, Y: p.Y, Map: p.M, Alive: truthy(p.Alive),
				Combat: truthy(p.Combat), Group: p.Grp})
		}
	}
	var res []member
	for _, n := range members {
		u := metrics.Use(n, inWindow)
		xp, kills, deaths := 0.0, 0, 0
		for _, e := range evs {
			if e.N != n || e.T < winFrom {
				continue
			}
			if e.Ev == "xp" {
				xp += e.Amount
				if truthy(e.Victim) {
					kills++
				}
			}
			if e.Ev == "death" {
				deaths++
			}
		}
		res = append(res, member{Name: n, You: n == me, Level: last[n].Lvl, XPPerHour: round(xp / hours), Kills: kills, Deaths: deaths,
			WithGroupPct: u.WithGroupPct, FightingPct: u.FightingPct, TravellingPct: u.TravellingPct, IdlePct: u.IdlePct, DeadPct: u.DeadPct})
	}
	myXPBefore := 0.0
	for _, e := range evs {
		if e.Ev == "xp" && e.N == me && e.T < joined && e.T >= from {
			myXPBefore += e.Amount
		}
	}
	beforeHours := float64(joined-from) / 3_600_000
	avg := func(f func(member) float64) float64 {
		s := 0.0
		for _, m := range res {
			s += f(m)
		}
		return round(s / float64(len(res)))
	}
	var party struct {
		XPPerHour       float64 `json:"xp_per_hour"`
		Kills           int     `json:"kills"`
		Deaths          int     `json:"deaths"`
		AvgWithGroupPct float64 `json:"avg_with_group_pct"`
		AvgIdlePct      float64 `json:"avg_idle_pct"`
		AvgDeadPct      float64 `json:"avg_dead_pct"`
	}
	for _, m := range res {
		party.XPPerHour += m.XPPerHour
		party.Kills += m.Kills
		party.Deaths += m.Deaths
	}
	party.AvgWithGroupPct = avg(func(m member) float64 { return m.WithGroupPct })
	party.AvgIdlePct = avg(func(m member) float64 { return m.IdlePct })
	party.AvgDeadPct = avg(func(m member) float64 { return m.DeadPct })
	var you struct {
		InParty *float64 `json:"xp_per_hour_in_party,omitempty"`
		Before  *float64 `json:"xp_per_hour_before_joining,omitempty"`
	}
	for _, m := range res {
		if m.You {
			v := m.XPPerHour
			you.InParty = &v
		}
	}
	if beforeHours > 0.05 {
		v := round(myXPBefore / beforeHours)
		you.Before = &v
	}
	out(struct {
		Group   float64  `json:"group"`
		Minutes float64  `json:"minutes"`
		Members []member `json:"members"`
		Party   any      `json:"party"`
		You     any      `json:"you"`
	}{mine.Grp, round(float64(now-winFrom) / 60_000), res, party, you})
}
