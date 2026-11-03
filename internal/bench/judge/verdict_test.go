package judge

import (
	"testing"

	"github.com/pthm/leeroy/azsdk/metrics"
)

func decode(t *testing.T, lines ...string) []metrics.Event {
	t.Helper()
	out := make([]metrics.Event, 0, len(lines))
	for _, l := range lines {
		e, ok := metrics.Decode([]byte(l))
		if !ok {
			t.Fatalf("decode %s", l)
		}
		out = append(out, e)
	}
	return out
}

func TestGoalVerdict(t *testing.T) {
	// samples every 15 s from t 0; a 10-minute pause (the clock held) between the third and the fourth
	samples := []record{{T: 0, ElapsedS: 0}, {T: 15_000, ElapsedS: 15}, {T: 30_000, ElapsedS: 30}, {T: 645_000, ElapsedS: 45, Value: 1}}
	evs := decode(t,
		`{"t":20000,"type":"achievement","data":{"id":99}}`,
		`{"t":641500,"type":"achievement","data":{"id":628}}`,
	)
	// the event 3.5 s before the sample that confirms it: 41.5 s on the clock, the pause before it not counted
	if met, at := goalVerdict("achievement:628", 1, evs, samples); !met || at != 41.5 {
		t.Fatalf("achievement: %v %v", met, at)
	}
	// no event for it (the log missed it): the confirming sample's time
	if met, at := goalVerdict("quest:176", 1, evs, samples); !met || at != 45 {
		t.Fatalf("quest without an event: %v %v", met, at)
	}
	// not confirmed by the database: not met, whatever the log says
	if met, _ := goalVerdict("achievement:628", 1, evs, samples[:3]); met {
		t.Fatal("met before the database showed it")
	}
	// kills: the last kill at or before the confirming sample
	kills := decode(t,
		`{"t":5000,"type":"kill","data":{"victim":{"guid":"1","name":"Wolf","entry":299}}}`,
		`{"t":12000,"type":"party_kill","data":{"victim":{"guid":"2","name":"Wolf","entry":299}}}`,
		`{"t":40000,"type":"kill","data":{"victim":{"guid":"3","name":"Wolf","entry":299}}}`,
	)
	ks := []record{{T: 0, ElapsedS: 0, Value: 0}, {T: 15_000, ElapsedS: 15, Value: 2}}
	if met, at := goalVerdict("kills:2", 2, kills, ks); !met || at != 12 {
		t.Fatalf("kills: %v %v", met, at)
	}
	// duels: decided by the log, at the nth decided duel (cancelled ones skipped); a sixth doesn't count
	duels := decode(t,
		`{"t":3000,"type":"duel_ended","data":{"state":"ended","result":"cancelled"}}`,
		`{"t":10000,"type":"duel_ended","data":{"state":"ended","result":"beaten","won":true}}`,
		`{"t":21000,"type":"duel_ended","data":{"state":"ended","result":"fled"}}`,
		`{"t":22000,"type":"duel_ended","data":{"state":"ended","result":"beaten","won":true}}`,
	)
	if met, at := goalVerdict("duels:2", 2, duels, samples); !met || at != 21 {
		t.Fatalf("duels: %v %v", met, at)
	}
	if met, _ := goalVerdict("duels:5", 5, duels, samples); met {
		t.Fatal("5 duels met with 3")
	}
}
