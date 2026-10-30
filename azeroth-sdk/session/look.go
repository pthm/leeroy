package session

import (
	"cmp"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// ---------- where things are, from where you stand ----------

// relate fills a place's bearing, clock and dz from you at (mx, my, mz) facing o. WoW's x runs north and y west,
// and facing o is radians counter-clockwise from north, so an angle measured the same way minus o is the turn to
// it (positive: to your left); bearing flips that so positive is to your right, like a clock.
func relate(mx, my, mz, o float64, p *types.Place) {
	rel := math.Atan2(p.Y-my, p.X-mx) - o
	rel = math.Mod(rel+3*math.Pi, 2*math.Pi) - math.Pi // into [-pi, pi)
	p.Bearing = math.Round(-rel * 180 / math.Pi)
	if p.Bearing == -180 {
		p.Bearing = 180
	}
	p.Clock = int(math.Round(p.Bearing/30)+12) % 12
	if p.Clock == 0 {
		p.Clock = 12
	}
	p.DZ = math.Round((p.Z-mz)*10) / 10
}

// aggroRadius is how close a player of your level can come before a hostile creature of its level attacks on sight
// (AzerothCore Creature::GetAttackDistance): 20 yd at equal level, a yard per level of difference, the gap capped
// at 25 levels below the creature, at least 5.
func aggroRadius(creature, you uint32) float64 {
	diff := max(int(you)-int(creature), -25)
	return math.Max(5, float64(20-diff))
}

// me3 is your position and facing, for the functions here
type me3 struct{ x, y, z, o float64 }

func (b *Bot) me3() me3 {
	x, y, z, o, _ := b.world.Position()
	return me3{float64(x), float64(y), float64(z), float64(o)}
}

// aggro sets a living hostile creature's aggro radius and whether you're inside it (straight-line distance in 3D;
// line of sight is checked later, when it's known).
func aggro(c *types.Creature, me me3, myLevel uint32) {
	if !c.Alive || c.Reaction != "hostile" || !c.Attackable {
		return
	}
	c.AggroRadius = aggroRadius(c.Level, myLevel)
	c.InAggro = math.Sqrt(math.Pow(c.X-me.x, 2)+math.Pow(c.Y-me.y, 2)+math.Pow(c.Z-me.z, 2)) <= c.AggroRadius
}

// danger: the hostile creatures whose aggro range you're in or within 15 yd of, nearest edge first, at most 5, each
// with what comes along if you pull it; how many have you in range now; the margin to the nearest. sight fills in
// line of sight (the reach annotator), which decides whether a creature in range can see you.
func danger(hostile []types.Creature, sight func(*types.Danger)) (out []types.Danger, inAggro int, margin *float64) {
	for _, c := range hostile {
		d := types.Danger{GUID: c.GUID, Name: c.Name, Level: c.Level, Elite: c.Elite, Place: c.Place, AggroRadius: c.AggroRadius,
			Margin: math.Round(c.Distance - c.AggroRadius)}
		if d.Margin > 15 {
			continue
		}
		for _, o := range hostile {
			if o.GUID != c.GUID && math.Hypot(o.X-c.X, o.Y-c.Y) <= 10 {
				d.Pack++
			}
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Margin < out[j].Margin })
	if len(out) > 5 {
		for _, d := range out[5:] { // past the five listed: in range counts, sight unchecked
			if d.Margin <= 0 {
				inAggro++
			}
		}
		out = out[:5]
	}
	for i := range out {
		if sight != nil {
			sight(&out[i])
		}
		out[i].InAggro = out[i].Margin <= 0 && (out[i].InSight == nil || *out[i].InSight)
		if out[i].InAggro {
			inAggro++
		}
	}
	if len(out) > 0 {
		m := out[0].Margin
		margin = &m
	}
	return out, inAggro, margin
}

// ---------- world.look: a top-down text map ----------

// lookUnit is one thing on the map
type lookUnit struct {
	label             byte
	name, kind        string // kind: hostile, neutral, friendly, player, party, corpse
	level             uint32
	x, y, z, distance float64
	clock             int
	dz, aggro         float64
	inAggro           bool
	inSight           *bool
	note              string
}

const (
	lookColYd = 2.0 // a character cell is about twice as tall as wide: 2 yd across, 4 down keeps the map square
	lookRowYd = 4.0
	lookStep  = 4.0 // floor this far above or below you shows as ^ or v
)

var compass = []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}

// compassOf: the direction to a point, north up (x north, y west)
func compassOf(dx, dy float64) string {
	deg := math.Mod(-math.Atan2(dy, dx)*180/math.Pi+360, 360) // clockwise from north
	return compass[int(math.Round(deg/45))%8]
}

