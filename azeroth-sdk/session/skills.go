package session

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type skillLine struct {
	name     string
	category uint32
}

// SkillLine.dbc: ID, category, skill-cost ID, then the localized name offsets. Use the first nonempty locale.
func parseSkillLines(data []byte) map[uint32]skillLine {
	out := map[uint32]skillLine{}
	if len(data) < 20 || string(data[:4]) != "WDBC" {
		return out
	}
	le := binary.LittleEndian
	n, rs, size := uint64(le.Uint32(data[4:])), uint64(le.Uint32(data[12:])), uint64(le.Uint32(data[16:]))
	end := 20 + n*rs
	if rs < 19*4 || end > uint64(len(data)) || size > uint64(len(data))-end {
		return out
	}
	strings := data[end : end+size]
	for i := uint64(0); i < n; i++ {
		r := data[20+i*rs : 20+(i+1)*rs]
		name := ""
		for locale := 0; locale < 16; locale++ {
			off := le.Uint32(r[(3+locale)*4:])
			if uint64(off) >= uint64(len(strings)) {
				continue
			}
			if end := bytes.IndexByte(strings[off:], 0); end > 0 {
				name = string(strings[off : uint64(off)+uint64(end)])
				break
			}
		}
		out[le.Uint32(r)] = skillLine{name, le.Uint32(r[4:])}
	}
	return out
}

func skillsOf(me *client.WorldObject, lines map[uint32]skillLine) []types.Skill {
	out := []types.Skill{}
	if me == nil {
		return out
	}
	for i := uint16(0); i < 128; i++ {
		f := fieldSkillInfo + i*3
		id := me.Value(f) & 0xFFFF
		if id == 0 {
			continue
		}
		v := me.Value(f + 1)
		line, known := lines[id]
		out = append(out, types.Skill{ID: id, Name: line.name, Category: line.category, Value: v & 0xFFFF, Max: v >> 16, MetadataKnown: known && line.name != ""})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (b *Bot) setupSkills() {
	data, _ := os.ReadFile(filepath.Join(b.config.DataDir, "dbc", "SkillLine.dbc"))
	lines := parseSkillLines(data)
	list := func() []types.Skill { return skillsOf(b.world.GetObject(b.world.CharGUID()), lines) }
	b.lua.Func("skills.list", "() → Skill[]", "observed learned skills, sorted by ID. Base value and maximum come from character fields, excluding temporary/equipment bonuses, matching character_skills after a server save. Missing DBC labels set metadata_known=false", list)
	b.lua.Func("skills.get", "(skill_id) → Skill?", "one learned skill by ID; nil means it is absent from the observed character skill list. Base values exclude bonuses; missing labels set metadata_known=false", func(id uint32) *types.Skill {
		for _, skill := range list() {
			if skill.ID == id {
				return &skill
			}
		}
		return nil
	})
}
