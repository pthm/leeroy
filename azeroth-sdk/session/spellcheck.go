package session

import (
	"math"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/types"
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
	cost := spellCost(sp, baseMana(w))
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
		facing := math.Abs(angle) <= math.Pi/2
		d.Facing = &facing
		if sight, known := b.inSight(navigation.Point3D{X: tx, Y: ty, Z: tz + 1.5}); known {
			d.InSight = &sight
		} else {
			d.Unknown = append(d.Unknown, "in_sight")
		}
		// DBC nominal ranges omit talents, hitboxes and friendly range variants.
		// Expose the estimate without claiming exact server range or target eligibility.
		d.Unknown = append(d.Unknown, "effective_range", "target_eligibility")
		if nominal > 0 && distance > nominal {
			return fail("out_of_range")
		}
		if d.InSight != nil && !*d.InSight {
			return fail("line_of_sight")
		}
		if !facing {
			return fail("unit_not_infront")
		}
	}
	d.Usable = true
	return d
}
