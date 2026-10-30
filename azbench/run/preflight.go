package run

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pthm/leeroy/azbench/config"
	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azworld/world"
)

type Finding struct {
	Check    string `json:"check"`
	Status   string `json:"status"` // verified, warning, unknown
	Agent    string `json:"agent,omitempty"`
	Message  string `json:"message"`
	Evidence any    `json:"evidence,omitempty"`
}
type Preflight struct {
	Schema    int       `json:"schema"`
	Scenario  string    `json:"scenario"`
	Goal      string    `json:"goal"`
	CheckedAt string    `json:"checked_at"`
	Findings  []Finding `json:"findings"`
}
type preflightSource struct {
	skill func(int) (string, bool)
	query func(context.Context, string, ...any) ([]map[string]string, error)
	path  func(context.Context, uint32, navigation.Point3D, navigation.Point3D) (*navigation.PathResult, error)
}

func preflightRows(db *sql.DB) func(context.Context, string, ...any) ([]map[string]string, error) {
	return func(ctx context.Context, q string, args ...any) ([]map[string]string, error) {
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		out := []map[string]string{}
		for rows.Next() {
			if len(out) >= 256 {
				return nil, fmt.Errorf("preflight row limit exceeded")
			}
			raw := make([]sql.RawBytes, len(cols))
			ptr := make([]any, len(cols))
			for i := range raw {
				ptr[i] = &raw[i]
			}
			if err := rows.Scan(ptr...); err != nil {
				return nil, err
			}
			r := map[string]string{}
			for i, k := range cols {
				r[k] = string(raw[i])
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
}
func CheckScenario(repo string, s *config.Scenario) Preflight {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	nav := navigation.NewEmbeddedNavigator(filepath.Join(repo, "data"))
	defer nav.Close()
	return checkScenario(ctx, s, preflightSource{query: preflightRows(world.DB(world.Admin, "acore_world")), skill: func(id int) (string, bool) { return preflightSkill(filepath.Join(repo, "data/dbc/SkillLine.dbc"), id) }, path: nav.FindPathContext})
}
func checkScenario(ctx context.Context, s *config.Scenario, src preflightSource) Preflight {
	out := Preflight{Schema: 1, Scenario: s.Name, Goal: s.Goal, CheckedAt: time.Now().UTC().Format(time.RFC3339), Findings: []Finding{}}
	add := func(check, status, agent, message string, evidence any) {
		out.Findings = append(out.Findings, Finding{check, status, agent, message, evidence})
	}
	query := func(check, q string, args ...any) []map[string]string {
		r, err := src.query(ctx, q, args...)
		if err != nil {
			add(check, "unknown", "", "database evidence unavailable", map[string]any{"error": err.Error()})
			return nil
		}
		return r
	}
	number := func(r map[string]string, k string) int { v, _ := strconv.Atoi(r[k]); return v }
	floor := func(check string, m int, x, y, z float64) {
		p := navigation.Point3D{X: float32(x), Y: float32(y), Z: float32(z)}
		path, err := src.path(ctx, uint32(m), p, p)
		status, message := "unknown", "no confirmed navmesh floor at the requested 3D position"
		if err == nil && path != nil && path.Found && len(path.Points) > 0 {
			a, b := path.Points[0], path.Points[len(path.Points)-1]
			gap := func(q navigation.Point3D) float64 {
				return math.Sqrt(math.Pow(float64(q.X-p.X), 2) + math.Pow(float64(q.Y-p.Y), 2) + math.Pow(float64(q.Z-p.Z), 2))
			}
			if gap(a) <= 3 && gap(b) <= 3 {
				status, message = "verified", "requested floor has a navmesh location"
			}
		}
		add(check, status, "", message, map[string]any{"map": m, "x": x, "y": y, "z": z})
	}
	if s.Start != nil {
		p := s.Start
		floor("start_floor", p.Map, p.X, p.Y, p.Z)
	} else {
		add("start_floor", "unknown", "", "start inherits each character's saved position", nil)
	}
	parts := strings.Split(s.Goal, ":")
	id := 0
	if len(parts) > 1 {
		id, _ = strconv.Atoi(parts[1])
	}
	var quest map[string]string
	switch parts[0] {
	case "quest":
		rows := query("goal", `SELECT q.ID,q.MinLevel,q.AllowableRaces,COALESCE(a.AllowableClasses,0) AllowableClasses,COALESCE(a.PrevQuestID,0) PrevQuestID,COALESCE(a.MaxLevel,0) MaxLevel,COALESCE(a.RequiredSkillID,0) RequiredSkillID,COALESCE(a.RequiredMinRepFaction,0) RequiredMinRepFaction FROM quest_template q LEFT JOIN quest_template_addon a ON a.ID=q.ID WHERE q.ID=?`, id)
		if len(rows) == 0 {
			add("goal", "warning", "", "quest does not exist or could not be loaded", id)
		} else {
			quest = rows[0]
			add("goal", "verified", "", "quest template exists", quest)
		}
		for _, kind := range []string{"starter", "ender"} {
			rows := query("quest_"+kind, `SELECT 'npc' kind,r.id entry,COUNT(s.guid) spawns FROM creature_quest`+kind+` r LEFT JOIN wowbench.creature_spawn s ON s.id=r.id WHERE r.quest=? GROUP BY r.id UNION ALL SELECT 'object',r.id,COUNT(s.guid) FROM gameobject_quest`+kind+` r LEFT JOIN wowbench.gameobject_spawn s ON s.id=r.id WHERE r.quest=? GROUP BY r.id`, id, id)
			status := "verified"
			if len(rows) == 0 {
				status = "unknown"
			}
			for _, r := range rows {
				if number(r, "spawns") == 0 {
					status = "warning"
				}
			}
			add("quest_"+kind, status, "", "configured quest interaction spawns; scripted availability remains server-controlled", rows)
		}
		objects := query("quest_objects", `SELECT g.entry,g.name,COUNT(s.guid) spawns FROM quest_template q JOIN gameobject_template g ON -CAST(g.entry AS SIGNED) IN (q.RequiredNpcOrGo1,q.RequiredNpcOrGo2,q.RequiredNpcOrGo3,q.RequiredNpcOrGo4) LEFT JOIN wowbench.gameobject_spawn s ON s.id=g.entry WHERE q.ID=? GROUP BY g.entry,g.name`, id)
		for _, r := range objects {
			status := "verified"
			if number(r, "spawns") == 0 {
				status = "warning"
			}
			add("quest_object", status, "", "required object spawn evidence", r)
		}
		targets := query("combat_targets", `SELECT DISTINCT c.entry,c.name,c.minlevel,c.maxlevel,c.rank,ROUND(c.HealthModifier*(CASE c.exp WHEN 1 THEN st.basehp1 WHEN 2 THEN st.basehp2 ELSE st.basehp0 END)) estimated_health FROM quest_template q JOIN creature_template c ON c.entry IN (q.RequiredNpcOrGo1,q.RequiredNpcOrGo2,q.RequiredNpcOrGo3,q.RequiredNpcOrGo4) OR c.lootid IN (SELECT l.Entry FROM creature_loot_template l WHERE l.Item IN (q.RequiredItemId1,q.RequiredItemId2,q.RequiredItemId3,q.RequiredItemId4,q.RequiredItemId5,q.RequiredItemId6) AND l.Reference=0) LEFT JOIN creature_classlevelstats st ON st.level=c.minlevel AND st.class=c.unit_class WHERE q.ID=? LIMIT 128`, id)
		for _, r := range targets {
			add("combat_target", "verified", "", "template level/rank and estimated base health; server scaling and live stats may differ", r)
			for _, a := range s.Agents {
				if s.Level > 0 && number(r, "maxlevel") > s.Level {
					add("level_difference", "warning", a.Name, "higher-level target increases misses/resists and reduces effective damage; preparation time is not predictable", map[string]any{"target": r["entry"], "level_difference": number(r, "maxlevel") - s.Level, "minutes_available": s.Minutes})
				}
			}
		}
		triggers := query("exploration", `SELECT t.entry,t.map,t.x,t.y,t.z,t.radius FROM areatrigger_involvedrelation r JOIN areatrigger t ON t.entry=r.id WHERE r.quest=?`, id)
		for _, r := range triggers {
			val := func(k string) float64 { v, _ := strconv.ParseFloat(r[k], 64); return v }
			floor("exploration_floor", number(r, "map"), val("x"), val("y"), val("z"))
		}
	case "item":
		rows := query("goal", "SELECT entry,name FROM item_template WHERE entry=?", id)
		status := "verified"
		if len(rows) == 0 {
			status = "unknown"
		}
		add("goal", status, "", "item template lookup", rows)
	case "skill":
		name, known := "", false
		if src.skill != nil {
			name, known = src.skill(id)
		}
		status := "unknown"
		if known {
			status = "verified"
		}
		add("goal", status, "", "skill ID checked against local SkillLine.dbc", map[string]any{"id": id, "name": name})
	default:
		add("goal", "verified", "", "numeric judge goal validated by scenario parser", s.Goal)
	}
	for _, a := range s.Agents {
		race, _ := config.ID(config.Races, a.Race)
		class, _ := config.ID(config.Classes, a.Class)
		rows := query("starting_character", "SELECT guid,level,money FROM acore_characters.characters WHERE name=?", a.Char)
		level, money := s.Level, s.Money
		if len(rows) > 0 {
			if level == 0 {
				level = number(rows[0], "level")
			}
			if money == 0 {
				money = number(rows[0], "money")
			}
		}
		if level == 0 {
			level = 1
		}
		add("starting_resources", "verified", a.Name, "configured level and money, or existing character values; level changes do not train spells or upgrade gear", map[string]any{"level": level, "money": money, "minutes": s.Minutes})
		if len(rows) > 0 {
			gear := query("starting_gear", `SELECT i.slot,t.entry,t.name,t.ItemLevel,ii.durability FROM acore_characters.character_inventory i JOIN acore_characters.item_instance ii ON ii.guid=i.item JOIN item_template t ON t.entry=ii.itemEntry WHERE i.guid=? AND i.bag=0 AND i.slot<19 ORDER BY i.slot`, rows[0]["guid"])
			add("starting_gear", "verified", a.Name, "observed equipped items before scenario setup", gear)
		} else {
			add("starting_gear", "unknown", a.Name, "new character starter outfit is supplied at creation; no equipped snapshot exists yet", nil)
		}
		if quest != nil {
			status := "verified"
			reason := "level, race and class permit the quest; additional server conditions may apply"
			if level < number(quest, "MinLevel") || (number(quest, "MaxLevel") > 0 && level > number(quest, "MaxLevel")) || (number(quest, "AllowableRaces") > 0 && number(quest, "AllowableRaces")&(1<<(race-1)) == 0) || (number(quest, "AllowableClasses") > 0 && number(quest, "AllowableClasses")&(1<<(class-1)) == 0) {
				status, reason = "warning", "configured character fails level, race or class requirements"
			}
			if status == "verified" && (number(quest, "PrevQuestID") != 0 || number(quest, "RequiredSkillID") != 0 || number(quest, "RequiredMinRepFaction") != 0) {
				status, reason = "unknown", "prerequisite quest, profession or reputation conditions require character progress evidence"
			}
			add("quest_eligibility", status, a.Name, reason, quest)
		}
	}
	add("preparation_time", "unknown", "", "available minutes are reported; travel, training, leveling and encounter success depend on agent choices and server state", map[string]any{"minutes": s.Minutes})
	return out
}
func SavePreflight(path string, p Preflight) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}

func preflightSkill(path string, id int) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
		return "", false
	}
	n, size := uint64(binary.LittleEndian.Uint32(b[4:])), uint64(binary.LittleEndian.Uint32(b[12:]))
	end := 20 + n*size
	if size < 19*4 || end > uint64(len(b)) {
		return "", false
	}
	for i := uint64(0); i < n; i++ {
		r := b[20+i*size : 20+(i+1)*size]
		if binary.LittleEndian.Uint32(r) != uint32(id) {
			continue
		}
		for locale := 0; locale < 16; locale++ {
			off := end + uint64(binary.LittleEndian.Uint32(r[(3+locale)*4:]))
			if off >= uint64(len(b)) {
				continue
			}
			if length := bytes.IndexByte(b[off:], 0); length > 0 {
				return string(b[off : off+uint64(length)]), true
			}
		}
	}
	return "", false
}
