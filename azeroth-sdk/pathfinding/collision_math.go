package pathfinding

import (
	"context"
	"fmt"
	"math"
)

func rayTriangle(o, d, a, b, c Vector3) (float32, Vector3, bool) {
	e, f := b.Sub(a), c.Sub(a)
	h := d.Cross(f)
	den := e.Dot(h)
	if math.Abs(float64(den)) < 1e-9 {
		return 0, Vector3{}, false
	}
	s := o.Sub(a)
	u := s.Dot(h) / den
	if u < 0 || u > 1 {
		return 0, Vector3{}, false
	}
	q := s.Cross(e)
	v := d.Dot(q) / den
	if v < 0 || u+v > 1 {
		return 0, Vector3{}, false
	}
	t := f.Dot(q) / den
	n := unit(e.Cross(f))
	if n.Dot(d) > 0 {
		n = n.Mul(-1)
	}
	return t, n, t >= 0 && t <= 1
}
func closestTriangle(p, a, b, c Vector3) Vector3 {
	ab, ac, ap := b.Sub(a), c.Sub(a), p.Sub(a)
	d1, d2 := ab.Dot(ap), ac.Dot(ap)
	if d1 <= 0 && d2 <= 0 {
		return a
	}
	bp := p.Sub(b)
	d3, d4 := ab.Dot(bp), ac.Dot(bp)
	if d3 >= 0 && d4 <= d3 {
		return b
	}
	vc := d1*d4 - d3*d2
	if vc <= 0 && d1 >= 0 && d3 <= 0 {
		return a.Add(ab.Mul(d1 / (d1 - d3)))
	}
	cp := p.Sub(c)
	d5, d6 := ab.Dot(cp), ac.Dot(cp)
	if d6 >= 0 && d5 <= d6 {
		return c
	}
	vb := d5*d2 - d1*d6
	if vb <= 0 && d2 >= 0 && d6 <= 0 {
		return a.Add(ac.Mul(d2 / (d2 - d6)))
	}
	va := d3*d6 - d5*d4
	if va <= 0 && d4-d3 >= 0 && d5-d6 >= 0 {
		return b.Add(c.Sub(b).Mul((d4 - d3) / ((d4 - d3) + (d5 - d6))))
	}
	den := va + vb + vc
	if math.Abs(float64(den)) < 1e-12 {
		return a
	}
	return a.Add(ab.Mul(vb / den)).Add(ac.Mul(vc / den))
}
func segmentDistance(p, q, a, b Vector3) (Vector3, Vector3) {
	d, e, r := q.Sub(p), b.Sub(a), p.Sub(a)
	aa, ee := d.Dot(d), e.Dot(e)
	f := e.Dot(r)
	s, t := float32(0), float32(0)
	if aa <= 1e-12 && ee <= 1e-12 {
		return p, a
	}
	if aa <= 1e-12 {
		t = max32(0, min32(1, f/ee))
	} else {
		c := d.Dot(r)
		if ee <= 1e-12 {
			s = max32(0, min32(1, -c/aa))
		} else {
			bb := d.Dot(e)
			den := aa*ee - bb*bb
			if den > 1e-12 {
				s = max32(0, min32(1, (bb*f-c*ee)/den))
			}
			t = (bb*s + f) / ee
			if t < 0 {
				t = 0
				s = max32(0, min32(1, -c/aa))
			} else if t > 1 {
				t = 1
				s = max32(0, min32(1, (bb-c)/aa))
			}
		}
	}
	return p.Add(d.Mul(s)), a.Add(e.Mul(t))
}
func capsuleTriangleDistance(p, q, a, b, c Vector3) (float32, Vector3) {
	if _, n, hit := rayTriangle(p, q.Sub(p), a, b, c); hit {
		return 0, n
	}
	delta := p.Sub(closestTriangle(p, a, b, c))
	best := delta.Dot(delta)
	d := q.Sub(closestTriangle(q, a, b, c))
	if d.Dot(d) < best {
		delta = d
		best = d.Dot(d)
	}
	vertices := []Vector3{a, b, c}
	for i := 0; i < 3; i++ {
		x, y := segmentDistance(p, q, vertices[i], vertices[(i+1)%3])
		d = x.Sub(y)
		if d.Dot(d) < best {
			delta = d
			best = d.Dot(d)
		}
	}
	return float32(math.Sqrt(float64(best))), unit(delta)
}

// Conservative advancement uses the Lipschitz bound on distance under a
// translation. It cannot jump over a thin triangle between time samples.
func sweepCapsuleTriangle(ctx context.Context, p, q, delta Vector3, r float32, a, b, c Vector3) (float32, Vector3, bool, error) {
	speed := length(delta)
	t := float32(0)
	for i := 0; i < 96; i++ {
		if err := ctx.Err(); err != nil {
			return 0, Vector3{}, false, err
		}
		d, n := capsuleTriangleDistance(p.Add(delta.Mul(t)), q.Add(delta.Mul(t)), a, b, c)
		if d <= r+0.001 {
			return t, n, true, nil
		}
		if speed < 1e-9 {
			return 1, Vector3{}, false, nil
		}
		step := (d - r) / speed
		if t+step > 1 {
			return 1, Vector3{}, false, nil
		}
		t += step * 0.95
	}
	return t, Vector3{}, false, fmt.Errorf("collision sweep did not converge")
}
