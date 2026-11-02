package gamelog

import "testing"

func TestLines(t *testing.T) {
	r := Reader{Char: "A"}
	evs := r.Lines(`{"t":1,"type":"state","data":{"m":0,"x":1,"y":2,"hp":5,"tgt":{"id":6,"n":"Kobold","lvl":3}}}
{"t":2,"type":"state","data":{"m":0,"x":1,"y":2,"hp":5,"tgt":{"id":6,"n":"Kobold","lvl":3}}}
{"t":3,"type":"state","data":{"m":0,"x":1,"y":2,"hp":5}}
{"seq":1,"t":4,"type":"damage","data":{"src":{"guid":"5","name":"A","player":true},"dst":{"guid":"99","name":"Kobold","entry":6,"level":3},"amount":7,"spell":133}}
{"seq":2,"t":5,"type":"chat","data":{"kind":"party","from":"A","text":"pull","self":true}}
{"seq":3,"t":6,"type":"chat","data":{"kind":"party","from":"B","text":"ok"}}
{"seq":4,"t":7,"type":"chat","data":{"kind":"npc_say","from":"Marshal","text":"hi"}}
{"seq":5,"t":8,"type":"chat","data":{"kind":"loot","from":"","text":"You receive loot"}}
{"t":9,"type":"transports","data":[[7,20808,"The Bravery",0,-8650.5,1340.2,1.5]]}
{"seq":6,"t":10,"type":"result","data":{"op":"move.to"}}
`)
	kinds := []string{}
	for _, e := range evs {
		kinds = append(kinds, e["ev"].(string))
	}
	want := []string{"pos", "tgt", "pos", "pos", "tgt", "dmg", "chat", "chat", "chat", "transport"}
	if len(kinds) != len(want) {
		t.Fatalf("kinds %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds %v, want %v", kinds, want)
		}
	}
	if evs[0]["n"] != "A" || evs[0]["x"] != 1.0 || evs[1]["tgt"].(map[string]any)["n"] != "Kobold" || evs[4]["tgt"] != nil {
		t.Fatalf("state and target: %v %v %v", evs[0], evs[1], evs[4])
	}
	d := evs[5]
	if d["src"].(map[string]any)["pl"] != 1.0 || d["src"].(map[string]any)["id"] != 5.0 || d["dst"].(map[string]any)["id"] != 6.0 || d["amount"] != 7.0 {
		t.Fatalf("damage: %v", d)
	}
	if evs[6]["by"] != nil || evs[6]["type"] != 2.0 || evs[7]["by"] != "B" || evs[8]["from"] != "Marshal" || evs[8]["type"] != 12.0 {
		t.Fatalf("chat: own %v, heard %v, npc %v", evs[6], evs[7], evs[8])
	}
	if evs[9]["n"] != "The Bravery" || evs[9]["id"] != 7.0 {
		t.Fatalf("transport: %v", evs[9])
	}
	sc := Samples(`{"t":5,"elapsed_s":0,"value":10,"score":2,"signals":{"x":1}}
{"elapsed_s":15,"value":20}`, "A")
	if len(sc) != 1 || sc[0]["ev"] != "score" || sc[0]["n"] != "A" || sc[0]["signals"] != nil {
		t.Fatalf("samples: %v", sc)
	}
}
