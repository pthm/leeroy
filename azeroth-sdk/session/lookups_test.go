package session

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func lookupResult(t *testing.T, b *Bot, h map[string]any) map[string]any {
	t.Helper()
	if h["pending"] != true || h["ok"] != nil {
		t.Fatalf("not a request: %v", h)
	}
	return b.lua.Reqs.Result(h["id"].(int))
}

func TestLookupCachedMissingAndUnavailable(t *testing.T) {
	b := testBot()
	defer b.lua.Close()
	const entry, quest = 999001, 999002
	items.Lock()
	items.proto[entry] = &itemProto{Entry: entry, Name: "Test item"}
	items.Unlock()
	quests.Lock()
	quests.proto[quest] = &questProto{title: "Test quest"}
	quests.Unlock()
	t.Cleanup(func() {
		items.Lock()
		delete(items.proto, entry)
		items.Unlock()
		quests.Lock()
		delete(quests.proto, quest)
		quests.Unlock()
	})
	if r := lookupResult(t, b, b.itemLookup(entry)); r["ok"] != true || r["data"].(types.Item).Name != "Test item" {
		t.Fatalf("cached item: %v", r)
	}
	if r := lookupResult(t, b, b.questLookup(quest)); r["ok"] != true || r["data"].(types.QuestInfo).Title != "Test quest" {
		t.Fatalf("cached quest: %v", r)
	}
	parseItemQuery(binary.LittleEndian.AppendUint32(nil, entry|0x80000000))
	parseQuestQuery(binary.LittleEndian.AppendUint32(nil, quest|0x80000000))
	for _, h := range []map[string]any{b.itemLookup(entry), b.questLookup(quest)} {
		if r := lookupResult(t, b, h); r["error"].(map[string]any)["code"] != "not_found" {
			t.Fatalf("negative response: %v", r)
		}
	}
	items.Lock()
	delete(items.proto, entry)
	items.Unlock()
	quests.Lock()
	delete(quests.proto, quest)
	quests.Unlock()
	for _, h := range []map[string]any{b.itemLookup(entry), b.questLookup(quest)} {
		if r := lookupResult(t, b, h); r["error"].(map[string]any)["code"] != "not_available" {
			t.Fatalf("disconnected: %v", r)
		}
	}
}

func TestLookupWaitsForEvidenceAndTimesOut(t *testing.T) {
	b := testBot()
	defer b.lua.Close()
	var response *script.Outcome
	sent := 0
	h := b.lookup("test.lookup", nil, func() *script.Outcome { return response }, func() error { sent++; return nil })
	b.lua.Reqs.Poll()
	if r := lookupResult(t, b, h); r["pending"] != true || sent != 1 {
		t.Fatalf("premature completion: %v", r)
	}
	response = script.Ok([]uint32{})
	b.lua.Reqs.Poll()
	if r := lookupResult(t, b, h); r["ok"] != true {
		t.Fatalf("confirmed empty result: %v", r)
	}
	h = b.lookup("test.lookup", nil, func() *script.Outcome { return nil }, func() error { return nil })
	time.Sleep(5050 * time.Millisecond)
	b.lua.Reqs.Poll()
	if r := lookupResult(t, b, h); r["error"].(map[string]any)["code"] != "timeout" {
		t.Fatalf("deadline: %v", r)
	}
	h = b.lookup("test.lookup", nil, func() *script.Outcome { return nil }, func() error { return errors.New("offline") })
	if r := lookupResult(t, b, h); r["error"].(map[string]any)["code"] != "not_available" {
		t.Fatalf("send error: %v", r)
	}
}

func TestAchievementsRequireCompleteSnapshot(t *testing.T) {
	s := &agentState{earned: map[uint32]bool{}, criteria: map[uint32]uint64{}}
	if s.achievements() != nil {
		t.Fatal("unloaded interpreted as empty")
	}
	packet := binary.LittleEndian.AppendUint32(packGUID(42), 0xFFFFFFFF)
	s.onPacket(42, smsgInspectAchieves, packet)
	if s.achievements() != nil {
		t.Fatal("truncated response accepted")
	}
	packet = binary.LittleEndian.AppendUint32(packet, 0xFFFFFFFF)
	s.onPacket(42, smsgInspectAchieves, packet)
	if o := s.achievements(); o == nil || !o.OK || len(o.Data.(types.Achievements).Earned) != 0 {
		t.Fatalf("empty confirmed snapshot: %+v", o)
	}
	s.onPacket(42, smsgAchievementEarned, binary.LittleEndian.AppendUint32(packGUID(42), 6))
	if o := s.achievements(); len(o.Data.(types.Achievements).Earned) != 1 {
		t.Fatal("cached snapshot stopped updating")
	}
}

func TestTruncatedTemplatesNotCached(t *testing.T) {
	const id = 999003
	parseItemQuery(binary.LittleEndian.AppendUint32(nil, id))
	parseQuestQuery(binary.LittleEndian.AppendUint32(nil, id))
	items.Lock()
	_, item := items.proto[id]
	items.Unlock()
	quests.Lock()
	_, quest := quests.proto[id]
	quests.Unlock()
	if item || quest {
		t.Fatal("truncated packet became a successful template")
	}
}

func TestCorpseReplyDistinguishesMissingAndMalformed(t *testing.T) {
	s := &agentState{}
	for _, packet := range [][]byte{nil, {1}, {1, 0, 0, 0, 0}} {
		s.onPacket(42, msgCorpseQuery, packet)
		if s.corpseKnown {
			t.Fatal("malformed corpse reply became evidence")
		}
	}
	s.onPacket(42, msgCorpseQuery, []byte{0})
	if !s.corpseKnown || s.corpse != nil {
		t.Fatal("explicit absence lost")
	}
	packet := append([]byte{1}, make([]byte, 20)...)
	s.onPacket(42, msgCorpseQuery, packet)
	if s.corpse == nil || len(s.corpse) != 5 {
		t.Fatal("corpse at origin lost")
	}
	packet[17] = 36 // the corpse's own map: inside the Deadmines, reported at its entrance on map 0
	s.onPacket(42, msgCorpseQuery, packet)
	if s.corpse[3] != 0 || s.corpse[4] != 36 {
		t.Fatalf("instance corpse map lost: %v", s.corpse)
	}
	s.onPacket(42, msgCorpseQuery, []byte{0})
	if s.corpse != nil {
		t.Fatal("old corpse survived explicit absence")
	}
}
