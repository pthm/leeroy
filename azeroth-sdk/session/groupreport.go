package session

import (
	"math"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/metrics"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// groupLog keeps a second-by-second record of you and your party from what the client sees (positions and state from
// party updates, combat while in sight) and the kills and deaths, for group.report. An hour of it.
type groupLog struct {
	mu     sync.Mutex
	last   time.Time
	pos    map[string][]metrics.Pos
	sight  map[string][]bool // in sight at each sample (combat is only known then)
	kills  map[string][]int64
	deaths map[string][]int64
	dead   map[string]bool
}

const groupLogKeep = time.Hour

func (g *groupLog) trim(now time.Time) {
	cut := now.Add(-groupLogKeep).UnixMilli()
	for n, ps := range g.pos {
		i := 0
		for i < len(ps) && ps[i].T < cut {
			i++
		}
		g.pos[n], g.sight[n] = ps[i:], g.sight[n][i:]
	}
}

// sampleGroup records one sample a second while you're in a party (called from the tick loop).
func (b *Bot) sampleGroup() {
	g := b.group
	if g == nil || b.lua == nil {
		return
	}
	now := time.Now()
	if now.Sub(g.last) < time.Second {
		return
	}
	g.last = now
	info, ok := b.lua.Native("group.info").(types.Group)
	if !ok {
		return // solo
	}
	x, y, _, _, m := b.world.Position()
	t := now.UnixMilli()
	g.mu.Lock()
	defer g.mu.Unlock()
	add := func(name string, p metrics.Pos, inSight bool) {
		p.T, p.Group = t, 1
		g.pos[name] = append(g.pos[name], p)
		g.sight[name] = append(g.sight[name], inSight)
		if !p.Alive && !g.dead[name] {
			g.deaths[name] = append(g.deaths[name], t)
		}
		g.dead[name] = !p.Alive
	}
	add(b.config.CharacterName, metrics.Pos{X: float64(x), Y: float64(y), Map: float64(m),
		Alive: b.IsAlive() && !b.IsGhost(), Combat: b.InCombat()}, true)
	for _, mb := range info.Members {
		if mb.Position == nil {
			continue
		}
		// in sight: exact. Out of sight the party updates give a position, but whether they're alive only when the
		// update carries their health: 0 of a known maximum is dead, else the last known state holds
		alive := mb.Alive && !mb.Ghost
		if !mb.InRange {
			switch {
			case mb.Ghost || (mb.Health.Max > 0 && mb.Health.Current == 0):
				alive = false
			case mb.Health.Max == 0:
				alive = !g.dead[mb.Name]
			}
		}
		add(mb.Name, metrics.Pos{X: mb.Position.X, Y: mb.Position.Y, Map: float64(m), Alive: alive, Combat: mb.InCombat},
			mb.InRange)
	}
	g.trim(now)
}

func (b *Bot) setupGroupReport() {
	g := &groupLog{pos: map[string][]metrics.Pos{}, sight: map[string][]bool{}, kills: map[string][]int64{},
		deaths: map[string][]int64{}, dead: map[string]bool{}}
	b.group = g
	b.lua.Bus.Listen(func(ev script.Event) {
		var who string
		switch d := ev.Data.(type) {
		case types.Kill:
			who = b.config.CharacterName
			if ev.Type == "party_kill" && d.Killer != nil {
				who = d.Killer.Name
			}
		default:
			return
		}
		g.mu.Lock()
		g.kills[who] = append(g.kills[who], time.Now().UnixMilli())
		g.mu.Unlock()
	})
	b.lua.Func("group.report", "([minutes = 10]) → GroupReport?",
		"how your party has spent the last minutes, from what you've seen of them: each member's time fighting, travelling, idle, dead and with the group, and their kills and deaths (XP and scores: the standings); nil when you're solo",
		func(minutes float64) any {
			if minutes <= 0 {
				minutes = 10
			}
			info, ok := b.lua.Native("group.info").(types.Group)
			if !ok {
				return nil
			}
			from := time.Now().Add(-time.Duration(minutes * float64(time.Minute))).UnixMilli()
			names := []string{b.config.CharacterName}
			for _, m := range info.Members {
				names = append(names, m.Name)
			}
			g.mu.Lock()
			defer g.mu.Unlock()
			window := map[string][]metrics.Pos{}
			for _, n := range names {
				for i, p := range g.pos[n] {
					if p.T >= from {
						window[n] = g.pos[n][i:]
						break
					}
				}
			}
			count := func(ts []int64) (n int) {
				for _, t := range ts {
					if t >= from {
						n++
					}
				}
				return n
			}
			r := types.GroupReport{Minutes: minutes, Members: []types.GroupMemberReport{}}
			for _, n := range names {
				u := metrics.Use(n, window)
				sight := g.sight[n][len(g.sight[n])-len(window[n]):]
				seen := 0
				for _, s := range sight {
					if s {
						seen++
					}
				}
				in := 0.0
				if len(sight) > 0 {
					in = math.Round(100 * float64(seen) / float64(len(sight)))
				}
				r.Members = append(r.Members, types.GroupMemberReport{Name: n, You: n == b.config.CharacterName,
					Seconds: u.Seconds, FightingPct: u.FightingPct, TravellingPct: u.TravellingPct, IdlePct: u.IdlePct,
					DeadPct: u.DeadPct, WithGroupPct: u.WithGroupPct, InSightPct: in, Kills: count(g.kills[n]),
					Deaths: count(g.deaths[n])})
			}
			return r
		})
}
