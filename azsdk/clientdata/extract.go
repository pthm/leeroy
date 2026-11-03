package clientdata

import (
	"bufio"
	"bytes"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/pthm/leeroy/azsdk/pathfinding"
)

// Maps holds the continents (map id -> the client's directory name).
var Maps = map[int]string{0: "Azeroth", 1: "Kalimdor", 530: "Expansion01", 571: "Northrend"}

// RoadTexture matches ground textures that are roads or paved paths (cobblestone streets, stone and dirt roads, brick), and
// the ones a zone paints its roads with under another name (found by their shape: long thin strips): Ghostlands'
// path, Hellfire's Path of Glory (bones), Winterspring's roads (its dirt), Tanaris's trails (footprints).
var RoadTexture = regexp.MustCompile(`(?i)(road|cobble|brick|ghostlandspath|outlandbones|winterspringdirt|tanarisdirtfootprint)`)

// notRoad: textures RoadTexture matches that aren't roads: gravel verges, and the brick of Ahn'Qiraj's plazas
var notRoad = regexp.MustCompile(`(?i)(gravel|burningsteppsbrick01)`)

// roadModel: models that carry roads over water and chasms (no ground texture there), drawn into the road rasters;
// not the bricks, lamps, braziers and statues named after bridges
var (
	roadModel    = regexp.MustCompile(`(?i)bridge`)
	notRoadModel = regexp.MustCompile(`(?i)(brick|brazier|lantern|statue|lamp|teleporter)`)
)

