package session

import (
	"strings"
	"testing"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// bearing and clock from you facing north (o = 0; x runs north, y west), and facing east
func TestRelate(t *testing.T) {
	for _, c := range []struct {
		x, y, o      float64
		bearing      float64
		clock        int
		dz, z, wantZ float64
	}{
		{10, 0, 0, 0, 12, 0, 5, 5},        // ahead, 5 above
		{0, -10, 0, 90, 3, 0, -12, -12},   // east: to your right, 12 below
		{0, 10, 0, -90, 9, 0, 0, 0},       // west: left
		{-10, 0, 0, 180, 6, 0, 0, 0},      // behind
		{10, -10, 0, 45, 2, 0, 0, 0},      // north-east: between 1 and 2 o'clock, rounds to 2
		{0, -10, -1.5708, 0, 12, 0, 0, 0}, // facing east (o = -pi/2), east is ahead
	} {
		p := types.Place{X: c.x, Y: c.y, Z: c.z}
		relate(0, 0, 0, c.o, &p)
		if p.Bearing != c.bearing || p.Clock != c.clock || p.DZ != c.wantZ {
			t.Errorf("(%g, %g) facing %g: bearing %g clock %d dz %g, want %g %d %g", c.x, c.y, c.o, p.Bearing, p.Clock, p.DZ, c.bearing, c.clock, c.wantZ)
		}
	}
}

// the server's aggro radius: 20 yd at equal level, a yard per level, capped 25 below, at least 5
func TestAggroRadius(t *testing.T) {
	for _, c := range []struct {
		creature, you uint32
		want          float64
	}{{6, 1, 25}, {5, 5, 20}, {1, 10, 11}, {1, 40, 5}, {60, 1, 45}} {
		if got := aggroRadius(c.creature, c.you); got != c.want {
			t.Errorf("aggroRadius(%d, %d) = %g, want %g", c.creature, c.you, got, c.want)
		}
	}
}

// a small map: you in the middle, a hostile north-east with its aggro range, no floor past 6 yd north
func TestRenderLook(t *testing.T) {
	units := []lookUnit{{label: 'A', name: "Kobold Tunneler", kind: "hostile", level: 6, x: 4, y: -4, z: -14, distance: 15.5, clock: 2,
		dz: -14, aggro: 3, inAggro: true}}
	out := renderLook(me3{}, 1, 8, units, func(x, y float64) (float64, bool) { return 0, x <= 6 })
	lines := strings.Split(out, "\n")
	grid := lines[3:8] // after the three header lines: 5 rows of 9
	want := []string{
		"#########", // x = 8: no floor
		".....:A:.", // x = 4: A at y = -4 (2 cells east of you), its 3 yd aggro range either side
		"....@....", // x = 0: 4 yd from A, outside its range
		".........",
		".........",
	}
	for i := range want {
		if grid[i] != want[i] {
			t.Errorf("row %d = %q, want %q\n%s", i, grid[i], want[i], out)
		}
	}
	if !strings.Contains(out, "A Kobold Tunneler L6 hostile, 16 yd NE (2 o'clock), 14 yd below, aggro 3 yd (you're inside)") ||
		!strings.Contains(out, "1 hostile(s) have you in aggro range") {
		t.Errorf("legend:\n%s", out)
	}
}

// danger lists the hostiles you're in or near the aggro range of, nearest edge first, with their packs; one out of
// sight (in the mine below) doesn't count as having you in range
func TestDanger(t *testing.T) {
	no := false
	mob := func(guid uint64, x, dist, aggro float64) types.Creature {
		return types.Creature{Unit: types.Unit{GUID: types.GUID(guid), Name: "Kobold", Level: 6, Place: types.Place{X: x, Distance: dist}}, AggroRadius: aggro}
	}
	hostile := []types.Creature{
		mob(1, 20, 20, 25), // you're 5 yd inside its range
		mob(2, 24, 24, 25), // 1 yd inside, 4 yd from mob 1: its pack
		mob(3, 60, 60, 25), // 35 yd from its edge: not listed
		mob(4, -10, 10, 25),
	}
	out, in, margin := danger(hostile, func(d *types.Danger) {
		if d.GUID == 4 {
			d.InSight = &no
		}
	})
	if len(out) != 3 || out[0].GUID != 4 || out[1].GUID != 1 || out[2].GUID != 2 {
		t.Fatalf("danger order: %+v", out)
	}
	if out[1].Pack != 1 || out[2].Pack != 1 {
		t.Errorf("packs: %d %d, want 1 each (mobs 1 and 2 together)", out[1].Pack, out[2].Pack)
	}
	if out[0].InAggro || !out[1].InAggro || in != 2 || margin == nil || *margin != -15 {
		t.Errorf("in aggro: %v %v, count %d, margin %v", out[0].InAggro, out[1].InAggro, in, *margin)
	}
}
