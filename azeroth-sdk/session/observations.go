package session

import (
	"sort"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// Read fields directly: recording observations must not initiate lookups or copy quest text.
func observedQuests(me *client.WorldObject) types.DiagnosticQuestLog {
	out := types.DiagnosticQuestLog{Available: me != nil, Quests: []types.DiagnosticQuest{}}
	if me == nil {
		return out
	}
	for slot := uint16(0); slot < 25; slot++ {
		f := fieldQuestLog + slot*5
		id := me.Value(f)
		if id == 0 {
			continue
		}
		state, c1, c2 := me.Value(f+1), me.Value(f+2), me.Value(f+3)
		out.Quests = append(out.Quests, types.DiagnosticQuest{ID: id, Complete: state&1 != 0, Failed: state&2 != 0, Counters: [4]uint32{c1 & 0xFFFF, c1 >> 16, c2 & 0xFFFF, c2 >> 16}})
	}
	sort.Slice(out.Quests, func(i, j int) bool { return out.Quests[i].ID < out.Quests[j].ID })
	return out
}

func observedGroup(me uint64, st client.GroupState) types.DiagnosticGroup {
	out := types.DiagnosticGroup{InGroup: st.InGroup, Members: []types.GUID{}}
	if !st.InGroup {
		return out
	}
	out.ID, out.Leader = types.GUID(st.GroupGUID), types.GUID(st.LeaderGUID)
	seen := map[uint64]bool{me: true}
	out.Members = append(out.Members, types.GUID(me))
	for _, m := range st.Members {
		if !seen[m.GUID] && len(out.Members) < 40 {
			out.Members = append(out.Members, types.GUID(m.GUID))
			seen[m.GUID] = true
		}
	}
	sort.Slice(out.Members, func(i, j int) bool { return out.Members[i] < out.Members[j] })
	return out
}
