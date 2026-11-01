package session

// Auras: buffs and debuffs on you and on units in sight (SMSG_AURA_UPDATE, kept in full by the client), as typed
// queries and as aura_gained / aura_faded events for auras on you and auras you cast on others.

import (
	"cmp"
	"math"
	"slices"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func (b *Bot) aura(a client.AuraInfo) types.Aura {
	t := types.Aura{Spell: a.Spell, Name: spellName(a.Spell), Harmful: a.Harmful(), Stacks: max(a.Stacks, 1),
		Mine: a.Caster != 0 && a.Caster == b.world.CharGUID()}
	if sp := spellInfo(a.Spell); sp != nil {
		t.Dispel = dispelTypes[sp.dispel]
	}
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

// controlAuras: SPELL_AURA_* that take control away (SpellAuraDefines.h), named for a spell with no mechanic
var controlAuras = map[uint32]string{2: "charm", 5: "confuse", 6: "charm", 7: "fear", 12: "stun", 25: "pacify", 26: "root",
	27: "silence", 60: "silence", 177: "charm"}

// mechanicNames: enum Mechanics (SharedDefines.h)
var mechanicNames = map[uint32]string{1: "charm", 2: "disoriented", 3: "disarm", 4: "distract", 5: "fear", 6: "grip",
	7: "root", 8: "slow_attack", 9: "silence", 10: "sleep", 11: "snare", 12: "stun", 13: "freeze", 14: "knockout",
	15: "bleed", 16: "bandage", 17: "polymorph", 18: "banish", 19: "shield", 20: "shackle", 21: "mount", 22: "infected",
	23: "turn", 24: "horror", 25: "invulnerability", 26: "interrupt", 27: "daze", 28: "discovery", 29: "immune_shield",
	30: "sap", 31: "enraged", 32: "wounded", 36: "taunted"}

// controls: a unit's auras that take its control away, with the mechanic of the effect that does it (else the spell's)
func controls(auras []client.AuraInfo) []types.Control {
	var out []types.Control
	for _, a := range auras {
		sp := spellInfo(a.Spell)
		if sp == nil {
			continue
		}
		for i, aura := range sp.effAura {
			kind, ok := controlAuras[aura]
			if !ok {
				continue
			}
			c := types.Control{Spell: a.Spell, Name: sp.Name, Mechanic: kind, CasterGUID: types.GUID(a.Caster), BreaksOnDamage: sp.auraInterrupt&0x2 != 0}
			// AuraEffect::CalculateAmount and HandleBreakableCCAuraProc: the confuse, fear, stun, root and transform
			// auras of a spell with proc flags (and no proc-trigger aura) take damage up to a cap
			c.DamageLimit = sp.procFlags != 0 && !slices.Contains(sp.effAura[:], 42) && slices.ContainsFunc(sp.effAura[:], func(a uint32) bool {
				return a == 5 || a == 7 || a == 12 || a == 26 || a == 56
			})
			if m := cmp.Or(sp.effMechanic[i], sp.mechanic); mechanicNames[m] != "" {
				c.Mechanic = mechanicNames[m]
			}
			if a.Left() > 0 {
				ms := uint32(a.Left().Milliseconds())
				c.RemainingMS = &ms
			}
			out = append(out, c)
			break
		}
	}
	return out
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
