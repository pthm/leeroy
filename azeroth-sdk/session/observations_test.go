package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pthm/leeroy/azeroth-sdk/client"
)

func TestObservedQuestLogDoesNotInferCompletion(t *testing.T) {
	if q := observedQuests(nil); q.Available || q.Quests == nil {
		t.Fatalf("missing character: %+v", q)
	}
	me := &client.WorldObject{Values: map[uint16]uint32{fieldQuestLog: 62, fieldQuestLog + 2: 2 | 3<<16, fieldQuestLog + 3: 4 | 5<<16}}
	q := observedQuests(me)
	if !q.Available || len(q.Quests) != 1 || q.Quests[0].Complete || q.Quests[0].Counters != [4]uint32{2, 3, 4, 5} {
		t.Fatalf("observed fields: %+v", q)
	}
	me.Values[fieldQuestLog+1] = 1
	if !observedQuests(me).Quests[0].Complete {
		t.Fatal("completion bit missing")
	}
	delete(me.Values, fieldQuestLog)
	if q := observedQuests(me); !q.Available || len(q.Quests) != 0 {
		t.Fatalf("removed quest must be absent, not completed: %+v", q)
	}
}

func TestObservedGroupContainsOnlyRosterIdentifiers(t *testing.T) {
	st := client.GroupState{InGroup: true, GroupGUID: 99, LeaderGUID: 42, Members: []client.GroupMember{{GUID: 43, Name: "private-name"}, {GUID: 42}}}
	g := observedGroup(42, st)
	if !g.InGroup || g.ID != 99 || g.Leader != 42 || len(g.Members) != 2 || g.Members[0] != 42 {
		t.Fatalf("roster: %+v", g)
	}
	b, _ := json.Marshal(g)
	if strings.Contains(string(b), "private-name") {
		t.Fatal("copied name")
	}
	st.InGroup = false
	if g := observedGroup(42, st); g.InGroup || g.ID != 0 || len(g.Members) != 0 {
		t.Fatalf("solo has stale roster: %+v", g)
	}
}
