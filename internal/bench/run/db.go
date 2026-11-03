package run

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pthm/leeroy/internal/bench/config"
	"github.com/pthm/leeroy/world"
)

func chars() *sql.DB { return world.DB(world.Admin, "acore_characters") }

// charExists and charOnline read the characters table.
func charExists(name string) bool {
	r, _ := world.Rows(chars(), "SELECT 1 FROM characters WHERE name = ?", name)
	return len(r) > 0
}

func charOnline(name string) bool {
	r, _ := world.Rows(chars(), "SELECT online FROM characters WHERE name = ?", name)
	return len(r) > 0 && r[0][0] != "0"
}

// waitOffline waits up to 2 minutes for the server to log a character out (it finishes a logout or a dropped
// connection on its own).
func waitOffline(name string) bool {
	for end := time.Now().Add(2 * time.Minute); time.Now().Before(end); time.Sleep(time.Second) {
		if !charOnline(name) {
			return true
		}
	}
	return false
}

// grantReader gives the read-only account azworld uses SELECT on the world database.
func grantReader() error {
	_, err := world.DB(world.Admin, "").Exec(`CREATE USER IF NOT EXISTS 'worldreader'@'%' IDENTIFIED BY 'worldreader';
		GRANT SELECT ON acore_world.* TO 'worldreader'@'%'; GRANT SELECT ON wowbench.* TO 'worldreader'@'%'`)
	return err
}

// applyStart sets the scenario's starting conditions on an offline character: level, money, position, and the setup
// steps for characters from earlier runs (revive, repair, reset_talents). health = 100000 so it logs in alive.
func applyStart(s *config.Scenario, char string) error {
	set := []string{"health = 100000", "power1 = 100000"} // full health and mana: the server caps both at login
	var args []any
	if s.Level > 0 {
		set = append(set, "level = ?", "xp = 0")
		args = append(args, s.Level)
	}
	if s.Money > 0 {
		set = append(set, "money = ?")
		args = append(args, s.Money)
	}
	if p := startOf(s, char); p != nil {
		x, y, z, o := spot(s, char)
		set = append(set, "position_x = ?", "position_y = ?", "position_z = ?", "orientation = ?", "map = ?", "zone = ?")
		args = append(args, x, y, z, o, p.Map, p.Zone)
	}
	g := "(SELECT guid FROM (SELECT guid FROM characters WHERE name = ?) c)"
	var q []string
	var qa []any
	for _, st := range s.Setup {
		switch st {
		case "revive": // no ghost flag, resurrection sickness or corpse
			q = append(q, "DELETE FROM corpse WHERE guid = "+g, "DELETE FROM character_aura WHERE spell = 8326 AND guid = "+g)
			qa = append(qa, char, char)
			set = append(set, "playerFlags = playerFlags & ~16")
		case "repair":
			q = append(q, `UPDATE item_instance ii JOIN acore_world.item_template it ON it.entry = ii.itemEntry
				SET ii.durability = it.MaxDurability WHERE it.MaxDurability > 0 AND ii.owner_guid = `+g)
			qa = append(qa, char)
		case "reset_talents":
			q = append(q, "DELETE FROM character_talent WHERE guid = "+g)
			qa = append(qa, char)
		}
	}
	db := chars()
	for i, stmt := range q {
		if _, err := db.Exec(stmt, qa[i]); err != nil {
			return fmt.Errorf("%s: %v", st(stmt), err)
		}
	}
	_, err := db.Exec("UPDATE characters SET "+strings.Join(set, ", ")+" WHERE name = ?", append(args, char)...)
	return err
}

func st(s string) string { return strings.Fields(s)[0] + " " + strings.Fields(s)[1] }

// Reset soft-deletes characters (the way the game deletes them: the name and account are freed, the row kept) and
// restarts the worldserver, which caches names, so a fresh run can reuse them. They must be offline.
func Reset(names []string) error {
	for _, n := range names {
		if charOnline(n) {
			return fmt.Errorf("%s is online", n)
		}
	}
	for _, n := range names {
		if _, err := chars().Exec(`UPDATE characters SET deleteInfos_Account = account, deleteInfos_Name = name,
			deleteDate = UNIX_TIMESTAMP(), name = '', account = 0 WHERE name = ?`, n); err != nil {
			return err
		}
	}
	return restartWorld()
}

// Fresh readies a scenario to run again from scratch (azbench run --reset): its characters deleted and the
// worldserver restarted (Reset), and its agents' workspaces moved aside to var/workspaces/.old/<agent>-<time>, as
// require_fresh wants (kept, not deleted: they hold the last run's notes and scripts).
func Fresh(repo string, s *config.Scenario) error {
	names := make([]string, 0, len(s.Agents))
	for _, a := range s.Agents {
		names = append(names, a.Char)
	}
	for _, n := range names {
		if charOnline(n) {
			return fmt.Errorf("%s is online: stop its run first", n)
		}
	}
	old := filepath.Join(repo, "var", "workspaces", ".old")
	stamp := time.Now().Format("20060102-150405")
	for _, a := range s.Agents {
		ws := filepath.Join(repo, "var", "workspaces", a.Name)
		if _, err := os.Stat(ws); err != nil {
			continue
		}
		if err := os.MkdirAll(old, 0o755); err != nil {
			return err
		}
		if err := os.Rename(ws, filepath.Join(old, a.Name+"-"+stamp)); err != nil {
			return err
		}
	}
	return Reset(names)
}

// restartWorld restarts the worldserver and waits for it to say it's ready (a few minutes at most): a run started
// before that can't log in.
func restartWorld() error {
	since := time.Now().Add(-time.Second).Format(time.RFC3339)
	if out, err := exec.Command("docker", "restart", "ac-worldserver").CombinedOutput(); err != nil {
		return fmt.Errorf("docker restart ac-worldserver: %v: %s", err, out)
	}
	for end := time.Now().Add(5 * time.Minute); time.Now().Before(end); time.Sleep(2 * time.Second) {
		out, _ := exec.Command("docker", "logs", "--since", since, "ac-worldserver").CombinedOutput()
		if strings.Contains(string(out), ") ready...") {
			return nil
		}
	}
	return fmt.Errorf("the worldserver didn't say it was ready within 5 minutes (docker logs ac-worldserver)")
}

// startOf is where a character's run starts: its own start (a versus pair's spot), or the run's.
func startOf(s *config.Scenario, char string) *config.Place {
	for _, a := range s.Agents {
		if a.Char == char && a.Start != nil {
			return a.Start
		}
	}
	return s.Start
}

// spot is where a character starts: its start, or with spread its place on the circle around it (by its order among
// the agents starting there), on the ground and facing the centre.
func spot(s *config.Scenario, char string) (x, y, z, o float64) {
	p := startOf(s, char)
	var here []string // the agents starting at the same place
	for _, a := range s.Agents {
		if q := startOf(s, a.Char); q != nil && *q == *p {
			here = append(here, a.Char)
		}
	}
	i := slices.Index(here, char)
	if s.Spread <= 0 || i < 0 || len(here) < 2 {
		return p.X, p.Y, p.Z, 0
	}
	a := 2 * math.Pi * float64(i) / float64(len(here))
	x, y = p.X+s.Spread/2*math.Cos(a), p.Y+s.Spread/2*math.Sin(a)
	z = p.Z
	if h, _, _ := world.Sample(uint32(p.Map), x, y); !math.IsNaN(h) {
		z = h + 0.5
	}
	return x, y, z, math.Mod(a+math.Pi, 2*math.Pi)
}
