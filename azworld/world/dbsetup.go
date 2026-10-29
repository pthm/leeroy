package world

// Setup builds the `wowbench` schema that azworld reads: client DBC data the world DB lacks, plus
// precomputed joins (aowow-style). Needs data/dbc/*.dbc and data/maps (see README). Tables:
//  achievement, achievement_criteria, achievement_category, criteria_type: the achievement catalogue
//  area (AreaTable), map_overlay (explore_area criteria asset -> area ids), area_location (where each area
//    is) and area_cell (area of every ~33yd cell), from the 16x16 area grid in each data/maps/MMMXXYY.map
//  creature_spawn, gameobject_spawn: every spawn with its area and zone (the world DB leaves them 0)
//  faction (names), faction_template + reaction: whether a creature faction is hostile/neutral/friendly
//    to Alliance and Horde players
//  spell: names, ranks, descriptions, cast time, range, cooldowns, cost, item created

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// DBC is one client database file (WDBC): fixed-size records of 4-byte fields, then a string block.
type DBC struct {
	b          []byte
	n, size    int
	strs       int
	FieldCount int
}

// ReadDBC reads data/dbc/<name>.
func ReadDBC(name string) (*DBC, error) {
	b, err := os.ReadFile(filepath.Join(DataDir(), "dbc", name))
	if err != nil {
		return nil, err
	}
	if len(b) < 20 || string(b[:4]) != "WDBC" {
		return nil, fmt.Errorf("%s: not a DBC", name)
	}
	le := binary.LittleEndian
	d := &DBC{b: b, n: int(le.Uint32(b[4:])), FieldCount: int(le.Uint32(b[8:])), size: int(le.Uint32(b[12:]))}
	d.strs = 20 + d.n*d.size
	if d.FieldCount*4 != d.size {
		return nil, fmt.Errorf("%s: unexpected record size", name)
	}
	return d, nil
}

// Rows is the number of records.
func (d *DBC) Rows() int { return d.n }

func (d *DBC) off(r, i int) int { return 20 + r*d.size + i*4 }

// U, I, F and S read field i of record r as uint32, int32, float32 or a string (from the string block).
func (d *DBC) U(r, i int) uint32 { return binary.LittleEndian.Uint32(d.b[d.off(r, i):]) }
func (d *DBC) I(r, i int) int32  { return int32(d.U(r, i)) }
func (d *DBC) F(r, i int) float32 {
	return math.Float32frombits(d.U(r, i))
}
func (d *DBC) S(r, i int) string {
	p := d.strs + int(d.U(r, i))
	if p >= len(d.b) {
		return ""
	}
	end := bytes.IndexByte(d.b[p:], 0)
	if end < 0 {
		end = len(d.b) - p
	}
	return strings.ToValidUTF8(string(d.b[p:p+end]), "�")
}

func mustDBC(name string) *DBC {
	d, err := ReadDBC(name)
	if err != nil {
		panic(err)
	}
	return d
}

type sqlRow []any // int64 or string

// text is a DBC string as the schema stores it: without the \r of the client's \r\n line breaks (the mysql client
// the first version loaded through dropped them, and azworld's output has never had them).
func text(s string) string { return strings.ReplaceAll(s, "\r", "") }

func sqlValues(rows []sqlRow) string {
	var b strings.Builder
	for k, r := range rows {
		if k > 0 {
			b.WriteString(",\n")
		}
		b.WriteByte('(')
		for i, v := range r {
			if i > 0 {
				b.WriteByte(',')
			}
			switch x := v.(type) {
			case string:
				b.WriteString(sqlStr(x))
			default:
				fmt.Fprint(&b, x)
			}
		}
		b.WriteByte(')')
	}
	return b.String()
}

func chunked(rows []sqlRow, n int) [][]sqlRow {
	var out [][]sqlRow
	for i := 0; i < len(rows); i += n {
		out = append(out, rows[i:min(i+n, len(rows))])
	}
	return out
}

// jsSlice is s.slice(0, n) on UTF-16 code units, as JavaScript cuts strings.
func jsSlice(s string, n int) string {
	u := utf16.Encode([]rune(s))
	if len(u) <= n {
		return s
	}
	return string(utf16.Decode(u[:n]))
}

