package session

// Auras: buffs and debuffs on you and on units in sight (SMSG_AURA_UPDATE, kept in full by the client), as typed
// queries and as aura_gained / aura_faded events for auras on you and auras you cast on others.

import (
	"math"

	"github.com/pthm/wowbench/azeroth-sdk/client"
	"github.com/pthm/wowbench/azeroth-sdk/types"
)

func (b *Bot) aura(a client.AuraInfo) types.Aura {
	t := types.Aura{Spell: a.Spell, Name: spellName(a.Spell), Harmful: a.Harmful(), Stacks: max(a.Stacks, 1),
		Mine: a.Caster != 0 && a.Caster == b.world.CharGUID()}
	if a.MaxDurationMS > 0 {
		left := math.Round(a.Left().Seconds()*10) / 10
		dur := math.Round(float64(a.MaxDurationMS)/100) / 10
		t.SecondsLeft, t.Duration = &left, &dur
	}
	if a.Caster != 0 {
		r := b.ref(a.Caster)
		t.Caster = &r
	}
	return t
}

// hiddenAura: internal states the real client never shows (Spell.dbc "do not display")
func hiddenAura(spell uint32) bool {
	sp := spellInfo(spell)
	return sp != nil && (sp.attrs&0x80 != 0 || sp.Name == "")
}

func (b *Bot) aurasOf(unit uint64) []types.Aura {
	out := []types.Aura{}
	for _, a := range b.world.Auras(unit) {
		if !hiddenAura(a.Spell) {
			out = append(out, b.aura(a))
		}
	}
	return out
}

func (b *Bot) setupAuras() {
	w, e := b.world, b.lua
	me := w.CharGUID()
	e.Func("me.auras", "() → Aura[]", "your buffs and debuffs: name, harmful, stacks, time left, who cast them", func() []types.Aura { return b.aurasOf(me) })
	e.Func("units.auras", "(guid) → Aura[]", "a unit's buffs and debuffs (units in sight: your target, party members, mobs). mine = you cast it: is your debuff still on the mob?",
		func(g uint64) []types.Aura { return b.aurasOf(g) })
	w.AddAuraHook(func(unit uint64, prev, cur *client.AuraInfo) {
		relevant := func(a *client.AuraInfo) bool {
			return a != nil && (unit == me || a.Caster == me) && !hiddenAura(a.Spell)
		}
		if prev != nil && cur != nil && prev.Spell == cur.Spell {
			return // refreshed or restacked
		}
		if relevant(prev) {
			b.emit("aura_faded", types.AuraChange{Unit: b.ref(unit), Aura: b.aura(*prev)})
		}
		if relevant(cur) {
			b.emit("aura_gained", types.AuraChange{Unit: b.ref(unit), Aura: b.aura(*cur)})
		}
	})
}
