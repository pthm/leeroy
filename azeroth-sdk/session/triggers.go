package session

// Area triggers: the real client sends CMSG_AREATRIGGER when the player walks into one (AreaTrigger.dbc), and
// the server credits "explore X" quest objectives, inn rest and instance portals only from that. Without it,
// quests like Fargodeep Mine never complete. Sent once per entry, checked twice a second.

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"time"
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

func abs32(v float32) float32 { return float32(math.Abs(float64(v))) }

func (b *Bot) watchAreaTriggers() {
	triggers := loadAreaTriggers(b.config.DataDir)
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