// Setup builds the schema (normally "wowbench"; another name builds a copy, e.g. to compare). It returns a summary
// line. repo is the repository root (for the server's DBCEnums.h).
func Setup(schema, repo string) (summary string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	U := func(d *DBC, r, f int) int64 { return int64(d.U(r, f)) }
	I := func(d *DBC, r, f int) int64 { return int64(d.I(r, f)) }

	// Field indices per AzerothCore DBCStructure.h / DBCfmt.h (enUS strings).
	var ach, crit, cat, area, overlay, factionName, reaction, spell []sqlRow
	d := mustDBC("Achievement.dbc")
	for r := 0; r < d.Rows(); r++ {
		ach = append(ach, sqlRow{U(d, r, 0), I(d, r, 1), I(d, r, 2), U(d, r, 3), text(d.S(r, 4)), text(d.S(r, 21)), U(d, r, 38), U(d, r, 39),
			U(d, r, 41), U(d, r, 60), U(d, r, 61)})
	}
	d = mustDBC("Achievement_Criteria.dbc")
	for r := 0; r < d.Rows(); r++ {
		crit = append(crit, sqlRow{U(d, r, 0), U(d, r, 1), U(d, r, 2), U(d, r, 3), U(d, r, 4), U(d, r, 5), U(d, r, 6), U(d, r, 7),
			U(d, r, 8), text(d.S(r, 9)), U(d, r, 26), U(d, r, 29)})
	}
	d = mustDBC("Achievement_Category.dbc")
	for r := 0; r < d.Rows(); r++ {
		cat = append(cat, sqlRow{U(d, r, 0), I(d, r, 1), text(d.S(r, 2))})
	}
	d = mustDBC("AreaTable.dbc")
	for r := 0; r < d.Rows(); r++ {
		area = append(area, sqlRow{U(d, r, 0), U(d, r, 1), U(d, r, 2), I(d, r, 10), text(d.S(r, 11))})
	}
	d = mustDBC("WorldMapOverlay.dbc")
	for r := 0; r < d.Rows(); r++ {
		overlay = append(overlay, sqlRow{U(d, r, 0), U(d, r, 2), U(d, r, 3), U(d, r, 4), U(d, r, 5)})
	}
	fa := mustDBC("Faction.dbc")
	repFaction := map[uint32]int{} // reputation factions -> record
	for r := 0; r < fa.Rows(); r++ {
		factionName = append(factionName, sqlRow{U(fa, r, 0), text(fa.S(r, 23))})
		if fa.I(r, 1) >= 0 {
			repFaction[fa.U(r, 0)] = r
		}
	}
	// A new character's standing with a reputation faction (FactionEntry base rep for its race), as a reaction:
	// ReputationMgr ranks, hostile below -3000. ponytail: class masks and at-war flags ignored
	repReaction := func(r int, raceBit uint32) string {
		v := int32(0)
		for k := 0; k < 4; k++ {
			if fa.U(r, 2+k)&raceBit != 0 {
				v = fa.I(r, 10+k)
				break
			}
		}
		switch {
		case v < -3000:
			return "hostile"
		case v < 0:
			return "unfriendly"
		case v < 3000:
			return "neutral"
		}
		return "friendly"
	}
	type ftEntry struct {
		id, faction, our, friend, hostile uint32
		enemies, friends                  [4]uint32
	}
	ftd := mustDBC("FactionTemplate.dbc")
	var ft []ftEntry
	ftByID := map[uint32]ftEntry{}
	for r := 0; r < ftd.Rows(); r++ {
		t := ftEntry{id: ftd.U(r, 0), faction: ftd.U(r, 1), our: ftd.U(r, 3), friend: ftd.U(r, 4), hostile: ftd.U(r, 5)}
		for k := 0; k < 4; k++ {
			t.enemies[k], t.friends[k] = ftd.U(r, 6+k), ftd.U(r, 10+k)
		}
		ft = append(ft, t)
		ftByID[t.id] = t
	}
	has := func(a [4]uint32, v uint32) bool { return a[0] == v || a[1] == v || a[2] == v || a[3] == v }
	// FactionTemplateEntry::IsHostileTo / IsFriendlyTo against the player templates (1 Human, 2 Orc)
	reactionTo := func(c, p ftEntry) string {
		switch {
		case c.id == p.id:
			return "friendly"
		case has(c.enemies, p.faction):
			return "hostile"
		case has(c.friends, p.faction):
			return "friendly"
		case c.hostile&p.our != 0:
			return "hostile"
		case c.friend&p.our != 0 || c.our&p.friend != 0:
			return "friendly"
		}
		return "neutral"
	}
	// Unit::GetFactionReactionTo: reputation decides for reputation factions, templates otherwise
	for _, t := range ft {
		if r, ok := repFaction[t.faction]; ok {
			reaction = append(reaction, sqlRow{int64(t.id), int64(t.faction), repReaction(r, 1), repReaction(r, 2)})
		} else {
			reaction = append(reaction, sqlRow{int64(t.id), int64(t.faction), reactionTo(t, ftByID[1]), reactionTo(t, ftByID[2])})
		}
	}
	castTime, rng := map[uint32]int64{}, map[uint32]float64{}
	d = mustDBC("SpellCastTimes.dbc")
	for r := 0; r < d.Rows(); r++ {
		castTime[d.U(r, 0)] = I(d, r, 1)
	}
	d = mustDBC("SpellRange.dbc")
	for r := 0; r < d.Rows(); r++ {
		rng[d.U(r, 0)] = float64(d.F(r, 3)) // max hostile range
	}
	d = mustDBC("Spell.dbc")
	for r := 0; r < d.Rows(); r++ {
		created := int64(0)
		for e := 0; e < 3; e++ {
			if d.U(r, 71+e) == 24 { // SPELL_EFFECT_CREATE_ITEM
				created = U(d, r, 107+e)
				break
			}
		}
		spell = append(spell, sqlRow{U(d, r, 0), text(d.S(r, 136)), text(d.S(r, 153)), jsSlice(text(d.S(r, 170)), 1000), U(d, r, 39), castTime[d.U(r, 28)],
			int64(jsRound(rng[d.U(r, 46)])), U(d, r, 29), U(d, r, 30), I(d, r, 41), U(d, r, 42), created})
	}

	// Cell (lx, ly) of grid file (gx, gy) is centred at 533.33 * (32 - g - (l + 0.5) / 16), the inverse of
	// GridTerrainData::getArea. ponytail: maps 0 and 1 only (Eastern Kingdoms, Kalimdor).
	type areaPts struct {
		m, id int64
		pts   [][2]float64
	}
	cells := map[string]*areaPts{}
	var cellOrder []*areaPts
	var grid []sqlRow // map, ix, iy, area: ix = floor(16 * (32 - x / S)), as GridTerrainData::getArea
	dir, err := os.Open(filepath.Join(DataDir(), "maps"))
	if err != nil {
		return "", err
	}
	names, _ := dir.Readdirnames(-1) // directory order, as readdirSync (it decides ties in area_location below)
	dir.Close()
	mapFile := regexp.MustCompile(`^(\d{3})(\d{2})(\d{2})\.map$`)
	for _, f := range names {
		mm := mapFile.FindStringSubmatch(f)
		if mm == nil {
			continue
		}
		mp, _ := strconv.Atoi(mm[1])
		gx, _ := strconv.Atoi(mm[2])
		gy, _ := strconv.Atoi(mm[3])
		if mp > 1 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(DataDir(), "maps", f))
		if err != nil {
			return "", err
		}
		le := binary.LittleEndian
		off := int(le.Uint32(b[12:]))
		flags, whole := le.Uint16(b[off+4:]), le.Uint16(b[off+6:])
		for lx := 0; lx < 16; lx++ {
			for ly := 0; ly < 16; ly++ {
				id := whole
				if flags&1 == 0 {
					id = le.Uint16(b[off+8+(lx*16+ly)*2:])
				}
				if id == 0 {
					continue
				}
				grid = append(grid, sqlRow{int64(mp), int64(gx*16 + lx), int64(gy*16 + ly), int64(id)})
				k := fmt.Sprintf("%s:%d", mm[1], id)
				a := cells[k]
				if a == nil {
					a = &areaPts{m: int64(mp), id: int64(id)}
					cells[k] = a
					cellOrder = append(cellOrder, a)
				}
				a.pts = append(a.pts, [2]float64{GridSize * (32 - float64(gx) - (float64(lx)+0.5)/16), GridSize * (32 - float64(gy) - (float64(ly)+0.5)/16)})
			}
		}
	}
	var loc []sqlRow
	for _, a := range cellOrder {
		cx, cy := 0.0, 0.0
		for _, p := range a.pts {
			cx += p[0]
		}
		for _, p := range a.pts {
			cy += p[1]
		}
		cx, cy = cx/float64(len(a.pts)), cy/float64(len(a.pts))
		// a cell inside the area nearest its centroid (the centroid itself can fall outside, e.g. a ring)
		best := a.pts[0]
		lx, hx, ly, hy := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
		for k, p := range a.pts {
			if k > 0 && math.Hypot(p[0]-cx, p[1]-cy) < math.Hypot(best[0]-cx, best[1]-cy) {
				best = p
			}
			lx, hx, ly, hy = math.Min(lx, p[0]), math.Max(hx, p[0]), math.Min(ly, p[1]), math.Max(hy, p[1])
		}
		r := func(v float64) int64 { return int64(jsRound(v)) }
		loc = append(loc, sqlRow{a.id, a.m, r(best[0]), r(best[1]), r(lx), r(hx), r(ly), r(hy), int64(len(a.pts))})
	}

	src, err := os.ReadFile(filepath.Join(repo, "server/azerothcore/src/server/shared/DataStores/DBCEnums.h"))
	if err != nil {
		return "", err
	}
	body := string(src)
	body = body[strings.Index(body, "enum AchievementCriteriaTypes"):]
	body = body[:strings.Index(body, "};")]
	var types []sqlRow
	for _, m := range regexp.MustCompile(`ACHIEVEMENT_CRITERIA_TYPE_(\w+)\s*=\s*(\d+)`).FindAllStringSubmatch(body, -1) {
		n, _ := strconv.ParseInt(m[2], 10, 64)
		types = append(types, sqlRow{n, strings.ToLower(m[1])})
	}

	S := strconv.FormatFloat(GridSize, 'f', -1, 64)
	spawnTable := func(name, src, alias string) string {
		return fmt.Sprintf(`CREATE TABLE %s (guid INT PRIMARY KEY, id INT, map INT, x FLOAT, y FLOAT, z FLOAT, area INT, zone INT,
  KEY (id), KEY (map, x, y), KEY (zone)) AS
  SELECT %[3]s.guid, %[3]s.id, %[3]s.map, %[3]s.position_x x, %[3]s.position_y y, %[3]s.position_z z, COALESCE(g.area, 0) area,
    COALESCE(IF(a.zone = 0, a.id, a.zone), 0) zone
  FROM acore_world.%[2]s %[3]s
  LEFT JOIN area_cell g ON g.map = %[3]s.map AND g.ix = FLOOR(16 * (32 - %[3]s.position_x / %[4]s)) AND g.iy = FLOOR(16 * (32 - %[3]s.position_y / %[4]s))
  LEFT JOIN area a ON a.id = g.area`, name, src, alias, S)
	}
	stmts := []string{
		"DROP DATABASE IF EXISTS " + schema, "CREATE DATABASE " + schema, "USE " + schema,
		`CREATE TABLE achievement (id INT PRIMARY KEY, faction INT COMMENT '-1 any, 0 horde, 1 alliance',
  map INT COMMENT '-1 any', parent INT, name TEXT, description TEXT, category INT, points INT,
  flags INT COMMENT '0x1 = statistic counter, not earnable', count INT, ref_achievement INT)`,
		`CREATE TABLE achievement_criteria (id INT PRIMARY KEY, achievement INT, type INT COMMENT 'see criteria_type',
  asset INT COMMENT 'creature/quest/area/spell/item... id, by type', quantity INT,
  extra1_type INT, extra1_value INT, extra2_type INT, extra2_value INT, description TEXT,
  completion_flags INT, time_limit INT, KEY (achievement))`,
		`CREATE TABLE achievement_category (id INT PRIMARY KEY, parent INT, name TEXT)`,
		`CREATE TABLE criteria_type (id INT PRIMARY KEY, name TEXT)`,
		`CREATE TABLE area (id INT PRIMARY KEY, map INT, zone INT COMMENT 'parent zone area id, 0 = this is a zone',
  level INT, name TEXT)`,
		`CREATE TABLE map_overlay (id INT PRIMARY KEY COMMENT '= asset of explore_area criteria', area1 INT, area2 INT,
  area3 INT, area4 INT)`,
		`CREATE TABLE area_location (area INT, map INT, x INT COMMENT 'a point inside the area near its middle', y INT,
  min_x INT, max_x INT, min_y INT, max_y INT, cells INT COMMENT '~33x33yd cells', PRIMARY KEY (area, map))`,
		"INSERT INTO achievement VALUES " + sqlValues(ach),
		"INSERT INTO achievement_criteria VALUES " + sqlValues(crit),
		"INSERT INTO achievement_category VALUES " + sqlValues(cat),
		"INSERT INTO criteria_type VALUES " + sqlValues(types),
		"INSERT INTO area VALUES " + sqlValues(area),
		"INSERT INTO map_overlay VALUES " + sqlValues(overlay),
		"INSERT INTO area_location VALUES " + sqlValues(loc),
		`CREATE TABLE area_cell (map INT, ix INT, iy INT, area INT, PRIMARY KEY (map, ix, iy))`,
	}
	for _, c := range chunked(grid, 5000) {
		stmts = append(stmts, "INSERT INTO area_cell VALUES "+sqlValues(c))
	}
	stmts = append(stmts, `CREATE TABLE faction (id INT PRIMARY KEY, name TEXT)`,
		"INSERT INTO faction VALUES "+sqlValues(factionName),
		`CREATE TABLE reaction (faction_template INT PRIMARY KEY COMMENT '= creature_template.faction', faction INT,
  to_alliance VARCHAR(10) COMMENT 'to a new Human character: hostile (attacks on sight), unfriendly, neutral, friendly',
  to_horde VARCHAR(10) COMMENT 'to a new Orc character')`,
		"INSERT INTO reaction VALUES "+sqlValues(reaction),
		"CREATE TABLE spell (id INT PRIMARY KEY, name VARCHAR(255), `rank` TEXT, description TEXT, level INT, cast_ms INT,\n"+
			"  range_yd INT, cooldown_ms INT, category_cooldown_ms INT, power_type INT, cost INT,\n"+
			"  create_item INT COMMENT 'item made by this spell (crafting), 0 = none', KEY (name), KEY (create_item))")
	for _, c := range chunked(spell, 5000) {
		stmts = append(stmts, "INSERT INTO spell VALUES "+sqlValues(c))
	}
	// every spawn with its area and zone; the cell maths matches GridTerrainData::getArea
	stmts = append(stmts, spawnTable("creature_spawn", "creature", "c"), spawnTable("gameobject_spawn", "gameobject", "o"),
		"CREATE USER IF NOT EXISTS 'worldreader'@'%' IDENTIFIED BY 'worldreader'",
		"GRANT SELECT ON "+schema+".* TO 'worldreader'@'%'")

	// one connection: USE applies per connection
	ctx := context.Background()
	conn, err := DB(Admin, "").Conn(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return "", fmt.Errorf("%v\n%.300s", err, s)
		}
	}
	return fmt.Sprintf("loaded %d achievements, %d criteria, %d categories, %d criteria types, %d areas, %d overlays, "+
		"%d area locations, %d area cells, %d faction templates, %d spells", len(ach), len(crit), len(cat), len(types), len(area),
		len(overlay), len(loc), len(grid), len(reaction), len(spell)), nil
}
