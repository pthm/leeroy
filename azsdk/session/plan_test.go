package session

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/pthm/leeroy/azsdk/navigation"
)

// Journeys on the real navmesh (PLAN_DATA: the data dir): a lift where walking can't get there, the tram over the
// mountains, boats and a portal across the sea, and on foot where that's quickest.
func TestPlanJourneys(t *testing.T) {
	data := os.Getenv("PLAN_DATA")
	if data == "" {
		t.Skip("PLAN_DATA not set")
	}
	b := &Bot{
		nav: navigation.NewEmbeddedNavigator(data), areaTriggers: loadAreaTriggers(data),
		trs: &transportsState{recs: map[uint64]*transportRec{}, paths: map[uint32]*transportPath{}},
	}
	queries := 0
	planTrace = func(a, c planNode, s float64) {
		queries++
		if os.Getenv("PLAN_TRACE") != "" {
			t.Logf("    walk %d:%.0f,%.0f,%.0f -> %d:%.0f,%.0f,%.0f = %.0fs (stop %d link %d)", a.m, a.p[0], a.p[1], a.p[2], c.m, c.p[0], c.p[1], c.p[2], s, c.stop, c.link)
		}
	}
	defer func() { planTrace = nil }()
	for _, c := range []struct {
		name         string
		m            uint32
		from         [3]float32
		tm           uint32
		to           [3]float32
		local        bool
		rides, gates []string // kinds ridden, portals taken, in order
	}{
		{"Orgrimmar to Thunder Bluff's mesa", 1, [3]float32{1300, -4400, 26}, 1, [3]float32{-1260, 120, 130}, true, []string{"elevator"}, nil},
		{"Stormwind to Ironforge", 0, [3]float32{-8853, 650, 96}, 0, [3]float32{-4918, -955, 501}, true, []string{"tram"}, []string{"Deeprun Tram (from Stormwind)", "Deeprun Tram (to Ironforge)"}},
		{"Goldshire to Moonbrook", 0, [3]float32{-9460, 64, 56}, 0, [3]float32{-11208, 1668, 26}, true, nil, nil},
		{"Ratchet to Auberdine (Flash Lite's)", 1, [3]float32{-894, -3773, 11}, 1, [3]float32{6450, 483, 9}, false, nil, nil},
		{"Theramore to Auberdine", 1, [3]float32{-3700, -4400, 11}, 1, [3]float32{6450, 483, 9}, false, nil, nil},
		{"Theramore to Auberdine, planned again (walks remembered)", 1, [3]float32{-3700, -4400, 11}, 1, [3]float32{6450, 483, 9}, false, nil, nil},
		{"Stormwind to Darnassus", 0, [3]float32{-8853, 650, 96}, 1, [3]float32{9945, 2616, 1316}, false, []string{"ship", "ship"}, []string{"Teddrassil - Darnassus"}},
	} {
		queries, planQueryTime = 0, 0
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		t0 := time.Now()
		p, err := b.plan(ctx, c.m, c.from, c.tm, c.to, c.local, "alliance")
		cancel()
		if err != nil || p == nil {
			t.Errorf("%s: no plan (%v)", c.name, err)
			continue
		}
		var rides, gates []string
		for _, l := range p.Legs {
			switch l.Action {
			case "ride":
				rides = append(rides, l.Kind)
			case "portal":
				gates = append(gates, l.Name)
			}
			t.Logf("  %s %s %s %.0fs to %d:%.0f,%.0f,%.0f", l.Action, l.Kind, l.Name, l.Seconds, l.To.Map, l.To.X, l.To.Y, l.To.Z)
		}
		t.Logf("%s: %.0fs, planned in %s (%d walks routed, %s of it routing)", c.name, p.Seconds, time.Since(t0).Round(time.Millisecond), queries, planQueryTime.Round(time.Millisecond))
		if fmtList(rides) != fmtList(c.rides) || fmtList(gates) != fmtList(c.gates) {
			t.Errorf("%s: rides %v portals %v, want %v %v", c.name, rides, gates, c.rides, c.gates)
		}
	}
}

func fmtList(s []string) string {
	out := ""
	for _, v := range s {
		out += v + ";"
	}
	return out
}
