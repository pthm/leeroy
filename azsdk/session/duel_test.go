package session

import (
	"encoding/binary"
	"slices"
	"testing"
	"time"

	"github.com/pthm/leeroy/azsdk/client"
)

// a duel's end stops your attacks (AzerothCore leaves them going, onto the loser at 1 health): auto-repeat, the
// channel, the cast in progress, melee and the pet
func TestDuelEndStopsFighting(t *testing.T) {
	b := testBot()
	b.agent = &agentState{}
	var sent []uint16
	var channel, cast, pet []byte
	b.world.OnPacketSend = func(op uint16, d []byte) {
		sent = append(sent, op)
		switch op {
		case cmsgCancelChannelling:
			channel = d
		case client.CmsgCancelCast:
			cast = d
		case client.CmsgPetAction:
			pet = d
		}
	}
	b.world.PutObjectForTest(&client.WorldObject{GUID: b.world.CharGUID(), Values: map[uint16]uint32{client.UnitChannelSpell: 5143, 0x08: 77}})
	b.agent.castSpell, b.agent.castUntil = 116, time.Now().Add(time.Second)
	b.stopFighting()
	for _, op := range []uint16{cmsgCancelAutoRepeat, cmsgCancelChannelling, client.CmsgCancelCast, client.CmsgAttackStop, client.CmsgPetAction} {
		if !slices.Contains(sent, op) {
			t.Fatalf("0x%X not sent: %x", op, sent)
		}
	}
	if binary.LittleEndian.Uint32(channel) != 5143 || cast[1] != 116 || binary.LittleEndian.Uint64(pet) != 77 || pet[11] != actCommand || pet[8] != 1 {
		t.Fatalf("channel %x cast %x pet %x", channel, cast, pet)
	}

	sent = nil // nothing going: no channel, cast or pet to stop
	b.world.PutObjectForTest(&client.WorldObject{GUID: b.world.CharGUID(), Values: map[uint16]uint32{}})
	b.agent.castSpell = 0
	b.stopFighting()
	if !slices.Equal(sent, []uint16{cmsgCancelAutoRepeat, client.CmsgAttackStop}) {
		t.Fatalf("sent %x", sent)
	}
}
