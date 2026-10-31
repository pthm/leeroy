package pathfinding

import (
	"context"
	"fmt"
	"math"
)

// BodyProfile defines a standing character. StepHeight reserves the lower foot
// region for traversable steps; support and step checks must accompany sweeps.
type BodyProfile struct{ Radius, Height, StepHeight, MaxSlope float32 }

func WalkingBody() BodyProfile {
	return BodyProfile{Radius: 0.3, Height: 2.03128, StepHeight: 0.6, MaxSlope: 50}
}
func (p BodyProfile) valid() bool {
	return p.Radius > 0 && p.Height > p.StepHeight+2*p.Radius && p.Height < 20 && p.StepHeight >= 0 && p.MaxSlope > 0 && p.MaxSlope < 90
}

type CollisionCheck struct {
	State     string  `json:"state"` // clear, blocked, unknown
	Reason    string  `json:"reason,omitempty"`
	Segment   int     `json:"segment,omitempty"` // 1-based segment ending at this point
	Fraction  float32 `json:"fraction,omitempty"`
	Position  Point3D `json:"position"`
	Normal    Point3D `json:"normal"`
	Model     string  `json:"model,omitempty"`
	Triangles int     `json:"triangles,omitempty"`
}

func unknownCollision(reason string) CollisionCheck {
	return CollisionCheck{State: "unknown", Reason: reason}
}
func vec(p Point3D) Vector3    { return Vector3{p.X, p.Y, p.Z} }
func point(v Vector3) Point3D  { return Point3D{v.X, v.Y, v.Z} }
func length(v Vector3) float32 { return float32(math.Sqrt(float64(v.Dot(v)))) }
func unit(v Vector3) Vector3 {
	if l := length(v); l > 1e-9 {
		return v.Mul(1 / l)
	}
	return Vector3{}
}
func boundsOf(vs ...Vector3) AABox {
	b := AABox{vs[0], vs[0]}
	for _, v := range vs[1:] {
		b.Low = Vector3{min32(b.Low.X, v.X), min32(b.Low.Y, v.Y), min32(b.Low.Z, v.Z)}
		b.High = Vector3{max32(b.High.X, v.X), max32(b.High.Y, v.Y), max32(b.High.Z, v.Z)}
	}
	return b
}
func (b AABox) expanded(r float32) AABox {
	return AABox{b.Low.Sub(Vector3{r, r, r}), b.High.Add(Vector3{r, r, r})}
}
func (b AABox) overlaps(o AABox) bool {
	return b.Low.X <= o.High.X && b.High.X >= o.Low.X && b.Low.Y <= o.High.Y && b.High.Y >= o.Low.Y && b.Low.Z <= o.High.Z && b.High.Z >= o.Low.Z
}
func modelPoint(mi ModelInstance, p Vector3) Vector3 {
	d := p.Sub(mi.iPos)
	return mat3MulVec(mi.iInvRot, Vector3{-d.X, -d.Y, d.Z}).Mul(mi.iInvScale)
}
func worldNormal(mi ModelInstance, n Vector3) Vector3 {
	// Inverse of inverse rotation (transpose); reflection matches VMAP coordinates.
	r := mi.iInvRot
	v := Vector3{r[0][0]*n.X + r[1][0]*n.Y + r[2][0]*n.Z, r[0][1]*n.X + r[1][1]*n.Y + r[2][1]*n.Z, r[0][2]*n.X + r[1][2]*n.Y + r[2][2]*n.Z}
	return unit(Vector3{-v.X, -v.Y, v.Z})
}

