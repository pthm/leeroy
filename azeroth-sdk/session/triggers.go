package session

// Area triggers: the real client sends CMSG_AREATRIGGER when the player walks into one (AreaTrigger.dbc), and
// the server credits "explore X" quest objectives, inn rest and instance portals only from that. Without it,
// quests like Fargodeep Mine never complete. Sent once per entry, checked twice a second.

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
)

const cmsgAreaTrigger = 0x0B4

type areaTrigger struct {
	id, mapID      uint32
	x, y, z, r     float32
	bx, by, bz, bo float32 // box size and orientation when r == 0
}

func loadAreaTriggers(dataDir string) []areaTrigger {
	b, err := os.ReadFile(filepath.Join(dataDir, "dbc", "AreaTrigger.dbc"))
	if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
		return nil
	}
	le := binary.LittleEndian
	n, rs := le.Uint32(b[4:]), le.Uint32(b[12:])
	f := func(rec []byte, i int) float32 { return math.Float32frombits(le.Uint32(rec[i*4:])) }
	out := make([]areaTrigger, 0, n)
	for i := uint32(0); i < n; i++ {
		rec := b[20+i*rs:]
		out = append(out, areaTrigger{le.Uint32(rec), le.Uint32(rec[4:]), f(rec, 2), f(rec, 3), f(rec, 4), f(rec, 5),
			f(rec, 6), f(rec, 7), f(rec, 8), f(rec, 9)})
	}
	return out
}

// inside mirrors the server's Player::IsInAreaTriggerRadius (sphere, or a box rotated by bo)
func (t *areaTrigger) inside(m uint32, x, y, z float32) bool {
	if m != t.mapID {
		return false
	}
	dx, dy, dz := x-t.x, y-t.y, z-t.z
	if t.r > 0 {
		return dx*dx+dy*dy+dz*dz <= t.r*t.r
	}
	s, c := math.Sincos(float64(-t.bo))
	rx, ry := float32(float64(dx)*c-float64(dy)*s), float32(float64(dx)*s+float64(dy)*c)
	return abs32(rx) <= t.bx/2 && abs32(ry) <= t.by/2 && abs32(dz) <= t.bz/2
}

// radius: how far from the centre the trigger reaches in every direction
func (t *areaTrigger) radius() float32 {
	if t.r > 0 {
		return t.r
	}
	return min(t.bx, t.by, t.bz) / 2
}

// portalNear: the area trigger on map m nearest (2D) x, y within 40 yd. The server reports a corpse inside an instance
// at the instance's Map.dbc entrance on the outside map, with the height of the ground there, which can be the
// surface above the tunnel the portal is in; the portal trigger itself is the place a ghost has to walk to.
func portalNear(triggers []areaTrigger, m uint32, x, y float32) (*areaTrigger, bool) {
	var best *areaTrigger
	bestD := float32(40 * 40)
	for i := range triggers {
		t := &triggers[i]
		if d := (t.x-x)*(t.x-x) + (t.y-y)*(t.y-y); t.mapID == m && d <= bestD {
			best, bestD = t, d
		}
	}
	return best, best != nil
}

// portalApproach: where to walk into portal t: its centre can be past the end of the navmesh (the Deadmines' is at
// the back of its tunnel), so the nearest reachable point to it from the instance's entrance as the server reports it,
// when that's inside the trigger. Cached per trigger.
// ponytail: computed under the caller's lock, once per portal (a few ms, ~1 s if its tiles aren't loaded yet)
func (b *Bot) portalApproach(t *areaTrigger, entrance navigation.Point3D) (navigation.Point3D, bool) {
	b.portalMu.Lock()
	defer b.portalMu.Unlock()
	if p, ok := b.portals[t.id]; ok {
		return p, true
	}
	if b.nav == nil {
		return navigation.Point3D{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	centre := navigation.Point3D{X: t.x, Y: t.y, Z: t.z}
	r, err := b.findPath(ctx, t.mapID, entrance, centre)
	if err != nil || r == nil || len(r.Points) == 0 {
		return navigation.Point3D{}, false
	}
	p := r.Points[len(r.Points)-1]
	if dist3(p, centre) > float64(t.radius()) {
		return navigation.Point3D{}, false
	}
	if b.portals == nil {
		b.portals = map[uint32]navigation.Point3D{}
	}
	b.portals[t.id] = p
	return p, true
}

func abs32(v float32) float32 { return float32(math.Abs(float64(v))) }

func (b *Bot) watchAreaTriggers() {
	triggers := loadAreaTriggers(b.config.DataDir)
	b.areaTriggers = triggers
	if len(triggers) == 0 {
		return // no AreaTrigger.dbc in <data>/dbc (see README)
	}
	w := b.world
	go func() {
		in := map[uint32]bool{}
		for range time.Tick(500 * time.Millisecond) {
			x, y, z, _, m := w.Position()
			for i := range triggers {
				t := &triggers[i]
				now := t.inside(m, x, y, z)
				if now && !in[t.id] {
					w.SendPacketRaw(cmsgAreaTrigger, binary.LittleEndian.AppendUint32(nil, t.id))
				}
				in[t.id] = now
			}
		}
	}()
}
