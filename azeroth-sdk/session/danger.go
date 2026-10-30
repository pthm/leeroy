package session

import (
	"context"
	"math"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type dangerSource struct {
	path  func(context.Context, uint32, navigation.Point3D, navigation.Point3D) (*navigation.PathResult, error)
	sight func(uint32, navigation.Point3D, navigation.Point3D) (bool, bool)
}

func checkDanger(ctx context.Context, here, point types.Position, radius float64, mobs []types.Creature, src dangerSource) types.DangerCheck {
	d := types.DangerCheck{State: "unknown", Hostiles: []types.Creature{}}
	unknown := func(reason string) types.DangerCheck { d.Reason = reason; return d }
	if here.Map != point.Map || math.Hypot(here.X-point.X, here.Y-point.Y)+radius > 80 {
		return unknown("outside_local_observation")
	}
	if src.path == nil || src.sight == nil {
		return unknown("not_available")
	}
	p := navigation.Point3D{X: float32(point.X), Y: float32(point.Y), Z: float32(point.Z)}
	// A zero-length navmesh check confirms the requested floor rather than replacing Z.
	path, err := src.path(ctx, point.Map, p, p)
	if !checkRoute(types.PathCheck{}, p, p, path, err).OK {
		return unknown("unverified_floor")
	}
	uncertain := ""
	for _, u := range mobs {
		if !u.Alive || u.Reaction != "hostile" || !u.Attackable {
			continue
		}
		reach := math.Max(radius, u.AggroRadius+2)
		if math.Hypot(u.X-point.X, u.Y-point.Y) > reach {
			continue
		}
		if ctx.Err() != nil {
			return unknown("route_timeout")
		}
		q := navigation.Point3D{X: float32(u.X), Y: float32(u.Y), Z: float32(u.Z)}
		eye, target := p, q
		eye.Z += 1.8
		target.Z += 1.5
		clear, known := src.sight(point.Map, eye, target)
		if !known {
			uncertain = "collision_unavailable"
			continue
		}
		if !clear {
			continue
		}
		path, err = src.path(ctx, point.Map, p, q)
		checked := checkRoute(types.PathCheck{}, p, q, path, err)
		if !checked.OK {
			uncertain = "floor_connection_unknown"
			continue
		}
		// A path winding through stairs to another floor is not immediate exposure.
		if checked.Data.(types.PathCheck).Distance > reach {
			continue
		}
		d.Hostiles = append(d.Hostiles, u)
	}
	if len(d.Hostiles) > 0 {
		d.State = "hostile"
	} else if uncertain != "" {
		return unknown(uncertain)
	} else {
		d.State = "clear"
	}
	return d
}

func (b *Bot) registerDanger(r *routeRequests) {
	b.lua.Func("world.danger", "(x, y, z [, radius = 25]) → Request<DangerCheck>", "local observed hostile exposure from an arbitrary point, with navmesh floor and point-to-point LOS checks. Radius 1..40; 15s shared route deadline and admission limit. Missing evidence is unknown, never clear; observation coverage is limited to 80 yards around you", func(x, y, z, radius float64) map[string]any {
		if radius < 1 || radius > 40 {
			return b.refuse("world.danger", nil, "invalid_arguments", "radius must be 1..40 yards")
		}
		px, py, pz, _, m := b.world.Position()
		here := types.Position{X: float64(px), Y: float64(py), Z: float64(pz), Map: m}
		point := types.Position{X: x, Y: y, Z: z, Map: m}
		mobs := b.creatures(b.GetNearbyUnits(80))
		src := dangerSource{path: r.find}
		if los, ok := b.nav.(interface {
			InLineOfSight(uint32, navigation.Point3D, navigation.Point3D) (bool, bool)
		}); ok {
			src.sight = los.InLineOfSight
		}
		id, _, _ := b.lua.CurrentTaskIdentity()
		return r.submit(id, "world.danger", map[string]any{"x": x, "y": y, "z": z, "radius": radius}, types.DangerCheck{State: "unknown", Hostiles: []types.Creature{}}, func(ctx context.Context, _ *routeRequest) *script.Outcome {
			return script.Ok(checkDanger(ctx, here, point, radius, mobs, src))
		})
	})
}