// Minimap writes <out>/<map id>/<gx>_<gy>.png: the client's minimap image of each 533-yard grid square (gx from x,
// gy from y, as the server's .map files), north up.
func (c *Client) Minimap(mapID int, out string) (int, error) {
	dir := Maps[mapID]
	trs, err := c.Read(`textures\Minimap\md5translate.trs`)
	if err != nil {
		return 0, err
	}
	re := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(dir) + `\\map(\d+)_(\d+)\.blp\t(\S+)`)
	type job struct {
		gx, gy int
		file   string
	}
	jobs := []job{}
	sc := bufio.NewScanner(bytes.NewReader(trs))
	for sc.Scan() {
		if m := re.FindStringSubmatch(strings.TrimSpace(sc.Text())); m != nil {
			var col, row int
			_, _ = fmt.Sscan(m[1], &col) // the pattern matched digits
			_, _ = fmt.Sscan(m[2], &row)
			jobs = append(jobs, job{gx: row, gy: col, file: `textures\Minimap\` + m[3]})
		}
	}
	if err := os.MkdirAll(filepath.Join(out, fmt.Sprint(mapID)), 0o755); err != nil {
		return 0, err
	}
	return parallel(len(jobs), func(i int) error {
		j := jobs[i]
		b, err := c.Read(j.file)
		if err != nil {
			return nil // listed but missing: skip
		}
		img, err := DecodeBLP(b)
		if err != nil {
			return fmt.Errorf("%s: %w", j.file, err)
		}
		f, err := os.Create(filepath.Join(out, fmt.Sprint(mapID), fmt.Sprintf("%d_%d.png", j.gx, j.gy)))
		if err != nil {
			return err
		}
		defer f.Close()
		return png.Encode(f, img)
	})
}

// RoadCells is the side of each square's road raster: 128 cells of ~4.2 yards.
const RoadCells = 128

// Roads writes <out>/<map id>/<gx>_<gy>.bin for every square with roads: RoadCells x RoadCells bytes, row-major from
// the north-west corner (row = x falling, column = y falling, as the minimap), each the share of the cell covered by
// road textures (0-255).
func (c *Client) Roads(mapID int, out string) (int, error) { return Roads(c, mapID, out) }

// Roads is Client.Roads from any source of game files (an install, or an extracted Dir).
func Roads(c Source, mapID int, out string) (int, error) {
	dir := Maps[mapID]
	big := false
	if wdt, err := c.Read(fmt.Sprintf(`World\Maps\%s\%s.wdt`, dir, dir)); err == nil && len(wdt) >= 24 {
		big = (uint32(wdt[20])|uint32(wdt[21])<<8)&0x84 != 0
	}
	if err := os.MkdirAll(filepath.Join(out, fmt.Sprint(mapID)), 0o755); err != nil {
		return 0, err
	}
	written := 0
	var mu sync.Mutex
	_, err := parallel(64*64, func(i int) error {
		gx, gy := i/64, i%64 // the ADT is <dir>_<col>_<row>: col from y (gy), row from x (gx)
		b, err := c.Read(fmt.Sprintf(`World\Maps\%s\%s_%d_%d.adt`, dir, dir, gy, gx))
		if err != nil {
			return nil
		}
		a, err := ParseADT(b, big)
		if err != nil {
			return nil
		}
		roadTex := map[uint32]bool{}
		for t, n := range a.Textures {
			if RoadTexture.MatchString(n) && !notRoad.MatchString(n) {
				roadTex[uint32(t)] = true
			}
		}
		// 1024x1024 alpha points per square (16 chunks x 64), averaged into RoadCells cells
		const per = 1024 / RoadCells
		sum := make([]int, RoadCells*RoadCells)
		any := false
		for ci, ch := range &a.Chunks {
			cx, cy := ci%16, ci/16 // chunk column (y falling), row (x falling)
			for t := range roadTex {
				found := false
				for _, l := range ch.Layers {
					found = found || l == t
				}
				if !found {
					continue
				}
				for py := 0; py < 64; py++ {
					for px := 0; px < 64; px++ {
						v := int(ch.Coverage(t, px, py))
						if v == 0 {
							continue
						}
						any = true
						r, col := (cy*64+py)/per, (cx*64+px)/per
						sum[r*RoadCells+col] += v
					}
				}
			}
		}
		cells := make([]byte, RoadCells*RoadCells)
		for k, s := range sum {
			cells[k] = byte(min(255, s/(per*per)))
		}
		if bridges(c, a, gx, gy, cells) {
			any = true
		}
		if !any {
			return nil
		}
		mu.Lock()
		written++
		mu.Unlock()
		return os.WriteFile(filepath.Join(out, fmt.Sprint(mapID), fmt.Sprintf("%d_%d.bin", gx, gy)), cells, 0o644)
	})
	return written, err
}

func parallel(n int, fn func(i int) error) (int, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	done := 0
	work := make(chan int)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				err := fn(i)
				mu.Lock()
				if err != nil && first == nil {
					first = err
				}
				if err == nil {
					done++
				}
				mu.Unlock()
			}
		}()
	}
	for i := 0; i < n; i++ {
		work <- i
	}
	close(work)
	wg.Wait()
	return done, first
}

// bridges draws the walkable deck of every bridge placed in square gx, gy into its road cells (full cover): the
// near-flat triangles (normal within 45 degrees of up) of the model, placed. True if it drew any.
func bridges(c Source, a *ADT, gx, gy int, cells []byte) bool {
	const size = 533.33333
	cell := size / RoadCells
	x0, y0 := size*float64(32-gx), size*float64(32-gy) // the square's north-west corner (x and y fall from it)
	drew := false
	for k, list := range [2][]Placement{a.WMOs, a.Models} {
		for _, pl := range list {
			if !roadModel.MatchString(pl.Name) || notRoadModel.MatchString(pl.Name) {
				continue
			}
			var m *Model
			var err error
			if k == 0 {
				m, err = LoadWMO(c, pl.Name)
			} else {
				m, err = LoadM2(c, pl.Name)
			}
			if err != nil || m == nil {
				continue
			}
			tw := pathfinding.PlacementWorld(pl.Pos, pl.Rot, pl.Scale)
			w := make([][3]float64, len(m.Verts))
			for i, v := range m.Verts {
				p := tw(pathfinding.Vector3{X: v[0], Y: v[1], Z: v[2]})
				w[i] = [3]float64{float64(p.X), float64(p.Y), float64(p.Z)}
			}
			for _, t := range m.Tris {
				p, q, r := w[t[0]], w[t[1]], w[t[2]]
				ux, uy, uz := q[0]-p[0], q[1]-p[1], q[2]-p[2]
				vx, vy, vz := r[0]-p[0], r[1]-p[1], r[2]-p[2]
				nx, ny, nz := uy*vz-uz*vy, uz*vx-ux*vz, ux*vy-uy*vx
				if l := math.Sqrt(nx*nx + ny*ny + nz*nz); l == 0 || math.Abs(nz)/l < 0.7 {
					continue // a wall, a railing's side
				}
				// the cells whose centres the triangle covers (rows from x, columns from y)
				rmin := int(math.Floor((x0 - max(p[0], q[0], r[0])) / cell))
				rmax := int(math.Ceil((x0 - min(p[0], q[0], r[0])) / cell))
				cmin := int(math.Floor((y0 - max(p[1], q[1], r[1])) / cell))
				cmax := int(math.Ceil((y0 - min(p[1], q[1], r[1])) / cell))
				for row := max(0, rmin); row <= min(RoadCells-1, rmax); row++ {
					for col := max(0, cmin); col <= min(RoadCells-1, cmax); col++ {
						x, y := x0-(float64(row)+0.5)*cell, y0-(float64(col)+0.5)*cell
						if inTriangle(x, y, p, q, r) {
							cells[row*RoadCells+col] = 255
							drew = true
						}
					}
				}
			}
		}
	}
	return drew
}

func inTriangle(x, y float64, a, b, c [3]float64) bool {
	d1 := (x-b[0])*(a[1]-b[1]) - (a[0]-b[0])*(y-b[1])
	d2 := (x-c[0])*(b[1]-c[1]) - (b[0]-c[0])*(y-c[1])
	d3 := (x-a[0])*(c[1]-a[1]) - (c[0]-a[0])*(y-a[1])
	neg, pos := d1 < 0 || d2 < 0 || d3 < 0, d1 > 0 || d2 > 0 || d3 > 0
	return !neg || !pos
}
