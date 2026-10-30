package session

// Fight summaries: from entering combat to leaving it (or dying), what happened, as one fight_end event. Built from
// the same events scripts see (damage, heal, casts, kills, xp), so it costs nothing extra to collect.

import (
	"math"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type fightState struct {
	mu      sync.Mutex
	active  bool
	start   time.Time
	last    time.Time // the last event counted
	sum     types.FightSummary
	enemies map[types.GUID]types.UnitRef
}

func (f *fightState) reset() {
	f.sum = types.FightSummary{Abilities: map[string]int{}, FailedCasts: map[string]int{}, Enemies: []types.UnitRef{}, Kills: []types.UnitRef{},
		AurasApplied: map[string]int{}, AurasSuffered: map[string]int{}, Outcomes: map[string]int{}}
	f.enemies = map[types.GUID]types.UnitRef{}
}

// count adds an event to the running fight (bus listener: must not block or emit).
func (f *fightState) count(ev script.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.active && time.Since(f.last) > 3*time.Second { // hits just before the combat flag still count
		f.reset()
	}
	f.last = time.Now()
	enemy := func(u types.UnitRef) {
		if !u.Me && !u.Player {
			f.enemies[u.GUID] = u
		}
	}
	switch d := ev.Data.(type) {
	case types.Damage:
		if ev.Type == "heal" {
			if d.Src.Me {
				f.sum.HealingDone += d.Amount
				f.sum.Overhealing += d.Overheal
			}
			if d.Taken {
				f.sum.HealingTaken += d.Amount
			}
			return
		}
		if d.Src.Me {
			f.sum.DamageDealt += d.Amount
			enemy(d.Dst)
			if d.Outcome != "" {
				f.sum.Outcomes[d.Outcome]++
			}
		}
		if d.Taken {
			f.sum.DamageTaken += d.Amount
			enemy(d.Src)
		}
	case types.Cast:
		switch ev.Type {
		case "cast_done":
			f.sum.Abilities[d.Name]++
		case "cast_failed":
			f.sum.FailedCasts[d.Code]++
		}
	case types.Kill:
		if ev.Type == "kill" || ev.Type == "party_kill" {
			f.sum.Kills = append(f.sum.Kills, d.Victim)
		}
	case types.XPGain:
		f.sum.XP += d.Amount
	case types.AttackFailed:
		f.sum.SwingsRefused++
	case types.AuraChange:
		if ev.Type != "aura_gained" {
			return
		}
		if d.Unit.Me && d.Aura.Harmful {
			f.sum.AurasSuffered[d.Aura.Name]++
		} else if !d.Unit.Me && d.Aura.Mine {
			f.sum.AurasApplied[d.Aura.Name]++
		}
	}
}

func (b *Bot) setupFights() {
	f := &fightState{}
	f.reset()
	b.fight = f
	b.lua.Bus.Listen(f.count)
}

// fightTransitions (each tick, from stateEvents): start a fight on entering combat, summarise it on leaving or dying.
func (b *Bot) fightTransitions(enteredCombat, leftCombat, died bool) {
	f := b.fight
	if f == nil {
		return
	}
	frac := func(c, m uint32) float64 {
		if m == 0 {
			return 0
		}
		return math.Round(float64(c)/float64(m)*100) / 100
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if enteredCombat && !f.active {
		if time.Since(f.last) > 3*time.Second {
			f.reset()
		}
		f.active, f.start = true, time.Now()
		if f.sum.HealthStart == 0 {
			hc, hm := b.GetHealth()
			pc, pm := b.GetPower()
			f.sum.HealthStart, f.sum.PowerStart = frac(hc, hm), frac(pc, pm)
		}
		return
	}
	if !f.active || !(leftCombat || died) {
		return
	}
	s := f.sum
	s.Seconds = math.Round(time.Since(f.start).Seconds()*10) / 10
	hc, hm := b.GetHealth()
	pc, pm := b.GetPower()
	s.HealthEnd, s.PowerEnd = frac(hc, hm), frac(pc, pm)
	for _, e := range f.enemies {
		s.Enemies = append(s.Enemies, e)
	}
	switch {
	case died:
		s.Outcome = "died"
	case len(s.Kills) > 0:
		s.Outcome = "won"
	default:
		s.Outcome = "ended"
	}
	if s.Seconds > 0 {
		s.DPS = math.Round(float64(s.DamageDealt)/s.Seconds*10) / 10
	}
	if len(s.FailedCasts) == 0 {
		s.FailedCasts = nil
	}
	if len(s.Outcomes) == 0 {
		s.Outcomes = nil
	}
	if len(s.AurasApplied) == 0 {
		s.AurasApplied = nil
	}
	if len(s.AurasSuffered) == 0 {
		s.AurasSuffered = nil
	}
	if g, ok := b.lua.Native("group.info").(types.Group); ok && len(g.Members) > 0 {
		in := map[types.GUID]bool{}
		for _, m := range g.Members {
			in[m.GUID] = true
		}
		var dmg, heal uint32
		seen := map[types.GUID]bool{}
		for _, m := range b.meter(s.Seconds + 1) {
			if m.Unit.Me || in[m.Unit.GUID] {
				m.BySpell = nil // yours are in abilities
				s.Group = append(s.Group, m)
				dmg, heal, seen[m.Unit.GUID] = dmg+m.Damage, heal+m.Healing, true
			}
		}
		// whoever in sight did nothing this fight (you included) is listed with zeros; out of sight, nothing is known
		present := []uint64{b.world.CharGUID()}
		for _, m := range g.Members {
			if b.world.GetObject(uint64(m.GUID)) != nil {
				present = append(present, uint64(m.GUID))
			}
		}
		for _, guid := range present {
			if !seen[types.GUID(guid)] {
				s.Group = append(s.Group, types.Meter{Unit: b.ref(guid)})
			}
		}
		share := func(v, total uint32) *float64 {
			x := 0.0
			if total > 0 {
				x = math.Round(float64(v)/float64(total)*100) / 100
			}
			return &x
		}
		s.DamageShare, s.HealingShare = share(s.DamageDealt, dmg), share(s.HealingDone, heal)
	}
	if b.fightLog != nil {
		b.fightLog.finish(&s, f.start, time.Now(), b.log)
	}
	f.active = false
	f.reset()
	go b.emit("fight_end", s) // not under f.mu: the bus calls f.count
}

// partyInCombat: a party member in sight has the server's in-combat flag (out of sight the server doesn't say).
func (b *Bot) partyInCombat() bool {
	g, ok := b.lua.Native("group.info").(types.Group)
	if !ok {
		return false
	}
	for _, m := range g.Members {
		if o := b.world.GetObject(uint64(m.GUID)); o != nil && o.Value(client.UnitFieldFlags)&0x80000 != 0 {
			return true
		}
	}
	return false
}