// overlap visits the existing BIH with an AABB, checking the serialized node
// encoding and returning an error for incomplete/malformed indexes. Encoding:
// AzerothCore src/common/Collision/BoundingIntervalHierarchy.h.
func (b *BIH) overlap(ctx context.Context, q AABox, visit func(uint32) error) error {
	if len(b.tree) == 0 {
		return fmt.Errorf("missing collision index")
	}
	if !b.bounds.overlaps(q) {
		return nil
	}
	stack := []int{0}
	steps := 0
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		steps++
		if steps > len(b.tree) {
			return fmt.Errorf("cyclic collision index")
		}
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n < 0 || n+2 >= len(b.tree) {
			return fmt.Errorf("invalid collision node")
		}
		word := b.tree[n]
		axis := word >> 30
		off := int(word & 0x1fffffff)
		if axis == 3 {
			count := int(b.tree[n+1])
			if off > len(b.objs) || count > len(b.objs)-off {
				return fmt.Errorf("invalid collision leaf")
			}
			for _, obj := range b.objs[off : off+count] {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := visit(obj); err != nil {
					return err
				}
			}
			continue
		}
		lo, hi := getCoord(q.Low, axis), getCoord(q.High, axis)
		left, right := math.Float32frombits(b.tree[n+1]), math.Float32frombits(b.tree[n+2])
		if math.IsNaN(float64(left)) || math.IsNaN(float64(right)) {
			return fmt.Errorf("invalid collision interval")
		}
		if word&BIH_BVH2_MASK != 0 {
			if hi >= left && lo <= right {
				stack = append(stack, off)
			}
		} else {
			if lo <= left {
				stack = append(stack, off)
			}
			if hi >= right {
				stack = append(stack, off+3)
			}
		}
	}
	return nil
}

// collisionModels loads the complete query footprint. The global map BIH is
// also the coverage authority: an indexed object absent from loaded tiles is
// unknown, while an empty indexed region really is empty (even without a tile).
func (vm *VMapManager) collisionModels(ctx context.Context, m uint32, box AABox) (*StaticMapTree, []ModelInstance, error) {
	tree, err := vm.getTreeContext(ctx, m)
	if err != nil {
		return nil, nil, err
	}
	minX, minY := gameToGridCoords(box.High.X, box.High.Y)
	maxX, maxY := gameToGridCoords(box.Low.X, box.Low.Y)
	minX, maxX = max(0, minX-1), min(63, maxX+1)
	minY, maxY = max(0, minY-1), min(63, maxY+1)
	if (maxX-minX+1)*(maxY-minY+1) > MaxQueryTiles {
		return nil, nil, ErrQueryLimit
	}
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			if err := tree.loadMapTileContext(ctx, uint32(x), uint32(y)); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := lockContext(ctx, tree.dataMu.TryRLock); err != nil {
		return nil, nil, err
	}
	defer tree.dataMu.RUnlock()
	models := []ModelInstance{}
	if !tree.isTiled {
		for _, mi := range tree.treeValues {
			if mi == nil {
				return nil, nil, fmt.Errorf("missing global model")
			}
			models = append(models, *mi)
		}
		if len(models) == 0 {
			return nil, nil, fmt.Errorf("missing global model")
		}
		return tree, models, nil
	}
	internal := AABox{Vector3{VMAP_mid - box.High.X, VMAP_mid - box.High.Y, box.Low.Z}, Vector3{VMAP_mid - box.Low.X, VMAP_mid - box.Low.Y, box.High.Z}}
	err = tree.tree.overlap(ctx, internal, func(id uint32) error {
		mi := tree.treeValues[id]
		if mi == nil || mi.spawn.name == "" {
			return fmt.Errorf("collision instance %d unavailable", id)
		}
		models = append(models, *mi)
		return nil
	})
	return tree, models, err
}

