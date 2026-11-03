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

// AreaTable is the area names and the zone each area belongs to (an area with no parent is its own zone), from the wowbench schema.
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
	rows := MustRows(Bench(), q, args...)
	out := make([]Label, 0, len(rows))
	for _, r := range rows {
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

var radii = map[int]float64{}

// CreatureRadii resolves creature entries to their size: the bounding radius in yards of their first model, times its
// scale, as the server uses it (0 when unknown), cached.
func CreatureRadii(ids []int) map[int]float64 {
	nameMu.Lock()
	defer nameMu.Unlock()
	var missing []int
	for _, id := range ids {
		if _, ok := radii[id]; !ok {
			missing = append(missing, id)
			radii[id] = 0
		}
	}
	if len(missing) > 0 {
		for _, r := range MustRows(World(), `SELECT m.CreatureID, i.BoundingRadius * m.DisplayScale FROM creature_template_model m
			JOIN creature_model_info i ON i.DisplayID = m.CreatureDisplayID WHERE m.Idx = 0 AND m.CreatureID IN (`+idList(missing)+")") {
			radii[atoi(r[0])] = atof(r[1])
		}
	}
	out := make(map[int]float64, len(ids))
	for _, id := range ids {
		out[id] = radii[id]
	}
	return out
}

var detect = map[int]float64{}

// CreatureDetection resolves creature entries to their detection range in yards (creature_template.detection_range,
// 20 for most): the aggro radius against a player of their own level, a yard less for each level the player is above
// them (Creature::GetAggroRange), cached.
func CreatureDetection(ids []int) map[int]float64 {
	nameMu.Lock()
	defer nameMu.Unlock()
	var missing []int
	for _, id := range ids {
		if _, ok := detect[id]; !ok {
			missing = append(missing, id)
			detect[id] = 20
		}
	}
	if len(missing) > 0 {
		for _, r := range MustRows(World(), "SELECT entry, detection_range FROM creature_template WHERE entry IN ("+idList(missing)+")") {
			detect[atoi(r[0])] = atof(r[1])
		}
	}
	out := make(map[int]float64, len(ids))
	for _, id := range ids {
		out[id] = detect[id]
	}
	return out
}

var quests = map[int]string{}

// QuestTitles resolves quest ids to titles ("" when unknown), cached.
func QuestTitles(ids []int) map[int]string {
	nameMu.Lock()
	defer nameMu.Unlock()
	var missing []int
	for _, id := range ids {
		if _, ok := quests[id]; !ok {
			missing = append(missing, id)
			quests[id] = ""
		}
	}
	if len(missing) > 0 {
		for _, r := range MustRows(World(), "SELECT ID, LogTitle FROM quest_template WHERE ID IN ("+idList(missing)+")") {
			quests[atoi(r[0])] = r[1]
		}
	}
	out := make(map[int]string, len(ids))
	for _, id := range ids {
		out[id] = quests[id]
	}
	return out
}

// Achievement is an achievement's title and points, from Achievement.dbc.
type Achievement struct {
	Title  string
	Points int
}

// Achievements reads Achievement.dbc once (empty without extracted client data).
var Achievements = sync.OnceValue(func() map[int]Achievement {
	out := map[int]Achievement{}
	d, err := ReadDBC("Achievement.dbc")
	if err != nil {
		return out
	}
	for r := 0; r < d.Rows(); r++ {
		out[int(d.U(r, 0))] = Achievement{Title: d.S(r, 4), Points: int(d.U(r, 39))} // fields: id, ..., title (enUS) 4, ..., points 39
	}
	return out
})

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
