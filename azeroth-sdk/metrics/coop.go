package metrics

import (
	"slices"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// HealWindowMS: a heal counts as useful if its target lands a useful hit within this long after it.
const HealWindowMS = 30_000

// Coop is one character's cooperation over a run, traced from every player's events the way AgentWorld's causal
// collaboration effectiveness traces actions back from success, with rules instead of a judge model. Success is XP:
// every player who got XP for a kill logs it, naming the victim. A hit is useful if the mob later gave XP to
// someone; a heal is useful if its target lands a useful hit within HealWindowMS.
//
//	Useful      your hits on mobs that gave you XP, plus your useful heals
//	Assists     of those, the ones that also paid someone else: a kill whose XP a teammate shared, or a heal on
//	            another player
//	AssistShare Assists / Useful: how much of your useful work also helped a teammate (solo play is ~0)
//	Contested   your hits on mobs whose XP went only to others (outside your party): you fought someone for the kill
//	Hits        every hit you landed on a creature
type Coop struct {
	Hits, Useful, Assists, Contested int
	AssistShare                      float64
}

// Team computes Coop for every character from all their event streams (one run, in any order).
func Team(streams map[string][]Event) map[string]Coop {
	paid := map[types.GUID]map[string]bool{} // mob -> who got XP for it
	for c, evs := range streams {
		for _, e := range evs {
			if x, ok := e.Data.(types.XPGain); ok && e.Type == "xp" && x.Source == "kill" && x.Victim != nil {
				if paid[x.Victim.GUID] == nil {
					paid[x.Victim.GUID] = map[string]bool{}
				}
				paid[x.Victim.GUID][c] = true
			}
		}
	}
	type heal struct {
		t      int64
		target string
	}
	out := map[string]Coop{}
	usefulAt := map[string][]int64{} // character -> times of its useful hits
	heals := map[string][]heal{}
	for c, evs := range streams {
		var s Coop
		for _, e := range evs {
			d, ok := e.Data.(types.Damage)
			if !ok || !d.Src.Me || d.Amount == 0 {
				continue
			}
			if e.Type == "heal" {
				heals[c] = append(heals[c], heal{e.T, d.Dst.Name})
				continue
			}
			if e.Type != "damage" || d.Taken || d.Dst.Player {
				continue
			}
			s.Hits++
			who := paid[d.Dst.GUID]
			switch {
			case who[c]:
				s.Useful++
				usefulAt[c] = append(usefulAt[c], e.T)
				if len(who) > 1 {
					s.Assists++
				}
			case len(who) > 0:
				s.Contested++
			}
		}
		out[c] = s
	}
	for _, ts := range usefulAt {
		slices.Sort(ts)
	}
	for c, hs := range heals {
		s := out[c]
		for _, h := range hs {
			ts := usefulAt[h.target]
			i, _ := slices.BinarySearch(ts, h.t+1)
			if i < len(ts) && ts[i]-h.t <= HealWindowMS {
				s.Useful++
				if h.target != c {
					s.Assists++
				}
			}
		}
		out[c] = s
	}
	for c, s := range out {
		if s.Useful > 0 {
			s.AssistShare = float64(s.Assists) / float64(s.Useful)
		}
		out[c] = s
	}
	return out
}
