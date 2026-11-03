package metrics

import (
	"math"
	"slices"
)

// Pos is one position sample (the game log's state, once a second).
type Pos struct {
	T         int64 // unix ms
	X, Y, Map float64
	Alive     bool
	Combat    bool
	Group     float64 // party id, 0 = none
	Broken    bool    // its main hand weapon is broken
}

// TimeUse is how a character's sampled time went, in percent. Each second is one of dead, fighting (in combat),
// travelling (moved over 1.5 yd since the last sample) or idle (alive, not fighting, not moving: usually waiting).
// WithGroup: within 50 yd of a member of the same party (within a second either way), and BrokenWeapon: alive with a
// broken main hand weapon, are counted separately. Seconds the character wasn't playing (played false: a pause, its
// agent stopped) don't count.
type TimeUse struct {
	Seconds                                      int
	FightingPct, TravellingPct, IdlePct, DeadPct float64
	WithGroupPct, BrokenWeaponPct                float64
}

// Use computes TimeUse for one character from everyone's samples (char -> samples, any order); played (nil: always)
// says whether the character was playing at a time (unix ms).
func Use(char string, pos map[string][]Pos, played func(t int64) bool) TimeUse {
	at := map[string]map[int64]Pos{}
	for n, ps := range pos {
		at[n] = map[int64]Pos{}
		for _, p := range ps {
			at[n][p.T/1000] = p
		}
	}
	near := func(sec int64, p Pos) bool {
		if p.Group == 0 {
			return false
		}
		for n, m := range at {
			if n == char {
				continue
			}
			for _, s := range []int64{sec, sec - 1, sec + 1} {
				if q, ok := m[s]; ok {
					if q.Group == p.Group && q.Map == p.Map && math.Hypot(q.X-p.X, q.Y-p.Y) < 50 {
						return true
					}
					break
				}
			}
		}
		return false
	}
	secs := make([]int64, 0, len(at[char]))
	for s := range at[char] {
		if played == nil || played(at[char][s].T) {
			secs = append(secs, s)
		}
	}
	slices.Sort(secs)
	var dead, fighting, moving, idle, grouped, broken int
	var prev *Pos
	for _, sec := range secs {
		p := at[char][sec]
		moved := prev != nil && math.Hypot(p.X-prev.X, p.Y-prev.Y) > 1.5
		switch {
		case !p.Alive:
			dead++
		case p.Combat:
			fighting++
		case moved:
			moving++
		default:
			idle++
		}
		if near(sec, p) {
			grouped++
		}
		if p.Alive && p.Broken {
			broken++
		}
		prev = &p
	}
	pct := func(a int) float64 {
		if len(secs) == 0 {
			return 0
		}
		return math.Floor(100*float64(a)/float64(len(secs)) + 0.5)
	}
	return TimeUse{
		Seconds: len(secs), FightingPct: pct(fighting), TravellingPct: pct(moving), IdlePct: pct(idle),
		DeadPct: pct(dead), WithGroupPct: pct(grouped), BrokenWeaponPct: pct(broken),
	}
}
