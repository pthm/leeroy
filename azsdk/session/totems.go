package session

// Totems: a shaman has one totem slot per element. The server says when a totem goes down (SMSG_TOTEM_CREATED: slot,
// the totem's unit, its duration, the spell), not when it goes; a totem is up while its unit is in sight and alive
// and its time isn't over. CMSG_TOTEM_DESTROYED takes one down, as right-clicking its icon does.

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/pthm/leeroy/azsdk/script"
	"github.com/pthm/leeroy/azsdk/types"
)

const (
	smsgTotemCreated   = 0x413
	cmsgTotemDestroyed = 0x414
)

var totemElements = []string{"fire", "earth", "water", "air"} // the slots, as the server numbers them

type totemSlot struct {
	guid  uint64
	spell uint32
	until time.Time
}

type totemSlots struct {
	mu    sync.Mutex
	slots [4]totemSlot
}

// watchTotems reads SMSG_TOTEM_CREATED: slot (u8), the totem (u64), duration in ms (u32), spell (u32).
func (b *Bot) watchTotems() {
	t := &b.totems
	b.world.AddPacketHook(func(op uint16, data []byte) {
		if op != smsgTotemCreated || len(data) < 17 || data[0] >= 4 {
			return
		}
		le := binary.LittleEndian
		t.mu.Lock()
		t.slots[data[0]] = totemSlot{
			guid: le.Uint64(data[1:]), spell: le.Uint32(data[13:]),
			until: time.Now().Add(time.Duration(le.Uint32(data[9:])) * time.Millisecond),
		}
		t.mu.Unlock()
	})
}

// up: the totem in a slot, if it's still there
func (b *Bot) totemUp(slot int) (totemSlot, bool) {
	b.totems.mu.Lock()
	s := b.totems.slots[slot]
	b.totems.mu.Unlock()
	if s.guid == 0 || time.Now().After(s.until) {
		return s, false
	}
	u := b.GetUnitInfo(s.guid)
	return s, u != nil && u.IsAlive
}

func (b *Bot) setupTotems() {
	e := b.lua
	e.Func("totems.list", "() → Totem[]", "your totems that are up, one per element at most: which, its unit, the seconds it has left", func() []types.Totem {
		out := []types.Totem{}
		for i, el := range totemElements {
			if s, up := b.totemUp(i); up {
				out = append(out, types.Totem{
					Element: el, GUID: types.GUID(s.guid), Spell: s.spell, Name: spellName(s.spell),
					SecondsLeft: int(time.Until(s.until).Seconds()),
				})
			}
		}
		return out
	})
	e.Func("totems.destroy", "(element) → Request", "take down your totem of an element: fire, earth, water or air", func(el string) map[string]any {
		args := map[string]any{"element": el}
		slot := -1
		for i, x := range totemElements {
			if x == el {
				slot = i
			}
		}
		if slot < 0 {
			return b.refuse("totems.destroy", args, "invalid_arguments", "fire, earth, water or air")
		}
		s, up := b.totemUp(slot)
		if !up {
			return b.refuse("totems.destroy", args, "no_totem", "you have no "+el+" totem up")
		}
		if err := b.world.SendPacketRaw(cmsgTotemDestroyed, []byte{byte(slot)}); err != nil {
			return b.refuse("totems.destroy", args, "failed", err.Error())
		}
		return b.request("totems.destroy", args, 3*time.Second, func() *script.Outcome {
			if u := b.GetUnitInfo(s.guid); u == nil || !u.IsAlive {
				return script.Ok(nil)
			}
			return nil
		}, nil)
	})
}
