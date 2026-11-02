package session

// world.los_spot: where to stand to cast at something: the nearest place you can walk to that's within range of it
// and has line of sight, as the server checks spells (your eyes 1.8 yd up, its chest 1.5). Round a corridor's bend,
// or onto a ledge over a pit, rather than walking up to the target (act.cast's approach did that).

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/pthm/leeroy/azsdk/navigation"
	"github.com/pthm/leeroy/azsdk/script"
	"github.com/pthm/leeroy/azsdk/types"
)

type losNavigator interface {
	InLineOfSightContext(context.Context, uint32, navigation.Point3D, navigation.Point3D) (bool, bool)
	FloorHeight(mapID uint32, x, y, hint float32) (float32, bool)
}

// losSpot picks where to stand. Candidates: points along the walking route towards the target (every 2 yd, round
// the bend) and rings round it (a ledge over a pit). Each is scored by how much of the target's surroundings it sees:
// the target and 8 points 4 yd round it, so a sliver of a view past a corner, lost when the target steps aside,
// scores low. The shortest walk with a view of at least 3/4 and 2 yd of range to spare wins; failing that, the best
// view, then the shortest walk.
func (b *Bot) losSpot(ctx context.Context, m uint32, from, target navigation.Point3D, rng, maxWalk float32) (types.LOSSpot, string) {
	nav, ok := b.nav.(losNavigator)
	if !ok {
		return types.LOSSpot{}, "navigation data is unavailable"
	}
	sight := func(from, to navigation.Point3D) bool {
		clear, ok := nav.InLineOfSightContext(ctx, m, navigation.Point3D{X: from.X, Y: from.Y, Z: from.Z + 1.8},
			navigation.Point3D{X: to.X, Y: to.Y, Z: to.Z + 1.5})
		return ok && clear
	}
	// where the target might step: 8 points 4 yd round it, on its floor
	around := []navigation.Point3D{target}
	for k := 0; k < 8; k++ {
		a := float64(k) * math.Pi / 4
		x, y := target.X+4*float32(math.Cos(a)), target.Y+4*float32(math.Sin(a))
		if z, ok := nav.FloorHeight(m, x, y, target.Z); ok && math.Abs(float64(z-target.Z)) < 3 {
			around = append(around, navigation.Point3D{X: x, Y: y, Z: z})
		}
	}
	view := func(p navigation.Point3D) float64 {
		if !sight(p, target) {
			return 0
		}
		n := 0
		for _, q := range around {
			if sight(p, q) {
				n++
			}
		}
		return float64(n) / float64(len(around))
	}
	type cand struct {
		p          navigation.Point3D
		walk, view float64
	}
	var good []cand
	consider := func(p navigation.Point3D, walk float64) {
		if d := dist3(p, target); d <= float64(rng) {
			if v := view(p); v > 0 {
				c := cand{p, walk, v}
				if d > float64(rng)-2 {
					c.view = math.Min(c.view, 0.74) // no range to spare: only if nothing better
				}
				good = append(good, c)
			}
		}
	}
	consider(from, 0)
	// along the route towards it
	if r, err := b.findPath(ctx, m, from, target); err == nil && r != nil && r.Found && len(r.Points) > 1 {
		walk, since, prev := 0.0, 0.0, r.Points[0]
		for _, p := range r.Points[1:] {
			step := dist3(prev, p)
			walk, since, prev = walk+step, since+step, p
			if walk > float64(maxWalk) || ctx.Err() != nil {
				break
			}
			if since >= 2 {
				since = 0
				consider(p, walk)
			}
		}
	}
	// round it, nearest to us first, viewed before walked to
	type ring struct {
		p navigation.Point3D
		d float64
	}
	var rings []ring
	for _, f := range []float32{0.75, 0.5, 0.3} {
		for k := 0; k < 16; k++ {
			a := float64(k) * math.Pi / 8
			x, y := target.X+rng*f*float32(math.Cos(a)), target.Y+rng*f*float32(math.Sin(a))
			if z, ok := nav.FloorHeight(m, x, y, target.Z); ok {
				p := navigation.Point3D{X: x, Y: y, Z: z}
				rings = append(rings, ring{p, dist3(from, p)})
			}
		}
	}
	sort.Slice(rings, func(i, j int) bool { return rings[i].d < rings[j].d })
	tried := 0
	for _, c := range rings {
		if tried >= 12 || ctx.Err() != nil || float32(c.d) > maxWalk {
			break
		}
		if view(c.p) < 0.75 {
			continue
		}
		tried++
		r, err := b.findPath(ctx, m, from, c.p)
		if err != nil || r == nil || !r.Found || r.Partial || len(r.Points) < 2 {
			continue
		}
		walk := 0.0
		for i := 1; i < len(r.Points); i++ {
			walk += dist3(r.Points[i-1], r.Points[i])
		}
		if walk <= float64(maxWalk) {
			consider(r.Points[len(r.Points)-1], walk)
		}
	}
	if len(good) == 0 {
		return types.LOSSpot{}, "no walkable spot within range and in sight of it (within max_walk)"
	}
	sort.Slice(good, func(i, j int) bool {
		gi, gj := good[i].view >= 0.75, good[j].view >= 0.75
		if gi != gj {
			return gi
		}
		if !gi && good[i].view != good[j].view {
			return good[i].view > good[j].view
		}
		return good[i].walk < good[j].walk
	})
	c := good[0]
	return types.LOSSpot{X: round1(c.p.X), Y: round1(c.p.Y), Z: round1(c.p.Z), Walk: math.Round(c.walk*10) / 10,
		Distance: math.Round(dist3(c.p, target)*10) / 10, View: math.Round(c.view*100) / 100}, ""
}

func (b *Bot) setupLOSSpot() {
	b.lua.Func("world.los_spot", "(x, y, z [, range = 30, max_walk = 60]) → Request<LOSSpot>",
		"where to stand to cast at something you can't see: the shortest walk to a spot within range yards of the point that sees it and most of the 4 yards round it (view, 0..1: a sliver of a view past a corner is lost when the target steps aside), with 2 yards of range to spare when it can; walk there with move.to. route_unreachable if there's none within max_walk yards of walking",
		func(x, y, z, rangeYd, maxWalkYd float64) map[string]any {
			rng, maxWalk := float32(30), float32(60)
			if rangeYd > 0 {
				rng = float32(min(rangeYd, 100))
			}
			if maxWalkYd > 0 {
				maxWalk = float32(min(maxWalkYd, 300))
			}
			args := map[string]any{"x": x, "y": y, "z": z, "range": rng, "max_walk": maxWalk}
			px, py, pz, _, m := b.world.Position()
			from, target := navigation.Point3D{X: px, Y: py, Z: pz}, navigation.Point3D{X: float32(x), Y: float32(y), Z: float32(z)}
			var mu sync.Mutex
			var out *script.Outcome
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				s, why := b.losSpot(ctx, m, from, target, rng, maxWalk)
				o := script.Ok(s)
				if why != "" {
					o = script.Fail("route_unreachable", why, nil)
				}
				mu.Lock()
				out = o
				mu.Unlock()
			}()
			return b.request("world.los_spot", args, 12*time.Second, func() *script.Outcome {
				mu.Lock()
				defer mu.Unlock()
				return out
			}, nil)
		})
}
