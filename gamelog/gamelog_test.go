package gamelog

import "testing"

func TestLines(t *testing.T) {
	evs := Lines(`{"t":1,"type":"state","data":{"map":0,"x":1,"y":2,"health":{"current":5,"max":9}}}
{"seq":1,"t":4,"type":"damage","data":{"src":{"guid":"5","name":"A","player":true},"amount":7}}
{"t":9,"type":"transports","data":[{"guid":"7","entry":20808}]}
{"t":10,"type":"logout"}
not json
`, "A")
	if len(evs) != 4 {
		t.Fatalf("%d lines: %v", len(evs), evs)
	}
	if s := evs[0]; s["type"] != "state" || s["char"] != "A" || s["t"] != 1.0 || s["x"] != 1.0 || s["health"].(map[string]any)["max"] != 9.0 {
		t.Fatalf("state: %v", s)
	}
	if d := evs[1]; d["amount"] != 7.0 || d["src"].(map[string]any)["name"] != "A" || d["seq"] != nil {
		t.Fatalf("damage: %v", d)
	}
	if tr, _ := evs[2]["data"].([]any); len(tr) != 1 {
		t.Fatalf("transports: %v", evs[2])
	}
	sc := Samples(`{"t":5,"elapsed_s":0,"value":10,"score":2,"signals":{"x":1}}
{"elapsed_s":15,"value":20}`, "A")
	if len(sc) != 1 || sc[0]["type"] != "score" || sc[0]["char"] != "A" || sc[0]["signals"] != nil {
		t.Fatalf("samples: %v", sc)
	}
}
