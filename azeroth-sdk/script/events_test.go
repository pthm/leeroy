package script

import (
	"testing"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func TestMatchesKind(t *testing.T) {
	npc := Event{Type: "chat", Data: types.Chat{Kind: "npc_say"}}
	whisper := Event{Type: "chat", Data: map[string]any{"kind": "whisper"}} // as read back from the file
	for _, c := range []struct {
		e    Event
		pats []string
		want bool
	}{
		{npc, []string{"chat"}, true},
		{npc, []string{"chat:whisper", "chat:party"}, false},
		{npc, []string{"chat:npc_*"}, true},
		{whisper, []string{"chat:whisper"}, true},
		{Event{Type: "quest_complete"}, []string{"quest_*"}, true},
		{Event{Type: "death"}, []string{"chat:*"}, false},
	} {
		if Matches(c.e, c.pats) != c.want {
			t.Errorf("Matches(%v, %v) != %v", c.e, c.pats, c.want)
		}
	}
}

// a waiting agent's query skips its own chat, live or read back from the file; other queries keep it
func TestWakeSkipsOwnChat(t *testing.T) {
	b := NewBus("")
	b.Emit("chat", types.Chat{Kind: "say", Text: "mine", Self: true})
	b.Emit("chat", map[string]any{"kind": "say", "text": "mine, from the file", "self": true})
	b.Emit("chat", types.Chat{Kind: "say", Text: "theirs"})
	if n := len(b.Query(Query{Types: []string{"chat:say"}, Wake: true})); n != 1 {
		t.Fatalf("wake: %d events", n)
	}
	if n := len(b.Query(Query{Types: []string{"chat:say"}})); n != 3 {
		t.Fatalf("query: %d events", n)
	}
}
