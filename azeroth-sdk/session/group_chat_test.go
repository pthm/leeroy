package session

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func testBot() *Bot {
	return &Bot{lua: script.NewEngine(func(string, ...any) {}, nil), world: client.NewWorldClient("t", nil, func(string, ...any) {})}
}

func TestInventoryCapacityCodeIsCanonical(t *testing.T) {
	code, _ := inventoryError([]byte{50})
	if code != "bags_full" {
		t.Fatal(code)
	}
}

// SMSG_PARTY_COMMAND_RESULT (WorldSession::SendPartyResult), SMSG_CHANNEL_NOTIFY (Channel::MakeNotifyPacket,
// ChannelMgr::MakeNotOnPacket), SMSG_CHAT_PLAYER_NOT_FOUND and SMSG_MESSAGECHAT (ChatHandler::BuildChatPacket).
func TestGroupChatPackets(t *testing.T) {
	le := binary.LittleEndian
	b := testBot()
	c := &controlState{watched: map[uint16]bool{}, names: map[uint64]string{}, acks: map[string]bool{}}
	party := func(op uint32, name string, res uint32) []byte {
		return le.AppendUint32(le.AppendUint32(append(le.AppendUint32(nil, op), name+"\x00"...), res), 0)
	}
	c.onPacket(b, smsgPartyResult, party(0, "Bob", 0)) // the invite went out
	c.onPacket(b, smsgPartyResult, party(0, "", 0))    // a raid conversion: no invite
	c.onPacket(b, smsgPartyResult, party(2, "Me", 0))  // left
	c.onPacket(b, smsgPartyResult, party(0, "", 4))    // accept: group full
	if !c.acks["invite\x00bob"] || len(c.acks) != 1 {
		t.Fatalf("acks: %v", c.acks)
	}

	notify := func(typ byte, ch string, rest ...byte) []byte {
		return append(append([]byte{typ}, ch+"\x00"...), rest...)
	}
	c.onPacket(b, smsgChannelNotify, notify(2, "Trade - City", 0x3C, 2, 0, 0, 0, 0, 0, 0, 0)) // you joined: flags, id, 0
	c.onPacket(b, smsgChannelNotify, notify(4, "secret"))                                     // wrong password
	c.onPacket(b, smsgChannelNotify, notify(5, "nowhere"))                                    // not on it
	c.onPacket(b, smsgChannelNotify, notify(0, "Trade - City", make([]byte, 8)...))           // someone joined: nothing
	c.onPacket(b, smsgChatNotFound, []byte("Bobb\x00"))
	if !c.acks["join\x00trade - city"] {
		t.Fatalf("join: %v", c.acks)
	}

	chat := func(typ uint8, sender uint64, channel, text string) []byte {
		p := le.AppendUint64(le.AppendUint32([]byte{typ}, 0), sender)
		p = le.AppendUint32(p, 0)
		if channel != "" {
			p = append(p, channel+"\x00"...)
		}
		p = le.AppendUint64(p, sender)
		return append(append(le.AppendUint32(p, uint32(len(text)+1)), text+"\x00"...), 0)
	}
	c.onPacket(b, smsgMessageChat, chat(9, 0x22, "", "hi there")) // WHISPER_INFORM: from the receiver
	c.onPacket(b, smsgMessageChat, chat(0x11, b.world.CharGUID(), "Trade - City", "wts"))
	if !c.acks["whisper\x00hi there"] || !c.acks["channel\x00trade - city\x00wts"] {
		t.Fatalf("echoes: %v", c.acks)
	}

	var got []string
	for _, ev := range b.lua.Bus.Wait(script.Query{Types: []string{"error"}}, time.Second) {
		e := ev.Data.(types.ErrorEvent)
		got = append(got, e.Op+" "+e.Code)
	}
	want := "group group_full,chat.join wrong_password,chat.channel not_member,chat.whisper player_not_found"
	if strings.Join(got, ",") != want {
		t.Fatalf("errors: %v", got)
	}

	if chatKind(0x33) != "party" || chatKind(0x27) != "raid" || chatKind(0x28) != "raid_warning" || chatKind(0x2D) != "battleground" {
		t.Fatal("leader chat kinds")
	}
	// BG_SYSTEM_*: a non-player receiver's name before the text
	bg := le.AppendUint64(le.AppendUint32([]byte{0x25}, 0), 0)
	bg = le.AppendUint64(le.AppendUint32(bg, 0), 0xF130000000000001)
	bg = append(le.AppendUint32(bg, 5), "Flag\x00"...)
	bg = append(append(le.AppendUint32(bg, 9), "captured\x00"...), 0)
	if m, _, ok := parseChat(smsgMessageChat, bg); !ok || m.Text != "captured" || chatKind(m.Type) != "battleground_system" {
		t.Fatalf("bg system: %+v", m)
	}
}

