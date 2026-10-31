package session

import (
	"encoding/binary"
	"sort"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// lookup preserves one request shape for cached and asynchronous answers. A transport refusal is different
// from an explicit negative server answer and from a server that never answers.
func (b *Bot) lookup(op string, args map[string]any, check func() *script.Outcome, send func() error) map[string]any {
	if o := check(); o != nil {
		return b.lua.Reqs.Now(script.Spec{Op: op, Args: args}, o)
	}
	if err := send(); err != nil {
		return b.refuse(op, args, "not_available", "the server connection could not send the lookup")
	}
	return b.request(op, args, 5*time.Second, check, nil)
}

func (b *Bot) itemLookup(entry uint32) map[string]any {
	return b.lookup("items.info", map[string]any{"item_entry": entry}, func() *script.Outcome {
		items.Lock()
		p, known := items.proto[entry]
		items.Unlock()
		if !known {
			return nil
		}
		if p == nil {
			return script.Fail("not_found", "the server has no item with this entry", nil)
		}
		it := itemTemplate(b.world, entry)
		it.Description, it.Readable = p.desc, p.pageText != 0
		for _, sp := range p.spells {
			if e := itemEffect(sp[0], sp[1]); e != "" {
				it.Effects = append(it.Effects, e)
			}
		}
		return script.Ok(it)
	}, func() error {
		return b.world.SendPacketRaw(cmsgItemQuery, binary.LittleEndian.AppendUint32(nil, entry))
	})
}

func (b *Bot) questLookup(id uint32) map[string]any {
	return b.lookup("quests.info", map[string]any{"quest_id": id}, func() *script.Outcome {
		quests.Lock()
		p, known := quests.proto[id]
		quests.Unlock()
		if !known {
			return nil
		}
		if p == nil {
			return script.Fail("not_found", "the server has no quest with this ID", nil)
		}
		q := types.QuestInfo{ID: id, Title: p.title, Level: p.level, MinLevel: p.minLevel, Summary: b.gameText(p.summary),
			Details: b.gameText(p.details), Completed: b.gameText(p.completed), Money: p.money, Spell: p.rewSpell, NextQuest: p.next}
		for _, o := range p.objectives {
			if o != "" {
				q.Objectives = append(q.Objectives, b.gameText(o))
			}
		}
		rewards := func(list [][2]uint32) (out []types.Item) {
			for _, it := range list {
				if it[0] != 0 {
					t := itemTemplate(b.world, it[0])
					t.Count = it[1]
					out = append(out, t)
				}
			}
			return out
		}
		q.Rewards, q.Choices = rewards(p.rewards[:]), rewards(p.choices[:])
		return script.Ok(q)
	}, func() error { return b.world.SendPacketRaw(cmsgQuestQuery, binary.LittleEndian.AppendUint32(nil, id)) })
}

func (s *agentState) achievements() *script.Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.achLoaded {
		return nil
	}
	out := types.Achievements{Earned: []uint32{}, Criteria: [][2]uint64{}}
	for id := range s.earned {
		out.Earned = append(out.Earned, id)
	}
	for id, n := range s.criteria {
		out.Criteria = append(out.Criteria, [2]uint64{uint64(id), n})
	}
	sort.Slice(out.Earned, func(i, j int) bool { return out.Earned[i] < out.Earned[j] })
	sort.Slice(out.Criteria, func(i, j int) bool { return out.Criteria[i][0] < out.Criteria[j][0] })
	return script.Ok(out)
}
