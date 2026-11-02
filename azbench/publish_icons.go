package main

// Icons for the website's kit panel: spells' from Spell.dbc (SpellIconID) via SpellIcon.dbc, items' from their display
// (item_template.displayid) via ItemDisplayInfo.dbc. The images come from `clientdata icons` (data/icons/<name>.png);
// each one used is copied to site/public/icons. Without the extracted data, nothing gets an icon.

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/pthm/leeroy/azbench/run"
	"github.com/pthm/leeroy/azsdk/clientdata"
	"github.com/pthm/leeroy/azworld/world"
)

// dbcIcons: record id -> icon name (lower case, no path) from a DBC's string field.
func dbcIcons(file string, field int) map[int]string {
	out := map[int]string{}
	b, err := os.ReadFile(filepath.Join(world.DataDir(), "dbc", file))
	if err != nil {
		return out
	}
	ids, names := dbcInts(b, 0), clientdata.DBCStrings(b, field)
	for i, id := range ids {
		if names[i] == "" {
			continue
		}
		out[id] = strings.ToLower(filepath.Base(strings.ReplaceAll(names[i], `\`, "/")))
	}
	return out
}

func dbcInts(b []byte, field int) []int {
	if len(b) < 20 || string(b[:4]) != "WDBC" {
		return nil
	}
	n, size := int(binary.LittleEndian.Uint32(b[4:])), int(binary.LittleEndian.Uint32(b[12:]))
	out := make([]int, n)
	for i := range out {
		out[i] = int(binary.LittleEndian.Uint32(b[20+i*size+field*4:]))
	}
	return out
}

var (
	itemIcons  = sync.OnceValue(func() map[int]string { return dbcIcons("ItemDisplayInfo.dbc", 5) })
	spellIcons = sync.OnceValue(func() map[int]string { // spell id -> icon
		icons := dbcIcons("SpellIcon.dbc", 1)
		out := map[int]string{}
		b, _ := os.ReadFile(filepath.Join(world.DataDir(), "dbc", "Spell.dbc"))
		ids, icon := dbcInts(b, 0), dbcInts(b, 133) // field 133: SpellIconID
		for i, id := range ids {
			if n := icons[icon[i]]; n != "" && n != "temp" {
				out[id] = n
			}
		}
		return out
	})
)

// useIcon copies an icon to the site, returning its name, or "" when there's no image for it.
func useIcon(name, dir string) string {
	if name == "" {
		return ""
	}
	dst := filepath.Join(dir, name+".png")
	if _, err := os.Stat(dst); err == nil {
		return name
	}
	b, err := os.ReadFile(filepath.Join(world.DataDir(), "icons", name+".png"))
	if err != nil {
		return ""
	}
	os.MkdirAll(dir, 0o755)
	if os.WriteFile(dst, b, 0o644) != nil {
		return ""
	}
	return name
}

// kitIcons fills in the kit's icons: items' displays from the world database.
func kitIcons(k *run.Kit, dir string) {
	if len(k.Gear) > 0 {
		ids := make([]string, len(k.Gear))
		for i, g := range k.Gear {
			ids[i] = strconv.Itoa(g.Entry)
		}
		display := map[int]int{}
		rows, _ := world.Rows(world.DB(world.Admin, "acore_world"), "SELECT entry, displayid FROM item_template WHERE entry IN ("+strings.Join(ids, ",")+")")
		for _, r := range rows {
			e, _ := strconv.Atoi(r[0])
			d, _ := strconv.Atoi(r[1])
			display[e] = d
		}
		for i := range k.Gear {
			k.Gear[i].Icon = useIcon(itemIcons()[display[k.Gear[i].Entry]], dir)
		}
	}
	for _, l := range [][]run.KitSpell{k.Talents, k.Abilities} {
		for i := range l {
			l[i].Icon = useIcon(spellIcons()[l[i].ID], dir)
		}
	}
}

// castIcons: spell name -> icon, for the cast counts.
func castIcons(casts map[string]int, spells map[string]int, dir string) map[string]string {
	out := map[string]string{}
	for name := range casts {
		if n := useIcon(spellIcons()[spells[name]], dir); n != "" {
			out[name] = n
		}
	}
	return out
}
