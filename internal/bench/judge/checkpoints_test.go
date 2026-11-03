package judge

import (
	"encoding/json"
	"testing"

	"github.com/pthm/leeroy/azsdk/metrics"
	"github.com/pthm/leeroy/internal/bench/config"
)

func TestCheckpoints(t *testing.T) {
	cps := []config.Checkpoint{{Name: "Rhahk'Zor", Kill: 644}, {Name: "Sneed", Kill: 643}, {Name: "Edwin VanCleef", Kill: 639}}
	lines := []string{
		`{"t":61000,"type":"kill","data":{"victim":{"guid":"1","name":"Defias Thug","entry":4418}}}`,
		`{"t":120400,"type":"party_kill","data":{"victim":{"guid":"2","name":"Rhahk'Zor","entry":644},"killer":{"guid":"3","name":"A","player":true}}}`,
		`{"t":300000,"type":"damage","data":{}}`,
		`{"t":900000,"type":"kill","data":{"victim":{"guid":"4","name":"Rhahk'Zor","entry":644}}}`, // a second one: the first counts
		`{"t":960000,"type":"kill","data":{"victim":{"guid":"5","name":"Sneed","entry":643}}}`,
	}
	evs := make([]metrics.Event, 0, len(lines))
	for _, l := range lines {
		e, ok := metrics.Decode([]byte(l))
		if !ok {
			t.Fatalf("decode %s", l)
		}
		evs = append(evs, e)
	}
	reached := map[string]float64{}
	clock := []record{{T: 1000, ElapsedS: 0}, {T: 1_000_000, ElapsedS: 999}} // the run's clock started at t 1000
	got, _ := json.Marshal(checkpoints(cps, evs, clock, reached))
	want := `[{"name":"Rhahk'Zor","at_s":119.4},{"name":"Sneed","at_s":959},{"name":"Edwin VanCleef","at_s":null}]`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// a resumed judge keeps what it had: an earlier time isn't overwritten by events read again
	reached = map[string]float64{"Rhahk'Zor": 50}
	if got, _ := json.Marshal(checkpoints(cps[:1], evs, clock, reached)); string(got) != `[{"name":"Rhahk'Zor","at_s":50}]` {
		t.Fatalf("resumed: %s", got)
	}
}

func TestZoneCheckpoints(t *testing.T) {
	cps := []config.Checkpoint{{Name: "Stormwind City", Zone: 1519}, {Name: "Ironforge", Zone: 1537}, {Name: "Darnassus", Zone: 1657}}
	lines := []string{
		`{"t":31000,"type":"area_changed","data":{"zone":"Stormwind City","area":"Trade District","zone_id":1519,"area_id":5150}}`,
		`{"t":91000,"type":"area_changed","data":{"zone":"Ironforge","area":"","zone_id":1537,"area_id":1537}}`,
		`{"t":151000,"type":"area_changed","data":{"zone":"Stormwind City","area":"","zone_id":1519,"area_id":1519}}`, // back again: the first counts
	}
	evs := make([]metrics.Event, 0, len(lines))
	for _, l := range lines {
		e, ok := metrics.Decode([]byte(l))
		if !ok {
			t.Fatalf("decode %s", l)
		}
		evs = append(evs, e)
	}
	reached := map[string]float64{}
	clock := []record{{T: 1000, ElapsedS: 0}, {T: 1_000_000, ElapsedS: 999}}
	got, _ := json.Marshal(checkpoints(cps, evs, clock, reached))
	if want := `[{"name":"Stormwind City","at_s":30},{"name":"Ironforge","at_s":90},{"name":"Darnassus","at_s":null}]`; string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if met, _ := allReached(reached, len(cps)); met {
		t.Fatal("met with Darnassus unvisited")
	}
	if met, at := allReached(reached, 2); !met || at != 90 {
		t.Fatalf("all of two: met %v at %v, want at 90", met, at)
	}
}

func TestCheckpointScore(t *testing.T) {
	// two bosses of five on a two-hour clock: 2/5 of finishing as it runs out (0.5)
	if s := checkpointScore(2, 5, 7200); s != 0.2 {
		t.Fatalf("2/5: %v", s)
	}
	// all but the last still scores below any finish
	if checkpointScore(4, 5, 7200) >= timeScore(true, 7199) || checkpointScore(0, 5, 7200) != 0 || checkpointScore(1, 0, 7200) != 0 {
		t.Fatal("checkpointScore bounds")
	}
}

func TestDuelsOf(t *testing.T) {
	lines := []string{
		`{"t":11000,"type":"duel_ended","data":{"state":"ended","opponent":"B","result":"beaten","winner":"A","loser":"B","won":true}}`,
		`{"t":20000,"type":"duel_ended","data":{"state":"ended","opponent":"B","result":"cancelled"}}`, // declined: not counted
		`{"t":61000,"type":"duel_ended","data":{"state":"ended","opponent":"B","result":"fled","winner":"B","loser":"A"}}`,
	}
	evs := make([]metrics.Event, 0, len(lines))
	for _, l := range lines {
		e, ok := metrics.Decode([]byte(l))
		if !ok {
			t.Fatalf("decode %s", l)
		}
		evs = append(evs, e)
	}
	clock := []record{{T: 1000, ElapsedS: 0}, {T: 1_000_000, ElapsedS: 999}}
	out, won := duelsOf(evs, clock, 5)
	got, _ := json.Marshal(out)
	if want := `[{"at_s":10,"opponent":"B","won":true,"result":"beaten"},{"at_s":60,"opponent":"B","won":false,"result":"fled"}]`; string(got) != want || won != 1 {
		t.Fatalf("got %s (won %d)\nwant %s", got, won, want)
	}
	if out, won := duelsOf(evs, clock, 1); len(out) != 1 || won != 1 { // a goal of 1: the second duel doesn't count
		t.Fatalf("capped at 1: %d duels, %d won", len(out), won)
	}
}
