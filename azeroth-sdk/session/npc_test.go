package session

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func TestGossipRequiresObservedWindow(t *testing.T) {
	b := testBot()
	defer b.lua.Close()
	b.npc = &npcState{texts: map[uint32]string{}}
	asked := time.Now().Add(-2 * time.Second)
	check := b.gossipAfter(42, asked, false)
	if check() != nil {
		t.Fatal("elapsed time became success")
	}
	b.npc.setWindow(43, "vendor")
	if check() != nil {
		t.Fatal("another NPC satisfied interaction")
	}
	for _, kind := range []string{"vendor", "trainer", "flight"} {
		b.npc.setWindow(42, kind)
		o := check()
		if o == nil || !o.OK || o.Data.(types.GossipWindow).Kind != kind {
			t.Fatalf("window %s: %+v", kind, o)
		}
	}
	b.npc.setMenuKind(types.Gossip{NPC: 42, Text: "A quest"}, "quest")
	if o := check(); o.Data.(types.GossipWindow).Kind != "quest" || o.Data.(types.GossipWindow).Value.Text != "A quest" {
		t.Fatalf("quest: %+v", o)
	}
	b.npc.onPacket(smsgGossipComplete, nil)
	if o := check(); o.OK || o.Code != "not_available" {
		t.Fatalf("closed open: %+v", o)
	}
	if o := b.gossipAfter(42, asked, true)(); !o.OK || o.Data.(types.GossipWindow).Kind != "closed" {
		t.Fatalf("closed selection: %+v", o)
	}
	if b.gossipAfter(42, time.Now(), true)() != nil {
		t.Fatal("stale close satisfied new interaction")
	}
}

// Packets laid out as AzerothCore writes them (PlayerMenu::SendGossipMenu, WorldPackets::NPC::TrainerList).
func TestNPCPackets(t *testing.T) {
	le := binary.LittleEndian
	s := &npcState{trainers: map[uint64]types.Trainer{}, trainAt: map[uint64]time.Time{}, texts: map[uint32]string{},
		status: map[uint64]uint8{}}

	g := le.AppendUint64(nil, 0xF130000000000042)
	g = le.AppendUint32(g, 7)   // menu
	g = le.AppendUint32(g, 900) // text
	g = le.AppendUint32(g, 1)   // one option
	g = le.AppendUint32(g, 3)
	g = append(g, 5, 0)       // icon interact, not coded
	g = le.AppendUint32(g, 0) // money
	g = append(g, "Make this inn your home.\x00\x00"...)
	g = le.AppendUint32(g, 1)  // one quest
	g = le.AppendUint32(g, 33) // quest
	g = le.AppendUint32(g, 2)  // icon
	g = le.AppendUint32(g, 5)  // level
	g = le.AppendUint32(g, 0)  // flags
	g = append(g, 0)           // repeatable
	g = append(g, "Wolves Across the Border\x00"...)
	s.onPacket(smsgGossipMessage, g)
	m := s.menu
	if m == nil || m.Menu != 7 || len(m.Options) != 1 || m.Options[0].Index != 3 || m.Options[0].Icon != "interact" ||
		m.Options[0].Text != "Make this inn your home." || len(m.Quests) != 1 || m.Quests[0].Quest != 33 || m.Quests[0].Title != "Wolves Across the Border" {
		t.Fatalf("gossip: %+v", m)
	}
	s.onPacket(smsgGossipComplete, nil)
	if s.menu != nil {
		t.Fatal("gossip complete should close the menu")
	}

	st := le.AppendUint32(nil, 2) // SMSG_QUESTGIVER_STATUS_MULTIPLE
	st = append(le.AppendUint64(st, 0x10), 8)
	st = append(le.AppendUint64(st, 0x11), 10)
	s.onPacket(smsgQuestgiverStatusMulti, st)
	if s.questStatus(0x10) != "available" || s.questStatus(0x11) != "reward" || s.questStatus(0x12) != "" {
		t.Fatalf("status: %v", s.status)
	}

	ql := append(le.AppendUint64(nil, 0x10), "Hail, $N.\x00"...) // SMSG_QUESTGIVER_QUEST_LIST
	ql = le.AppendUint32(le.AppendUint32(ql, 0), 0)
	ql = append(ql, 1)
	ql = le.AppendUint32(le.AppendUint32(le.AppendUint32(le.AppendUint32(ql, 7), 2), 3), 0)
	ql = append(append(ql, 0), "A Threat Within\x00"...)
	s.onPacket(smsgQuestList, ql)
	if m := s.menu; m == nil || m.Text != "Hail, $N." || len(m.Quests) != 1 || m.Quests[0].Quest != 7 || m.Quests[0].Title != "A Threat Within" {
		t.Fatalf("quest list: %+v", m)
	}

	nt := le.AppendUint32(nil, 900) // SMSG_NPC_TEXT_UPDATE: 8 options, the likeliest non-empty wins
	for i, txt := range []string{"", "Rare.", "Usual."} {
		nt = le.AppendUint32(nt, math.Float32bits([]float32{0, 10, 90}[i]))
		nt = append(append(nt, txt+"\x00"...), txt+"\x00"...)
		nt = append(nt, make([]byte, 4+24)...)
	}
	for range 5 {
		nt = append(nt, make([]byte, 4)...)
		nt = append(nt, 0, 0)
		nt = append(nt, make([]byte, 4+24)...)
	}
	s.onPacket(smsgNPCTextUpdate, nt)
	if s.texts[900] != "Usual." {
		t.Fatalf("npc text: %q", s.texts[900])
	}

	tr := le.AppendUint64(nil, 0x42)
	tr = le.AppendUint32(tr, 0) // class trainer
	tr = le.AppendUint32(tr, 2)
	for _, sp := range [][3]uint32{{6673, 2, 10}, {100, 1, 4}} { // spell, state, level
		tr = le.AppendUint32(tr, sp[0])
		tr = append(tr, byte(sp[1]))
		tr = le.AppendUint32(tr, 95)        // cost
		tr = append(tr, make([]byte, 8)...) // point costs
		tr = append(tr, byte(sp[2]))        // level
		tr = append(tr, make([]byte, 8)...) // skill line, rank
		tr = le.AppendUint32(tr, sp[0]-1)   // requires
		tr = append(tr, make([]byte, 8)...)
	}
	tr = append(tr, "Hello\x00"...)
	s.onPacket(smsgTrainerList, tr)
	got := s.trainers[0x42]
	if len(got.Spells) != 2 || got.Spells[0].State != "known" || got.Spells[1].Spell != 100 || got.Spells[1].State != "unavailable" ||
		got.Spells[1].Level != 4 || got.Spells[1].Cost != 95 || len(got.Spells[1].Requires) != 1 || got.Greeting != "Hello" {
		t.Fatalf("trainer: %+v", got)
	}
}
