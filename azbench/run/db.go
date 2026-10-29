package run

import (
	"database/sql"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/pthm/leeroy/azbench/config"
	"github.com/pthm/leeroy/azworld/world"
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

// waitOffline waits for the server to log a character out (it finishes a logout or a dropped connection on its own).
func waitOffline(name string, limit time.Duration) bool {
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(time.Second) {
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
	set := []string{"health = 100000"}
	var args []any
	if s.Level > 0 {
		set = append(set, "level = ?", "xp = 0")
		args = append(args, s.Level)
	}
	if s.Money > 0 {
		set = append(set, "money = ?")
		args = append(args, s.Money)
	}
	if p := s.Start; p != nil {
		set = append(set, "position_x = ?", "position_y = ?", "position_z = ?", "map = ?", "zone = ?")
		args = append(args, p.X, p.Y, p.Z, p.Map, p.Zone)
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

// allowedPairs: the race/class pairs the game allows (playercreateinfo).
func allowedPairs() (func(r, c int) bool, error) {
	rows, err := world.Rows(world.DB(world.Admin, "acore_world"), "SELECT DISTINCT race, class FROM playercreateinfo")
	if err != nil {
		return nil, err
	}
	ok := map[[2]string]bool{}
	for _, r := range rows {
		ok[[2]string{r[0], r[1]}] = true
	}
	return func(r, c int) bool { return ok[[2]string{fmt.Sprint(r), fmt.Sprint(c)}] }, nil
}

// Reset soft-deletes characters (the way the game deletes them: the name and account are freed, the row kept) and
// restarts the worldserver, which caches names, so a fresh run can reuse them. They must be offline.
func Reset(names []string) error {
	db := chars()
	for _, n := range names {
		if charOnline(n) {
			return fmt.Errorf("%s is online", n)
		}
		if _, err := db.Exec(`UPDATE characters SET deleteInfos_Account = account, deleteInfos_Name = name,
			deleteDate = UNIX_TIMESTAMP(), name = '', account = 0 WHERE name = ?`, n); err != nil {
			return err
		}
	}
	return exec.Command("docker", "restart", "ac-worldserver").Run()
}
