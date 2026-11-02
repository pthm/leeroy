package clientdata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Source is where game files come from, by their path in the client (World\Maps\Azeroth\Azeroth.wdt): a game
// install's archives (Client), or a directory they were extracted to (Dir).
type Source interface {
	Read(name string) ([]byte, error)
}

// Dir is a directory of extracted game files, each at its client path lowercased with / separators
// (world/maps/azeroth/azeroth.wdt), as Extract writes them.
type Dir string

func (d Dir) Read(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(string(d), fileKey(name)))
}

// Has reports whether the directory holds any extracted files.
func (d Dir) Has() bool {
	_, err := os.Stat(filepath.Join(string(d), "world"))
	return err == nil
}

// fileKey is a client path as a relative file path: lowercased, / separated (client paths are case-insensitive).
func fileKey(name string) string { return strings.ToLower(strings.ReplaceAll(name, `\`, "/")) }

// copier is a Source that copies every file it reads into a Dir: what the loaders read is what rendering needs.
type copier struct {
	src  Source
	out  string
	mu   sync.Mutex
	done map[string]bool
	n    int
	size int64
}

func (c *copier) Read(name string) ([]byte, error) {
	b, err := c.src.Read(name)
	if err != nil {
		return nil, err
	}
	k := fileKey(name)
	c.mu.Lock()
	seen := c.done[k]
	c.done[k] = true
	c.mu.Unlock()
	if !seen {
		p := filepath.Join(c.out, k)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.n, c.size = c.n+1, c.size+int64(len(b))
		c.mu.Unlock()
	}
	return b, nil
}

// ExtractTerrain copies into out (a Dir) everything the viewer's detail tiles read for a continent: its WDT and ADTs
// (terrain, texture layers, placements), the ground textures, and every placed model (WMOs with their groups and
// doodads, M2s with their skins) with its textures. Returns the files and bytes written.
func (c *Client) ExtractTerrain(mapID int, out string) (files int, bytes int64, err error) {
	dir, ok := Maps[mapID]
	if !ok {
		return 0, 0, fmt.Errorf("map %d is not a continent", mapID)
	}
	cp := &copier{src: c, out: out, done: map[string]bool{}}
	big := false
	if wdt, err := cp.Read(fmt.Sprintf(`World\Maps\%s\%s.wdt`, dir, dir)); err == nil && len(wdt) >= 24 {
		big = (uint32(wdt[20])|uint32(wdt[21])<<8)&0x84 != 0
	}
	var mu sync.Mutex
	models := map[string]bool{}
	first := func(name string) bool { // each model loaded once across the map
		mu.Lock()
		defer mu.Unlock()
		k := fileKey(name)
		if models[k] {
			return false
		}
		models[k] = true
		return true
	}
	textures := func(m *Model) {
		for _, mt := range m.Materials {
			cp.Read(mt.Texture)
		}
	}
	m2 := func(name string) {
		if !first(name) {
			return
		}
		if m, err := LoadM2(cp, name); err == nil {
			textures(m)
		}
	}
	_, err = parallel(64*64, func(i int) error {
		gx, gy := i/64, i%64
		b, err := cp.Read(fmt.Sprintf(`World\Maps\%s\%s_%d_%d.adt`, dir, dir, gy, gx))
		if err != nil {
			return nil
		}
		a, err := ParseADT(b, big)
		if err != nil {
			return nil
		}
		for _, t := range a.Textures {
			cp.Read(t)
		}
		for _, p := range a.WMOs {
			if !first(p.Name) {
				continue
			}
			w, err := LoadWMO(cp, p.Name)
			if err != nil {
				continue
			}
			textures(w)
			for _, d := range w.Doodads {
				m2(d.Name)
			}
		}
		for _, p := range a.Models {
			m2(p.Name)
		}
		return nil
	})
	return cp.n, cp.size, err
}