// renderLook draws the map: you (@) in the middle, north up, units by letter, the ground around you (^ higher, v
// lower, # no walkable floor at your height: wall, water, a building), and the aggro range of hostile creatures
// (:). height(x, y) is the floor nearest your height there, or the terrain where there's no walkable floor near it
// (a hillside); ok false where there's neither.
func renderLook(me me3, myLevel uint32, radius float64, units []lookUnit, height func(x, y float64) (float64, bool)) string {
	cols, rows := int(radius/lookColYd)*2+1, int(radius/lookRowYd)*2+1
	cc, rc := cols/2, rows/2
	grid := make([][]byte, rows)
	for r := range grid {
		grid[r] = make([]byte, cols)
		for c := range grid[r] {
			x, y := me.x+float64(rc-r)*lookRowYd, me.y-float64(c-cc)*lookColYd
			ch := byte('.')
			if h, ok := height(x, y); !ok {
				ch = '#'
			} else if h > me.z+lookStep {
				ch = '^'
			} else if h < me.z-lookStep {
				ch = 'v'
			}
			for _, u := range units {
				if u.aggro > 0 && math.Hypot(x-u.x, y-u.y) <= u.aggro && ch != '#' {
					ch = ':'
				}
			}
			grid[r][c] = ch
		}
	}
	cell := func(x, y float64) (int, int, bool) {
		r, c := rc-int(math.Round((x-me.x)/lookRowYd)), cc+int(math.Round((me.y-y)/lookColYd))
		return r, c, r >= 0 && r < rows && c >= 0 && c < cols
	}
	for i := len(units) - 1; i >= 0; i-- { // nearest drawn last, on top
		if r, c, ok := cell(units[i].x, units[i].y); ok {
			grid[r][c] = units[i].label
		}
	}
	grid[rc][cc] = '@'
	facing := compassOf(math.Cos(me.o), math.Sin(me.o))
	var sb strings.Builder
	fmt.Fprintf(&sb, "north up, %g yd across a cell, %g down; you (@) at %.0f %.0f %.0f facing %s\n", lookColYd, lookRowYd, me.x, me.y, me.z, facing)
	fmt.Fprintf(&sb, "ground: . level  ^ %g+ yd higher  v %g+ yd lower  # no floor at your height (wall, water, building)  : a hostile's aggro range\n", lookStep, lookStep)
	sb.WriteString("units: A-Z hostile, a-z neutral or friendly, 1-9 players, x corpse\n")
	for _, row := range grid {
		sb.Write(row)
		sb.WriteByte('\n')
	}
	inAggro := 0
	for _, u := range units {
		if u.inAggro {
			inAggro++
		}
		line := fmt.Sprintf("%c %s", u.label, u.name)
		if u.level > 0 {
			line += fmt.Sprintf(" L%d", u.level)
		}
		line += fmt.Sprintf(" %s, %.0f yd %s (%d o'clock)", u.kind, u.distance, compassOf(u.x-me.x, u.y-me.y), u.clock)
		if math.Abs(u.dz) >= 3 {
			line += fmt.Sprintf(", %.0f yd %s", math.Abs(u.dz), map[bool]string{true: "above", false: "below"}[u.dz > 0])
		}
		if u.aggro > 0 {
			line += fmt.Sprintf(", aggro %.0f yd", u.aggro)
			if u.inAggro {
				line += " (you're inside)"
			}
		}
		if u.inSight != nil && !*u.inSight {
			line += ", out of sight"
		}
		if u.note != "" {
			line += ", " + u.note
		}
		sb.WriteString(line + "\n")
	}
	if inAggro > 0 {
		fmt.Fprintf(&sb, "%d hostile(s) have you in aggro range\n", inAggro)
	}
	return sb.String()
}

// look gathers the map's units around you and draws it (world.look).
func (b *Bot) look(radius float64) string {
	me, lvl := b.me3(), uint32(b.GetLevel())
	var us []UnitInfo
	for _, u := range b.GetNearbyUnits(float32(radius * 1.5)) {
		if u.Flags&0x2000000 == 0 {
			us = append(us, u)
		}
	}
	cs := b.creatures(us)
	b.annotateNearby(cs)
	var units []lookUnit
	hostile, other := byte('A'), byte('a')
	for _, c := range cs {
		if math.Abs(c.X-me.x) > radius || math.Abs(c.Y-me.y) > radius*1.0 {
			continue
		}
		u := lookUnit{name: cmp.Or(c.Name, "?"), kind: c.Reaction, level: c.Level, x: c.X, y: c.Y, z: c.Z,
			distance: c.Distance, clock: c.Clock, dz: c.DZ, aggro: c.AggroRadius, inAggro: c.InAggro, inSight: c.InSight}
		switch {
		case !c.Alive:
			if !c.Lootable {
				continue // only corpses worth walking to
			}
			u.label, u.kind, u.note, u.aggro = 'x', "corpse", "lootable", 0
		case c.Reaction == "hostile" && hostile <= 'Z':
			u.label, hostile = hostile, hostile+1
		case other <= 'z':
			u.label, other = other, other+1
		default:
			continue
		}
		if len(c.Roles) > 0 {
			u.note = strings.Join(c.Roles, " ")
		}
		units = append(units, u)
	}
	n := byte('1')
	for _, p := range b.players(b.GetNearbyPlayers(float32(radius * 1.5))) {
		if n > '9' || math.Abs(p.X-me.x) > radius || math.Abs(p.Y-me.y) > radius {
			continue
		}
		units = append(units, lookUnit{label: n, name: p.Name, kind: "player", level: p.Level, x: p.X, y: p.Y, z: p.Z,
			distance: p.Distance, clock: p.Clock, dz: p.DZ})
		n++
	}
	_, _, _, _, m := b.world.Position()
	height := func(x, y float64) (float64, bool) {
		if b.nav == nil {
			return me.z, true
		}
		if h, ok := b.nav.GetHeight(m, float32(x), float32(y), float32(me.z)); ok {
			return float64(h), true
		}
		// no walkable floor near your height: the terrain says whether it's a slope above or below you (a hill, a
		// ravine); level terrain without a floor stays # (a wall, water, a building)
		if tn, ok := b.nav.(terrainHeightNavigator); ok {
			if t, ok := tn.GetTerrainHeight(m, float32(x), float32(y)); ok && math.Abs(float64(t)-me.z) > lookStep {
				return float64(t), true
			}
		}
		return 0, false
	}
	return renderLook(me, lvl, radius, units, height)
}