// SMSG_PARTY_MEMBER_STATS_FULL writes each aura's flags; SMSG_PARTY_MEMBER_STATS always writes 1
// (WorldSession::BuildPartyMemberStatsChangedPacket).
func TestPartyStatsAuras(t *testing.T) {
	le := binary.LittleEndian
	c := &controlState{members: map[uint64]*memberStats{}}
	auras := func(full bool, slots map[uint8][2]uint32) []byte {
		p := []byte{}
		if full {
			p = append(p, 0)
		}
		p = le.AppendUint32(append(p, 1, 0x42), 0x200) // packed guid 0x42, mask: auras only
		var m uint64
		for s := range slots {
			m |= 1 << s
		}
		p = le.AppendUint64(p, m)
		for s := uint8(0); s < 64; s++ {
			if a, ok := slots[s]; ok {
				p = append(le.AppendUint32(p, a[0]), byte(a[1]))
			}
		}
		return p
	}
	c.parsePartyStats(true, auras(true, map[uint8][2]uint32{3: {172, 0x80}, 5: {1243, 0x10}}))
	g, refresh := c.parsePartyStats(false, auras(false, map[uint8][2]uint32{3: {172, 1}, 7: {589, 1}}))
	m := c.members[0x42]
	if g != 0x42 || !refresh || m.auras[3] != [2]uint32{172, 0x80} || m.auras[7] != [2]uint32{589, 0} {
		t.Fatalf("changed: %v refresh %v", m.auras, refresh)
	}
	if _, refresh = c.parsePartyStats(false, auras(false, map[uint8][2]uint32{3: {0, 1}})); refresh || len(m.auras) != 2 {
		t.Fatalf("removed: %v", m.auras)
	}
	c.parsePartyStats(true, auras(true, map[uint8][2]uint32{7: {589, 0x80}}))
	if len(m.auras) != 1 || m.auras[7] != [2]uint32{589, 0x80} {
		t.Fatalf("full: %v", m.auras)
	}
}

// SMSG_TRANSFER_ABORTED (Player::SendTransferAborted), SMSG_AREA_TRIGGER_MESSAGE (WorldSession::SendAreaTriggerMessage),
// the reputation packets (ReputationMgr), SMSG_MOTD (MotdMgr) and MSG_RAID_READY_CHECK.
func TestWorldMessages(t *testing.T) {
	le := binary.LittleEndian
	b := testBot()
	m := &worldMsgs{factions: map[uint32][2]uint32{}}
	m.onPacket(b, smsgTransferAborted, append(le.AppendUint32(nil, 533), 8, 1)) // Naxxramas, difficulty + arg
	msg := "You must be at least level 55 to enter."
	m.onPacket(b, smsgAreaTriggerMessage, append(le.AppendUint32(nil, uint32(len(msg)+1)), msg+"\x00"...))
	init := le.AppendUint32(nil, 128)
	for i := 0; i < 128; i++ {
		init = append(init, 0, 0, 0, 0, 0)
	}
	init[4+5*12] = 0x11 // list id 12: visible, peace forced
	le.PutUint32(init[4+5*12+1:], 1500)
	m.onPacket(b, smsgInitFactions, init)
	set := le.AppendUint32(append(le.AppendUint32(nil, 0), 1), 2)
	set = le.AppendUint32(le.AppendUint32(set, 12), 2500)
	set = le.AppendUint32(le.AppendUint32(set, 40), 0xFFFFFF9C) // -100
	m.onPacket(b, smsgSetFactionStanding, set)
	if m.factions[12] != [2]uint32{0x11, 2500} || m.factions[40] != [2]uint32{1, 0xFFFFFF9C} || len(m.factions) != 128 {
		t.Fatalf("factions: %v %v", m.factions[12], m.factions[40])
	}
	m.onPacket(b, smsgMotd, append(le.AppendUint32(nil, 2), "Welcome\x00to the realm\x00"...))
	m.onPacket(b, msgRaidReadyCheck, le.AppendUint64(nil, 7))

	var got []string
	for _, ev := range b.lua.Bus.Wait(script.Query{Types: []string{"travel_refused", "motd", "ready_check"}}, time.Second) {
		switch d := ev.Data.(type) {
		case types.TravelRefused:
			got = append(got, d.Reason+" "+d.Message)
		case types.Notice:
			got = append(got, d.Message)
		case types.ReadyCheck:
			got = append(got, fmt.Sprintf("ready? %d", d.From.GUID))
		}
	}
	want := []string{"difficulty_unavailable that difficulty isn't available", "requirement " + msg, "Welcome\nto the realm", "ready? 7"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events: %q", got)
	}
	for v, r := range map[int32]string{-42000: "hated", -6000: "hostile", -1: "unfriendly", 2999: "neutral", 3000: "friendly", 20999: "honored", 42999: "exalted"} {
		if repRank(v) != r {
			t.Fatalf("rank %d: %s", v, repRank(v))
		}
	}
}

// ChatHandler cuts a message at its first newline and collapses runs of spaces before echoing it.
func TestChatText(t *testing.T) {
	for in, want := range map[string]string{"hi  there   you": "hi there you", "wts\nline two": "wts", "\rx": "", "ok": "ok"} {
		if got := chatText(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
