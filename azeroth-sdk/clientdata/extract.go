package clientdata

import (
	"bufio"
	"bytes"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Maps: the continents (map id -> the client's directory name).
var Maps = map[int]string{0: "Azeroth", 1: "Kalimdor", 530: "Expansion01", 571: "Northrend"}

// RoadTexture: ground textures that are roads or paved paths (cobblestone streets, stone and dirt roads, brick).
var RoadTexture = regexp.MustCompile(`(?i)(road|cobble|brick)`)

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
			fmt.Sscan(m[1], &col)
			fmt.Sscan(m[2], &row)
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
func (c *Client) Roads(mapID int, out string) (int, error) {
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
			if RoadTexture.MatchString(n) && !strings.Contains(strings.ToLower(n), "gravel") {
				roadTex[uint32(t)] = true
			}
		}
		if len(roadTex) == 0 {
			return nil
		}
		// 1024x1024 alpha points per square (16 chunks x 64), averaged into RoadCells cells
		const per = 1024 / RoadCells
		sum := make([]int, RoadCells*RoadCells)
		any := false
		for ci, ch := range a.Chunks {
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
		if !any {
			return nil
		}
		cells := make([]byte, RoadCells*RoadCells)
		for k, s := range sum {
			cells[k] = byte(min(255, s/(per*per)))
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
