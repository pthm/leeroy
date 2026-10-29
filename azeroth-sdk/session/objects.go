package session

// What it takes to use a game object: the lock on chests, gathering nodes and quest objects (Lock.dbc, by the lock
// id in the object's template data from SMSG_GAMEOBJECT_QUERY_RESPONSE), your skill levels (player fields), and
// whether the server lights it up for you (dynamic flags: a quest you have needs it).

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

const (
	fieldSkillInfo = 0x27C // PLAYER_SKILL_INFO_1_1 (UNIT_END + 0x1E8): 128 x {id | step<<16, value | max<<16, bonus}
	fieldGODynamic = 0x0E  // GAMEOBJECT_DYNAMIC: low 16 bits are GO_DYNFLAG_LO_*
)

// lock types that take a skill (SharedDefines LOCKTYPE_*) and the skill line each uses
var lockSkills = map[uint32]struct {
	name  string
	skill uint32
}{1: {"lockpicking", 633}, 2: {"herbalism", 182}, 3: {"mining", 186}, 19: {"fishing", 356}, 20: {"inscription", 773}}

type lockReq struct{ kind, index, skill uint32 } // kind 1: item (index = entry), 2: lock type (index = LOCKTYPE_*)

var locks struct {
	once sync.Once
	m    map[uint32][]lockReq
}

func loadLocks() {
	locks.once.Do(func() {
		locks.m = map[uint32][]lockReq{}
		b, err := os.ReadFile(filepath.Join(spellsDir, "dbc", "Lock.dbc"))
		if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
			return
		}
		le := binary.LittleEndian
		n, rs := le.Uint32(b[4:]), le.Uint32(b[12:])
		for i := uint32(0); i < n && 20+(i+1)*rs <= uint32(len(b)); i++ {
			r := b[20+i*rs:]
			for k := 0; k < 8; k++ { // ID, then Type[8], Index[8], Skill[8], Action[8]
				if kind := le.Uint32(r[4+k*4:]); kind != 0 {
					locks.m[le.Uint32(r)] = append(locks.m[le.Uint32(r)], lockReq{kind, le.Uint32(r[36+k*4:]), le.Uint32(r[68+k*4:])})
				}
			}
		}
	})
}

// lockID: where each object type keeps its lock in the template data (GameObjectTemplate::GetLockId)
func lockID(typ uint32, data [24]uint32) uint32 {
	switch typ {
	case 0, 1: // door, button
		return data[1]
	case 2, 3, 6, 10, 12, 13, 24, 26: // questgiver, chest, trap, goober, area damage, camera, flag stand, flag drop
		return data[0]
	case 25: // fishing pool
		return data[4]
	}
	return 0
}

// skillLevel: your current level in a skill line, false if you don't have it
func skillLevel(w *client.WorldClient, skill uint32) (uint32, bool) {
	me := w.GetObject(w.CharGUID())
	if me == nil {
		return 0, false
	}
	for i := uint16(0); i < 128; i++ {
		if me.Value(fieldSkillInfo+i*3)&0xFFFF == skill {
			return me.Value(fieldSkillInfo+i*3+1) & 0xFFFF, true
		}
	}
	return 0, false
}

// objectNeeds fills in what using the object takes, from its template (nil until the server answered the query).
func objectNeeds(w *client.WorldClient, o *client.WorldObject, ob *types.Object, t *goTemplate) {
	ob.Lit = o.Value(fieldGODynamic)&0x09 != 0 // GO_DYNFLAG_LO_ACTIVATE | SPARKLE
	if t == nil {
		return
	}
	switch t.typ {
	case 3:
		ob.Quest = t.data[8]
	case 10:
		ob.Quest = t.data[1]
	}
	loadLocks()
	for _, r := range locks.m[lockID(t.typ, t.data)] {
		switch {
		case r.kind == 1 && ob.Key == 0:
			ob.Key = r.index
		case r.kind == 2 && ob.Gather == "":
			if s, ok := lockSkills[r.index]; ok {
				ob.Gather, ob.Skill = s.name, r.skill
				have, known := skillLevel(w, s.skill)
				can := known && have >= r.skill
				ob.CanUse = &can
			}
		}
	}
}
