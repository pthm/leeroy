package metrics

import (
	"math"
	"testing"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestCurve(t *testing.T) {
	var c Curve
	// 1000 an hour for 30 minutes, then 4000 an hour for 15: 1500 in 0.75 h
	var r Rates
	for m := 0; m <= 45; m++ {
		v := float64(min(m, 30))*1000/60 + float64(max(0, m-30))*4000/60
		r = c.Add(float64(m)/60, v)
	}
	if !near(r.Rate, 2000) || !near(r.BestWindow, 4000) || math.Abs(r.Recent-4000) > 1e-6*4000 || !near(r.Score, 3000) {
		t.Fatalf("rates %+v", r)
	}
	// the first 10 minutes count as 10
	var d Curve
	if r := d.Add(1.0/60, 100); !near(r.Rate, 600) {
		t.Fatalf("floor: %+v", r)
	}
}

func dmg(src, dst string, mob types.GUID, amount uint32) Event {
	return Event{Type: "damage", Data: types.Damage{Src: types.UnitRef{Name: src, Me: true},
		Dst: types.UnitRef{Name: dst, GUID: mob}, Amount: amount}}
}

func TestTeam(t *testing.T) {
	xp := func(at int64, mob types.GUID) Event {
		return Event{T: at, Type: "xp", Data: types.XPGain{Amount: 50, Source: "kill", Victim: &types.UnitRef{GUID: mob}}}
	}
	heal := Event{T: 1000, Type: "heal", Data: types.Damage{Src: types.UnitRef{Name: "Healer", Me: true},
		Dst: types.UnitRef{Name: "Tank", Player: true}, Amount: 80}}
	tankHit := dmg("Tank", "Wolf", 1, 30)
	tankHit.T = 5000
	solo := dmg("Solo", "Boar", 2, 30)
	stolen := dmg("Solo", "Wolf", 3, 30) // mob 3: XP went to the party, not Solo
	s := Team(map[string][]Event{
		"Tank":   {tankHit, xp(6000, 1), xp(9000, 3)},
		"Healer": {heal, xp(6000, 1), xp(9000, 3)},
		"Solo":   {solo, stolen, xp(7000, 2)},
	})
	if c := s["Tank"]; c.Useful != 1 || c.Assists != 1 || !near(c.AssistShare, 1) {
		t.Fatalf("tank %+v", c)
	}
	if c := s["Healer"]; c.Useful != 1 || c.Assists != 1 {
		t.Fatalf("healer %+v", c)
	}
	if c := s["Solo"]; c.Hits != 2 || c.Useful != 1 || c.Assists != 0 || c.Contested != 1 {
		t.Fatalf("solo %+v", c)
	}
}

func TestUse(t *testing.T) {
	var me, mate []Pos
	for s := int64(0); s < 10; s++ {
		p := Pos{T: s * 1000, X: float64(s * 2 * min(s, 5)), Alive: s < 8, Combat: s >= 6, Group: 1}
		me = append(me, p)
		mate = append(mate, Pos{T: s * 1000, X: 10, Alive: true, Group: 1})
	}
	u := Use("me", map[string][]Pos{"me": me, "mate": mate})
	if u.Seconds != 10 || u.DeadPct != 20 || u.FightingPct != 20 || u.TravellingPct != 50 || u.IdlePct != 10 {
		t.Fatalf("use %+v", u)
	}
}
