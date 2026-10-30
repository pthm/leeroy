package session

// World messages: refused portals and instances, reputation, ready checks, the message of the day, summons and
// battleground queues. The hook goes on before login (factions and the MOTD come with it); the API with setupControl.

import (
	"bytes"
	"encoding/binary"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

const (
	smsgTransferAborted    = 0x040
	smsgInitFactions       = 0x122
	smsgSetFactionStanding = 0x124
	smsgAreaTriggerMessage = 0x2B8
	msgRaidReadyCheck      = 0x322
	smsgMotd               = 0x33D
)

type worldMsgs struct {
	mu       sync.Mutex
	factions map[uint32][2]uint32 // reputation list id -> flags, standing (relative to the faction's base)
	motd     []string
}

// TransferAbortReason (Player.h)
var transferAborts = map[uint8][2]string{1: {"error", "transfer failed"}, 2: {"instance_full", "the instance is full"},
	3: {"not_found", "instance not found"}, 4: {"too_many_instances", "you've entered too many instances recently"},
	6: {"encounter_in_progress", "an encounter is in progress"}, 7: {"expansion_required", "that needs a newer expansion"},
	8: {"difficulty_unavailable", "that difficulty isn't available"}, 9: {"unique_message", "you can't leave this place yet"},
	0x0A: {"too_many_realm_instances", "no more instances can be launched right now"}, 0x0B: {"need_group", "you need a group"},
	0x0C: {"not_found", "instance not found"}, 0x0D: {"not_found", "instance not found"}, 0x0E: {"not_found", "instance not found"},
	0x0F: {"realm_only", "everyone in the party must be from this realm"}, 0x10: {"map_not_allowed", "that map can't be entered now"}}

func (b *Bot) watchWorldMessages() *worldMsgs {
	m := &worldMsgs{factions: map[uint32][2]uint32{}}
	b.world.AddPacketHook(func(op uint16, data []byte) { m.onPacket(b, op, data) })
	return m
}

func (m *worldMsgs) onPacket(b *Bot, op uint16, data []byte) {
	r := bytes.NewReader(data)
	u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
	switch op {
	case smsgTransferAborted: // map, reason, [arg]
		mapID := u32()
		reason, _ := r.ReadByte()
		t, ok := transferAborts[reason]
		if !ok {
			t = [2]string{"error", "transfer aborted"}
		}
		b.emit("travel_refused", types.TravelRefused{Map: mapID, Reason: t[0], Message: t[1]})
	case smsgAreaTriggerMessage: // length, text: a portal's requirement ("You must be level 55...")
		u32()
		b.emit("travel_refused", types.TravelRefused{Reason: "requirement", Message: readCString(r)})
	case smsgInitFactions: // count (128), then per reputation list id: flags u8, standing u32
		n := u32()
		m.mu.Lock()
		for i := uint32(0); i < n && r.Len() >= 5; i++ {
			fl, _ := r.ReadByte()
			m.factions[i] = [2]uint32{uint32(fl), u32()}
		}
		m.mu.Unlock()
	case smsgSetFactionStanding: // refer-a-friend bonus f32, show visual u8, count, then reputation list id, standing
		u32()
		r.ReadByte()
		n := u32()
		m.mu.Lock()
		for i := uint32(0); i < n && r.Len() >= 8; i++ {
			id, st := u32(), u32()
			m.factions[id] = [2]uint32{m.factions[id][0] | 1, st} // a change makes it visible
		}
		m.mu.Unlock()
	case smsgMotd: // line count, lines
		n := u32()
		var lines []string
		for i := uint32(0); i < n && r.Len() > 0; i++ {
			lines = append(lines, readCString(r))
		}
		m.mu.Lock()
		m.motd = lines
		m.mu.Unlock()
		b.emit("motd", types.Notice{Message: strings.Join(lines, "\n")})
	case msgRaidReadyCheck: // the leader's guid
		if g := uint64(u32()) | uint64(u32())<<32; g != b.world.CharGUID() {
			b.emit("ready_check", types.ReadyCheck{From: b.ref(g)})
		}
	}
}

type factionRec struct {
	rec  []byte // Faction.dbc (DBCStructure.h FactionEntry)
	name string
}

var (
	factionOnce sync.Once
	factionRecs map[uint32]factionRec // reputation list id -> faction
)

// reputation: visible factions, absolute standing (ReputationMgr::GetReputation: the base for your race and class
// plus what the server sends).
func (m *worldMsgs) reputation(race, class uint8) []types.Reputation {
	factionOnce.Do(func() {
		factionRecs = map[uint32]factionRec{}
		b := dbcFile("Faction.dbc")
		if b == nil {
			return
		}
		le := binary.LittleEndian
		n, rs := le.Uint32(b[4:]), le.Uint32(b[12:])
		strs := b[20+n*rs:]
		for i := uint32(0); i < n; i++ {
			rec := b[20+i*rs : 20+(i+1)*rs]
			if l := int32(le.Uint32(rec[4:])); l >= 0 {
				f := factionRec{rec: rec}
				if off := le.Uint32(rec[23*4:]); off < uint32(len(strs)) { // name, enUS
					f.name = string(bytes.SplitN(strs[off:], []byte{0}, 2)[0])
				}
				factionRecs[uint32(l)] = f
			}
		}
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []types.Reputation{}
	for id, f := range m.factions {
		fr, ok := factionRecs[id]
		if f[0]&1 == 0 || f[0]&(4|8) != 0 || !ok {
			continue
		}
		rec := fr.rec
		u := func(c int) uint32 { return binary.LittleEndian.Uint32(rec[c*4:]) }
		v := int32(f[1])
		for i := 0; i < 4; i++ { // ReputationMgr::GetBaseReputation
			rm, cm := u(2+i), u(6+i)
			if (rm&(1<<(race-1)) != 0 || (rm == 0 && cm != 0)) && (cm&(1<<(class-1)) != 0 || cm == 0) {
				v += int32(u(10 + i))
				break
			}
		}
		out = append(out, types.Reputation{Faction: u(0), Name: fr.name, Standing: v, Rank: repRank(v), AtWar: f[0]&2 != 0})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func repRank(v int32) string {
	for _, r := range []struct {
		min  int32
		name string
	}{{42000, "exalted"}, {21000, "revered"}, {9000, "honored"}, {3000, "friendly"}, {0, "neutral"}, {-3000, "unfriendly"}, {-6000, "hostile"}} {
		if v >= r.min {
			return r.name
		}
	}
	return "hated"
}

var bgStatus = map[uint32]string{client.BattlegroundStatusNone: "none", client.BattlegroundStatusWaitQueue: "queued",
	client.BattlegroundStatusWaitJoin: "invited", client.BattlegroundStatusInProgress: "in_progress", client.BattlegroundStatusWaitLeave: "leaving"}

func (b *Bot) setupWorldMessages() {
	w, e, m := b.world, b.lua, b.wm
	m.mu.Lock()
	if m.motd != nil { // it came with the login, before the API was up
		b.emit("motd", types.Notice{Message: strings.Join(m.motd, "\n")})
	}
	m.mu.Unlock()
	e.Func("me.reputation", "() → Reputation[]", "your standing with each faction in your reputation list, by name", func() []types.Reputation {
		return m.reputation(b.GetRace(), b.GetClass())
	})
	e.Func("group.ready", "([ready = true]) → Request", "answer a ready check (ready_check event)", func(ready any) map[string]any {
		if !w.InGroup() {
			return b.refuse("group.ready", nil, "not_in_group", "you aren't in a group")
		}
		v := byte(1)
		if ready == false {
			v = 0
		}
		return b.sent("group.ready", nil, w.SendPacketRaw(msgRaidReadyCheck, []byte{v}))
	})

	w.AddSummonRequestHook(func(s client.SummonRequest) {
		b.emit("summon_request", types.SummonRequest{From: b.ref(s.SummonerGUID), Zone: areaNames()[s.ZoneID],
			SecondsLeft: float64(s.AutoDecline) / 1000})
	})
	summon := func(op string, agree bool) map[string]any {
		s := w.PendingSummon()
		if s.SummonerGUID == 0 || time.Since(s.ReceivedAt) > time.Duration(s.AutoDecline)*time.Millisecond {
			return b.refuse(op, nil, "no_summon", "no summon is pending (summon_request event; they expire after 2 minutes)")
		}
		if agree && (!b.IsAlive() || b.InCombat()) { // the server ignores the answer then
			return b.refuse(op, nil, "busy", "dead or in combat: the server ignores the answer")
		}
		if err := w.SummonResponse(s.SummonerGUID, agree); err != nil {
			return b.refuse(op, nil, "failed", err.Error())
		}
		w.ClearPendingSummon()
		if !agree {
			return b.done(op, nil, nil)
		}
		return b.request(op, nil, 15*time.Second, nil, func(ev script.Event) *script.Outcome {
			switch d := ev.Data.(type) {
			case types.Teleport:
				return script.Ok(d)
			case types.TravelRefused:
				return script.Fail(d.Reason, d.Message, nil)
			}
			return nil
		})
	}
	e.Func("summon.accept", "() → Request<Teleport>", "accept a pending summon (summon_request event): resolves when you arrive", func() map[string]any {
		return summon("summon.accept", true)
	})
	e.Func("summon.decline", "() → Request", "decline a pending summon", func() map[string]any { return summon("summon.decline", false) })

	w.AddBattlefieldStatusHook(func(s client.BattlefieldStatus) {
		bg := types.Battleground{Slot: s.QueueSlot, Status: bgStatus[s.Status], Type: s.BgTypeID, Map: s.MapID, Arena: uint8(s.ArenaType), Rated: s.IsRated}
		if s.Status == client.BattlegroundStatusWaitJoin {
			bg.SecondsLeft = math.Round(float64(s.TimeToRemove)/100) / 10
		}
		b.emit("battleground", bg)
	})
}
