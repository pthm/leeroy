package main

// The roster: a card per player at a moment, grouped by party (leader first) or flat, sorted.

import (
	"cmp"
	"fmt"
	"html/template"
	"math"
	"slices"
	"strings"
)

// Card is one player's card.
type Card struct {
	Name, Color, What string
	Leader, Sel, Off  bool
	State             string // combat, dead, ghost or ""
	Lvl               int
	HP, HPMax         int
	HPPct, PwPct      float64
	Power             string
	PW, PWMax         int
	Target            *Target
	TargetHP          string // the target's health bar colour
	Pet               *PetCard
	Say, SayFull      string
	Money             int
	Score             Ev
	Agent             map[string]any
	sortKeys          map[string]float64
}

// PetCard is the pet a player has out, on its card.
type PetCard struct {
	Name     string
	Lvl      int
	HP, Max  int
	HPPct    float64
	Dead     bool
	BarColor string
}

// RosterGroup is a party's cards.
type RosterGroup struct {
	ID     int
	Sel    bool
	Spread int // how far apart its members are, yd
	Cards  []Card
}

var power = map[int]string{0: "mana", 1: "rage", 3: "energy", 6: "runic"}

func (s *Store) card(p *Player, t float64, q Query, leader bool) Card {
	c := Card{Name: p.Name, Color: ClassColor(p.Cls), Leader: leader, Sel: q.Sel == p.Name, Score: p.ScoreAt(t), sortKeys: map[string]float64{}}
	if m, ok := s.agents[p.Name].(map[string]any); ok {
		c.Agent = map[string]any{"runtime": S(m, "runtime"), "model": S(m, "model"), "short": strings.TrimPrefix(S(m, "model"), "claude-"), "spec": S(m, "spec")}
	}
	c.What = p.Cls
	if c.Agent != nil && c.Agent["spec"] != "" {
		c.What = c.Agent["spec"].(string)
	}
	if n := s.classIndex(p); n > 0 {
		c.What += fmt.Sprintf(" #%d", n)
	}
	if e := p.SayAt(t); e != nil {
		c.SayFull = S(e, "text")
		c.Say, _, _ = strings.Cut(c.SayFull, "\n")
	}
	c.sortKeys["score"] = -1e9
	if c.Score != nil {
		c.sortKeys["score"] = N(c.Score, "score")
	}
	pp, ok := p.At(t)
	if !ok {
		c.Off = true
		c.sortKeys["level"], c.sortKeys["hp"] = -1, 2
		return c
	}
	c.Lvl, c.HP, c.HPMax, c.PW, c.PWMax, c.Money = pp.Lvl, pp.HP, pp.HPMax, pp.PW, pp.PWMax, pp.Money
	c.HPPct = 100 * float64(pp.HP) / math.Max(1, float64(pp.HPMax))
	c.PwPct = 100 * float64(pp.PW) / math.Max(1, float64(pp.PWMax))
	c.Power = power[pp.PT]
	if c.Power == "" {
		c.Power = "mana"
	}
	switch {
	case !pp.Alive && pp.Ghost:
		c.State = "ghost"
	case !pp.Alive:
		c.State = "dead"
	case pp.Combat:
		c.State = "combat"
	}
	if pp.Alive {
		c.Target = s.TargetOf(p, t)
		if c.Target != nil && c.Target.HP != nil {
			c.TargetHP = barColor(*c.Target.HP)
		}
	}
	if pt := pp.Pet; pt != nil {
		c.Pet = &PetCard{Name: pt.Name, Lvl: pt.Lvl, HP: pt.HP, Max: pt.HPMax, HPPct: 100 * float64(pt.HP) / math.Max(1, float64(pt.HPMax)), Dead: !pt.Alive}
		c.Pet.BarColor = barColor(int(c.Pet.HPPct))
	}
	c.sortKeys["level"], c.sortKeys["hp"] = float64(pp.Lvl), c.HPPct/100
	return c
}