// SweepBodyContext checks a continuous upright capsule sweep against static
// WMO and M2 triangles. It does not use spell LOS filtering or dynamic objects.
func (vm *VMapManager) SweepBodyContext(ctx context.Context, m uint32, a, b Point3D, body BodyProfile) CollisionCheck {
	if err := validateLeg(a, b); err != nil {
		return unknownCollision(err.Error())
	}
	if !body.valid() {
		return unknownCollision("invalid body profile")
	}
	bottom, top := body.StepHeight+body.Radius+0.02, body.Height-body.Radius
	a0, a1 := vec(a).Add(Vector3{Z: bottom}), vec(a).Add(Vector3{Z: top})
	b0, b1 := vec(b).Add(Vector3{Z: bottom}), vec(b).Add(Vector3{Z: top})
	box := boundsOf(a0, a1, b0, b1).expanded(body.Radius)
	tree, models, err := vm.collisionModels(ctx, m, box)
	if err != nil {
		return unknownCollision(err.Error())
	}
	result := CollisionCheck{State: "clear", Fraction: 1}
	for _, mi := range models {
		model, ok := tree.modelForInstanceContext(ctx, &mi)
		if !ok {
			return unknownCollision("collision model unavailable: " + mi.spawn.name)
		}
		if mi.iInvScale <= 0 || math.IsInf(float64(mi.iInvScale), 0) || math.IsNaN(float64(mi.iInvScale)) {
			return unknownCollision("invalid model scale")
		}
		p, q, end := modelPoint(mi, a0), modelPoint(mi, a1), modelPoint(mi, b0)
		delta := end.Sub(p)
		radius := body.Radius * mi.iInvScale
		localBox := boundsOf(p, q, end, q.Add(delta)).expanded(radius)
		for gi := range model.groupModels {
			g := &model.groupModels[gi]
			if !g.vbounds.overlaps(localBox) {
				continue
			}
			err = g.groupTree.overlap(ctx, localBox, func(index uint32) error {
				if int(index) >= len(g.Mesh) {
					return fmt.Errorf("invalid triangle index")
				}
				tri := g.Mesh[index]
				if int(tri.idx0) >= len(g.Vertices) || int(tri.idx1) >= len(g.Vertices) || int(tri.idx2) >= len(g.Vertices) {
					return fmt.Errorf("invalid triangle vertices")
				}
				x, y, z := g.Vertices[tri.idx0], g.Vertices[tri.idx1], g.Vertices[tri.idx2]
				if !boundsOf(x, y, z).overlaps(localBox) {
					return nil
				}
				result.Triangles++
				fraction, normal, hit, err := sweepCapsuleTriangle(ctx, p, q, delta, radius, x, y, z)
				if err != nil {
					return err
				}
				if hit && fraction <= result.Fraction {
					result.State = "blocked"
					result.Reason = "body_obstructed"
					result.Fraction = fraction
					result.Model = mi.spawn.name
					result.Normal = point(worldNormal(mi, normal))
					result.Position = point(vec(a).Add(vec(b).Sub(vec(a)).Mul(fraction)))
				}
				return nil
			})
			if err != nil {
				return unknownCollision(err.Error())
			}
		}
	}
	if ctx.Err() != nil {
		return unknownCollision(ctx.Err().Error())
	}
	return result
}

// TraceMovementContext is a geometry ray with movement masks and coverage.
func (vm *VMapManager) TraceMovementContext(ctx context.Context, m uint32, a, b Point3D) CollisionCheck {
	if err := validateLeg(a, b); err != nil {
		return unknownCollision(err.Error())
	}
	tree, models, err := vm.collisionModels(ctx, m, boundsOf(vec(a), vec(b)).expanded(0.01))
	if err != nil {
		return unknownCollision(err.Error())
	}
	result := CollisionCheck{State: "clear", Fraction: 1}
	for _, mi := range models {
		model, ok := tree.modelForInstanceContext(ctx, &mi)
		if !ok {
			return unknownCollision("collision model unavailable: " + mi.spawn.name)
		}
		p, q := modelPoint(mi, vec(a)), modelPoint(mi, vec(b))
		d := q.Sub(p)
		box := boundsOf(p, q).expanded(0.001)
		for gi := range model.groupModels {
			g := &model.groupModels[gi]
			if !g.vbounds.overlaps(box) {
				continue
			}
			err = g.groupTree.overlap(ctx, box, func(index uint32) error {
				if int(index) >= len(g.Mesh) {
					return fmt.Errorf("invalid triangle index")
				}
				tri := g.Mesh[index]
				if int(tri.idx0) >= len(g.Vertices) || int(tri.idx1) >= len(g.Vertices) || int(tri.idx2) >= len(g.Vertices) {
					return fmt.Errorf("invalid triangle vertices")
				}
				x, y, z := g.Vertices[tri.idx0], g.Vertices[tri.idx1], g.Vertices[tri.idx2]
				result.Triangles++
				if t, normal, hit := rayTriangle(p, d, x, y, z); hit && t <= result.Fraction {
					result.State = "blocked"
					result.Fraction = t
					result.Model = mi.spawn.name
					result.Position = point(vec(a).Add(vec(b).Sub(vec(a)).Mul(t)))
					result.Normal = point(worldNormal(mi, normal))
				}
				return nil
			})
			if err != nil {
				return unknownCollision(err.Error())
			}
		}
	}
	if ctx.Err() != nil {
		return unknownCollision(ctx.Err().Error())
	}
	return result
}
