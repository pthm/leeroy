package metrics

import "github.com/pthm/leeroy/azeroth-sdk/types"

// Combat totals for one character from its own event stream. Reported next to the score, not part of it.
type Combat struct {
	DamageDealt float64 // damage you did, to anything
	DamageTaken float64 // damage done to you
	HealingDone float64 // healing you cast that landed (overheal excluded), on anyone
	Kills       int     // killing blows
	PartyKills  int     // a party member's killing blows
	Deaths      int
	XP          float64 // XP gained, from kills, quests and exploring
}

// Add counts one event.
func (c *Combat) Add(e Event) {
	switch e.Type {
	case "damage":
		if d, ok := e.Data.(types.Damage); ok {
			if d.Src.Me {
				c.DamageDealt += float64(d.Amount)
			}
			if d.Taken {
				c.DamageTaken += float64(d.Amount)
			}
		}
	case "heal":
		if d, ok := e.Data.(types.Damage); ok && d.Src.Me {
			c.HealingDone += float64(d.Amount)
		}
	case "kill":
		c.Kills++
	case "party_kill":
		c.PartyKills++
	case "death":
		c.Deaths++
	case "xp":
		if x, ok := e.Data.(types.XPGain); ok {
			c.XP += float64(x.Amount)
		}
	}
}
