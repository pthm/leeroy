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
	got, _ := json.Marshal(checkpoints(cps, evs, 1000, reached))
	want := `[{"name":"Rhahk'Zor","at_s":119},{"name":"Sneed","at_s":959},{"name":"Edwin VanCleef","at_s":null}]`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// a resumed judge keeps what it had: an earlier time isn't overwritten by events read again
	reached = map[string]float64{"Rhahk'Zor": 50}
	if got, _ := json.Marshal(checkpoints(cps[:1], evs, 1000, reached)); string(got) != `[{"name":"Rhahk'Zor","at_s":50}]` {
		t.Fatalf("resumed: %s", got)
	}
}