func barColor(hp int) string {
	switch {
	case hp > 50:
		return "#3ccf4e"
	case hp > 20:
		return "#e8c040"
	}
	return "#e04040"
}

// classIndex is p's number among players of its class (1, 2...), or 0 when it's the only one.
func (s *Store) classIndex(p *Player) int {
	n, i := 0, 0
	for _, q := range s.players {
		if q.Cls == p.Cls {
			n++
			if q == p {
				i = n
			}
		}
	}
	if n < 2 {
		return 0
	}
	return i
}

// sortCards orders cards by the roster's sort: score, level and health highest first; name; class.
func sortCards(cs []Card, by string) {
	slices.SortStableFunc(cs, func(a, b Card) int {
		var c int
		switch by {
		case "name":
			c = cmp.Compare(a.Name, b.Name)
		case "cls":
			c = cmp.Compare(a.What, b.What)
		case "hp":
			c = cmp.Compare(a.sortKeys["hp"], b.sortKeys["hp"])
		case "level":
			c = cmp.Compare(b.sortKeys["level"], a.sortKeys["level"])
		default:
			c = cmp.Compare(b.sortKeys["score"], a.sortKeys["score"])
		}
		if c == 0 {
			c = cmp.Compare(a.Name, b.Name)
		}
		return c
	})
}

// Roster is the roster at q.T: parties (largest first, leader first) and everyone else, or everyone flat.
func (s *Store) Roster(q Query) map[string]any {
	t := q.T
	if !q.ByGroup {
		var cs []Card
		for _, p := range s.players {
			cs = append(cs, s.card(p, t, q, false))
		}
		sortCards(cs, q.Sort)
		return map[string]any{"Rest": cs, "RestLabel": "", "Count": len(s.players)}
	}
	groups, _ := s.GroupsAt(t)
	var out []RosterGroup
	for _, g := range groups {
		rg := RosterGroup{ID: g.ID, Sel: q.Grp == g.ID}
		var lead []Card
		for _, p := range g.Members {
			c := s.card(p, t, q, p.Name == g.Leader)
			if c.Leader {
				lead = append(lead, c)
			} else {
				rg.Cards = append(rg.Cards, c)
			}
		}
		sortCards(rg.Cards, q.Sort)
		rg.Cards = append(lead, rg.Cards...)
		for _, a := range g.Members {
			for _, b := range g.Members {
				pa, _ := a.At(t)
				pb, _ := b.At(t)
				rg.Spread = max(rg.Spread, int(math.Round(math.Hypot(pa.X-pb.X, pa.Y-pb.Y))))
			}
		}
		out = append(out, rg)
	}
	var rest []Card
	inGroup := map[string]bool{}
	for _, g := range groups {
		for _, p := range g.Members {
			inGroup[p.Name] = true
		}
	}
	for _, p := range s.players {
		if !inGroup[p.Name] {
			rest = append(rest, s.card(p, t, q, false))
		}
	}
	sortCards(rest, q.Sort)
	label := "Everyone"
	if len(groups) > 0 {
		label = "Solo"
	}
	return map[string]any{"Groups": out, "Rest": rest, "RestLabel": label, "Count": len(s.players)}
}

// Coins is money as the game shows it: gold, silver, copper, each in its coin's colour.
func Coins(c int) template.HTML {
	g, sv, cp := c/10000, c/100%100, c%100
	var b strings.Builder
	b.WriteString(`<span class="coin" title="money">`)
	if g > 0 {
		fmt.Fprintf(&b, `%d<span class="g">g</span> `, g)
	}
	if g > 0 || sv > 0 {
		fmt.Fprintf(&b, `%d<span class="s">s</span> `, sv)
	}
	fmt.Fprintf(&b, `%d<span class="c">c</span></span>`, cp)
	return template.HTML(b.String())
}
