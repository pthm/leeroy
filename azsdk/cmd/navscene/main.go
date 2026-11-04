// navscene: a walk through waypoints (the prepared paths, as move.to walks them) and the world around it, for drawing:
// the collision models (buildings, walls, trees), a terrain and water height grid and the navmesh's triangles in a
// box round the path, as little-endian float32 files beside a scene.json, and index.html (view.html), which draws them
// with three.js: serve the out dir (python3 -m http.server) and open it; the query string sets the camera
// (cam=x,y,z&look=x,y,z, as its corner shows while you orbit) and the rest (tclip, corridor, bloom, labels, hud, dpr).
// "ride" between waypoints is a lift, boat or portal: no walk, a straight line from where the last walk ended to the
// next waypoint. The blog's Stormwind render: -9090 415 92.5 -8836 490 110, at 1600x1000 with
// ?dpr=2&cam=330,250,110&look=40,0,0&tclip=122&hud=0&fov=33. Its banner, Blackrock Mountain from the Spire's door down the
// chains to Molten Core's level, NAVMARGIN=70 -7535 -1213 285 -7536 -1069 181, at 2400x1000 with
// ?dpr=1&cam=-164,99,262&look=-31,-42,-16&shift=0.055,0.05&hud=0&labels=0&width=1.5&topac=0&glass=0.06&edges=0.2&corridor=25
// (an orbited camera, then shifted to centre the route without changing the perspective).
// [NAVMAP=0 NAVMARGIN=60 NAVSTEP=2] go run ./azsdk/cmd/navscene <data dir> <out dir> x y z x y z [ride] [x y z ...]
package main

import (
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pthm/leeroy/azsdk/navigation"
	"github.com/pthm/leeroy/azsdk/pathfinding"
)

//go:embed view.html
var view []byte

func main() {
	if len(os.Args) < 9 {
		fmt.Fprintln(os.Stderr, "navscene <data dir> <out dir> x y z x y z [ride] [x y z ...]")
		os.Exit(2)
	}
	data, out := os.Args[1], os.Args[2]
	num := func(s string, def float64) float32 {
		if s == "" {
			return float32(def)
		}
		v, err := strconv.ParseFloat(s, 32)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bad number", s)
			os.Exit(2)
		}
		return float32(v)
	}
	m64, _ := strconv.Atoi(os.Getenv("NAVMAP"))
	m := uint32(m64)
	margin, step := num(os.Getenv("NAVMARGIN"), 60), num(os.Getenv("NAVSTEP"), 2)

	// Groups of waypoints walked in turn; a ride between groups.
	var groups [][]navigation.Point3D
	var cur []navigation.Point3D
	args := os.Args[3:]
	for len(args) > 0 {
		if args[0] == "ride" {
			groups, cur, args = append(groups, cur), nil, args[1:]
			continue
		}
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "waypoints are x y z")
			os.Exit(2)
		}
		cur, args = append(cur, navigation.Point3D{X: num(args[0], 0), Y: num(args[1], 0), Z: num(args[2], 0)}), args[3:]
	}
	groups = append(groups, cur)

	navi := navigation.NewEmbeddedNavigator(data)
	var points []navigation.Point3D
	legs := make([]int, 0, len(groups)) // points in each group's walk
	for _, g := range groups {
		if len(g) < 2 {
			fmt.Fprintln(os.Stderr, "each walk needs two waypoints")
			os.Exit(2)
		}
		n := 0
		for i := 1; i < len(g); i++ {
			res, err := navi.FindPath(m, g[i-1], g[i])
			if err != nil || res == nil || !res.Found || len(res.Points) == 0 {
				fmt.Fprintln(os.Stderr, "no path from", g[i-1], "to", g[i], err)
				os.Exit(1)
			}
			points, n = append(points, res.Points...), n+len(res.Points)
		}
		legs = append(legs, n)
	}
	start, dest := groups[0][0], groups[len(groups)-1][len(groups[len(groups)-1])-1]
	xMin, xMax, yMin, yMax := float32(math.Inf(1)), float32(math.Inf(-1)), float32(math.Inf(1)), float32(math.Inf(-1))
	path := make([]float32, 0, 3*len(points))
	length := 0.0
	for i, p := range points {
		path = append(path, p.X, p.Y, p.Z)
		xMin, xMax, yMin, yMax = min(xMin, p.X), max(xMax, p.X), min(yMin, p.Y), max(yMax, p.Y)
		if i > 0 {
			length += float64(p.DistanceTo(points[i-1]))
		}
	}
	xMin, xMax, yMin, yMax = xMin-margin, xMax+margin, yMin-margin, yMax+margin

	var models []float32
	vm := pathfinding.NewVMapManager(filepath.Join(data, "vmaps"))
	vm.Triangles(m, xMin, xMax, yMin, yMax, func(a, b, c pathfinding.Vector3) {
		models = append(models, a.X, a.Y, a.Z, b.X, b.Y, b.Z, c.X, c.Y, c.Z)
	})

	// Terrain and water on a grid, row by row from (xMin, yMin): NaN where there's none.
	tm := pathfinding.NewTerrainManager(filepath.Join(data, "maps"))
	nx, ny := int((xMax-xMin)/step)+1, int((yMax-yMin)/step)+1
	terrain, water := make([]float32, 0, nx*ny), make([]float32, 0, nx*ny)
	nan := float32(math.NaN())
	for j := range ny {
		for i := range nx {
			x, y := xMin+float32(i)*step, yMin+float32(j)*step
			h, ok := tm.GetHeight(m, x, y, 1e5)
			if !ok {
				h = nan
			}
			w, ok := tm.GetWaterLevel(m, x, y)
			if !ok {
				w = nan
			}
			terrain, water = append(terrain, h), append(water, w)
		}
	}

	var nav []float32
	mm := pathfinding.NewMMapManager(filepath.Join(data, "mmaps"))
	gx0, gy0 := int(32-xMax/(1600.0/3)), int(32-yMax/(1600.0/3))
	gx1, gy1 := int(32-xMin/(1600.0/3)), int(32-yMin/(1600.0/3))
	for gx := gx0; gx <= gx1; gx++ {
		for gy := gy0; gy <= gy1; gy++ {
			tris, err := mm.SquareTriangles(m, gx, gy)
			if err != nil {
				continue
			}
			for _, t := range tris {
				if max(t[0].X, t[1].X, t[2].X) < xMin || min(t[0].X, t[1].X, t[2].X) > xMax ||
					max(t[0].Y, t[1].Y, t[2].Y) < yMin || min(t[0].Y, t[1].Y, t[2].Y) > yMax {
					continue
				}
				nav = append(nav, t[0].X, t[0].Y, t[0].Z, t[1].X, t[1].Y, t[1].Z, t[2].X, t[2].Y, t[2].Z)
			}
		}
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for name, v := range map[string][]float32{"path": path, "models": models, "terrain": terrain, "water": water, "navmesh": nav} {
		fh, err := os.Create(filepath.Join(out, name+".f32"))
		if err == nil {
			err = binary.Write(fh, binary.LittleEndian, v)
			if cerr := fh.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	meta := map[string]any{
		"map": m, "start": start, "dest": dest, "length": length, "points": len(points), "legs": legs,
		"bounds": []float32{xMin, xMax, yMin, yMax}, "grid": map[string]any{"nx": nx, "ny": ny, "step": step},
		"triangles": map[string]int{"models": len(models) / 9, "navmesh": len(nav) / 9},
	}
	b, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "index.html"), view, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(out, "scene.json"), b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}
