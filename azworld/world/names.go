package world

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
func atof(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func idList(ids []int) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = strconv.Itoa(id)
	}
	return strings.Join(s, ",")
}

// Area names and the zone each area belongs to (an area with no parent is its own zone), from the wowbench schema.
type AreaTable struct {
	ZoneOf map[int]int
	Names  map[int]string
}

var (
	areaMu    sync.Mutex
	areaCache = map[uint32]*AreaTable{}
)

// Areas is the area table of one map (cached).
func Areas(m uint32) *AreaTable {
	areaMu.Lock()
	defer areaMu.Unlock()
	if a := areaCache[m]; a != nil {
		return a
	}
	a := &AreaTable{ZoneOf: map[int]int{}, Names: map[int]string{}}
	for _, r := range MustRows(Bench(), "SELECT id, zone, name FROM area WHERE map = ?", m) {
		id, zone := atoi(r[0]), atoi(r[1])
		if zone == 0 {
			zone = id
		}
		a.ZoneOf[id], a.Names[id] = zone, r[2]
	}
	areaCache[m] = a
	return a
}

// Label is a zone or sub-area name at its centre (only areas big enough to name).
type Label struct {
	ID   int     `json:"id"`
	Name string  `json:"name"`
	Zone bool    `json:"zone"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Map  uint32  `json:"map"`
}

// Labels of one map, optionally inside a box {xMin, xMax, yMin, yMax}.
func Labels(m uint32, box *[4]float64) []Label {
	q := `SELECT a.id, a.name, a.zone = 0, l.x, l.y FROM area_location l JOIN area a ON a.id = l.area AND a.map = l.map
		WHERE l.map = ? AND l.cells >= 8`
	args := []any{m}
	if box != nil {
		q += " AND l.x BETWEEN ? AND ? AND l.y BETWEEN ? AND ?"
		args = append(args, box[0], box[1], box[2], box[3])
	}
	out := []Label{}
	for _, r := range MustRows(Bench(), q, args...) {
		out = append(out, Label{ID: atoi(r[0]), Name: r[1], Zone: r[2] == "1", X: atof(r[3]), Y: atof(r[4]), Map: m})
	}
	return out
}

var (
	nameMu    sync.Mutex
	creatures = map[int]string{}
)

// CreatureNames resolves creature entries to names ("#<entry>" when unknown), cached.
func CreatureNames(ids []int) map[int]string {
	nameMu.Lock()
	defer nameMu.Unlock()
	var missing []int
	for _, id := range ids {
		if _, ok := creatures[id]; !ok {
			missing = append(missing, id)
			creatures[id] = fmt.Sprintf("#%d", id)
		}
	}
	if len(missing) > 0 {
		for _, r := range MustRows(World(), "SELECT entry, name FROM creature_template WHERE entry IN ("+idList(missing)+")") {
			creatures[atoi(r[0])] = r[1]
		}
	}
	out := make(map[int]string, len(ids))
	for _, id := range ids {
		out[id] = creatures[id]
	}
	return out
}

// ItemName is an item's name and quality (0 poor ... 5 legendary).
type ItemName struct {
	Name string `json:"name"`
	Q    int    `json:"q"`
}

// ItemNames resolves item entries.
func ItemNames(ids []int) map[string]ItemName {
	out := map[string]ItemName{}
	if len(ids) == 0 {
		return out
	}
	for _, r := range MustRows(World(), "SELECT entry, name, Quality FROM item_template WHERE entry IN ("+idList(ids)+")") {
		out[r[0]] = ItemName{Name: r[1], Q: atoi(r[2])}
	}
	return out
}
