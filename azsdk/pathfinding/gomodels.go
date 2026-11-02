package pathfinding

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// GameObjectModels reads vmaps/GameObjectModels.dtree (LoadGameObjectModelList): a game object display id's
// collision model, the file name in the vmaps directory less ".vmo" (Transportship.wmo for a ship).
func GameObjectModels(vmapsDir string) (map[uint32]string, error) {
	b, err := os.ReadFile(filepath.Join(vmapsDir, "GameObjectModels.dtree"))
	if err != nil {
		return nil, err
	}
	if len(b) < 8 {
		return nil, errors.New("GameObjectModels.dtree: short")
	}
	le, out := binary.LittleEndian, map[uint32]string{}
	for p := 8; p+9 <= len(b); { // displayId, isWmo, name length, name, bounds (2 x 3 floats)
		id, n := le.Uint32(b[p:]), int(le.Uint32(b[p+5:]))
		p += 9
		if n >= 500 || p+n+24 > len(b) {
			return out, io.ErrUnexpectedEOF
		}
		out[id] = string(b[p : p+n])
		p += n + 24
	}
	return out, nil
}

// ModelOutline is a vmap model seen from above: the convex hull of its vertices' x and y, in the model's own axes
// (a ship's bow is -x).
func ModelOutline(file string) ([][2]float32, error) {
	var w WorldModel
	if err := w.readFile(file); err != nil {
		return nil, err
	}
	var pts [][2]float32
	for _, g := range w.groupModels {
		for _, v := range g.Vertices {
			pts = append(pts, [2]float32{v.X, v.Y})
		}
	}
	if len(pts) < 3 {
		return nil, errors.New("model has no outline")
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i][0] < pts[j][0] || pts[i][0] == pts[j][0] && pts[i][1] < pts[j][1] })
	cross := func(o, a, b [2]float32) float32 { return (a[0]-o[0])*(b[1]-o[1]) - (a[1]-o[1])*(b[0]-o[0]) }
	hull := make([][2]float32, 0, 2*len(pts)) // Andrew's monotone chain
	for _, half := range [2]int{0, 1} {
		start := len(hull)
		for k := range pts {
			p := pts[k]
			if half == 1 {
				p = pts[len(pts)-1-k]
			}
			for len(hull) >= start+2 && cross(hull[len(hull)-2], hull[len(hull)-1], p) <= 0 {
				hull = hull[:len(hull)-1]
			}
			hull = append(hull, p)
		}
		hull = hull[:len(hull)-1]
	}
	return hull, nil
}
