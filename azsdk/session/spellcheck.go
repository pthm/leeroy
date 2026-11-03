package session

import (
	"math"
	"strconv"
	"time"

	"github.com/pthm/leeroy/azsdk/client"
	"github.com/pthm/leeroy/azsdk/navigation"
	"github.com/pthm/leeroy/azsdk/types"
)

func (b *Bot) spellCheck(id uint32, guid uint64) types.SpellCheck {
	d := types.SpellCheck{Unknown: []string{}}
	fail := func(reason string) types.SpellCheck { d.Reason = reason; return d }
	if b.world == nil {
		return fail("not_available")
	}
	w := b.world
	if !w.KnowsSpell(id) {
		return fail("not_known")
	}
	if guid == 0 {
		guid = w.CharGUID()
	}
	sp := spellInfo(id)
	if sp == nil {
		return fail("spell_data_unavailable")
	}
	cost := spellCost(sp, w.GetObject(w.CharGUID()), b.agent)
	d.PowerNeeded = &cost
	if b.agent != nil {
		ms := cdMS(b.agent.cooldownLeft(id))
		d.CooldownMS = &ms
		if ms != 0 {
			return fail("cooldown")
		}
		b.agent.mu.Lock()
		casting := b.agent.castSpell != 0 && time.Now().Before(b.agent.castUntil)
		b.agent.mu.Unlock()
		if casting {
			return fail("casting")
		}
	}
	if !b.IsSpellReady(id) {
		return fail("server_not_ready")
	}
	if current, _ := b.GetPower(); sp.PowerType == uint32(b.GetPowerType()) && current < cost {
		return fail("no_power")
	}
	if sp.casterState != 0 && !hasAuraState(w.GetObject(w.CharGUID()), sp.casterState) {
		return fail("caster_aura_state")
	}
	obj := w.GetObject(guid)
	valid := obj != nil
	d.TargetValid = &valid
	if !valid {
		return fail("unit_gone")
	}
	if sp.targetState != 0 && !hasAuraState(obj, sp.targetState) {
		return fail("target_aura_state")
	}
	if guid != w.CharGUID() {
		x, y, z, o, _ := w.Position()
		tx, ty, tz := obj.InterpolatedPosition()
		distance := math.Sqrt(float64((x-tx)*(x-tx) + (y-ty)*(y-ty) + (z-tz)*(z-tz)))
		d.DistanceYards = &distance
		nominal := float64(sp.Range)
		d.RangeYards = &nominal
		angle := math.Remainder(math.Atan2(float64(ty-y), float64(tx-x))-float64(o), 2*math.Pi)
		facing := math.Abs(angle) <= math.Pi/2 // Unit::HasInArc(M_PI): 90 degrees either side
		d.Facing = &facing
		if sight := b.sightOf(obj); sight != nil {
			d.InSight = sight
		} else {
			d.Unknown = append(d.Unknown, "in_sight")
		}
		// DBC nominal ranges omit talents, hitboxes and friendly range variants.
		// Expose the estimate without claiming exact server range or target eligibility.
		d.Unknown = append(d.Unknown, "effective_range", "target_eligibility")
		if nominal > 0 {
			exceeded := distance > nominal
			d.NominalRangeExceeded = &exceeded
		}
		if d.InSight != nil && !*d.InSight {
			return fail("line_of_sight")
		}
		// the server asks for facing only of spells flagged for it (Spell::CheckRange): attacks and bolts, not
		// heals, buffs or Taunt
		if !facing && sp.facing&1 != 0 {
			return fail("unit_not_infront")
		}
	}
	d.Usable = true
	return d
}

// sightOf: line of sight from your eyes to a unit's (1.5 yards up), nil without collision evidence. spells.check uses
// it for every target, friendly or hostile, as the server checks line of sight for both.
func (b *Bot) sightOf(o *client.WorldObject) *bool {
	x, y, z := o.InterpolatedPosition()
	if visible, ok := b.inSight(navigation.Point3D{X: x, Y: y, Z: z + 1.5}); ok {
		return &visible
	}
	return nil
}

// sightRefused records a server line_of_sight refusal (diagnostic sight_refused) with both ends and what sightOf
// predicts, so the places where the SDK's model says clear and the server doesn't can be found.
func (b *Bot) sightRefused(spell uint32, target uint64) {
	x, y, z, _, m := b.world.Position()
	s := types.DiagnosticSight{Map: m, Caster: [3]float32{x, y, z}}
	if o := b.world.GetObject(target); o != nil && target != b.world.CharGUID() {
		tx, ty, tz := o.InterpolatedPosition()
		s.Target, s.Predicted = &[3]float32{tx, ty, tz}, b.sightOf(o)
	}
	b.lua.RecordObservation("sight_refused", types.DiagnosticDetails{SpellID: spell, Target: strconv.FormatUint(target, 10), Sight: &s})
	if s.Predicted != nil && *s.Predicted {
		b.log("[sight] the server refused spell %d for line of sight; the SDK predicted clear (map %d, from %.1f %.1f %.1f to %.1f %.1f %.1f)",
			spell, m, x, y, z, s.Target[0], s.Target[1], s.Target[2])
	}
}
