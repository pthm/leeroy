package session

// How well the character is built for its level: gear (Gear: a 0-100 grade from item level, quality and durability)
// and the class spells it could know (Spellbook). Both are in me.status(); see docs/playing.md for why they matter.

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/pthm/wowbench/azeroth-sdk/client"
	"github.com/pthm/wowbench/azeroth-sdk/types"
)

var slotNames = []string{"head", "neck", "shoulder", "shirt", "chest", "waist", "legs", "feet", "wrists", "hands",
	"finger", "finger", "trinket", "trinket", "back", "main_hand", "off_hand", "ranged", "tabard"}

// how much each equipment slot counts toward the gear score: the weapon most, then the big armor pieces
var slotWeight = map[int]float64{0: 1.5, 1: 0.5, 2: 1.5, 4: 1.5, 5: 1, 6: 1.5, 7: 1, 8: 0.75, 9: 1, 10: 0.5, 11: 0.5,
	12: 0.5, 13: 0.5, 14: 0.75, 15: 4, 16: 1, 17: 0.75}

var qualityFactor = map[string]float64{"poor": 0.6, "common": 0.85, "uncommon": 1, "rare": 1.1, "epic": 1.2, "legendary": 1.2}

// gear grades what you wear against your level.
func (b *Bot) gear() types.Gear {
	w := b.world
	lvl := float64(max(b.GetLevel(), 1))
	worn := map[int]types.Item{}
	for _, it := range bagItems(w) {
		if it.Equipped {
			worn[int(it.BagSlot)] = it
		}
	}
	g := types.Gear{Weakest: []types.GearSlot{}, Empty: []string{}, Broken: []string{}}
	// a two-hander fills the off hand too; casters without an off hand aren't penalised for it
	twoHand := worn[15].Slot == "two_hand" || worn[15].Slot == "ranged"
	var sum, total, ilvl float64
	slots := []types.GearSlot{}
	for slot, weight := range slotWeight {
		it, ok := worn[slot]
		if slot == 16 && (twoHand || !ok) {
			continue // ponytail: an empty off hand isn't scored (many specs never use one)
		}
		gs := types.GearSlot{Slot: slotNames[slot]}
		score := 0.0
		switch {
		case !ok:
			g.Empty = append(g.Empty, slotNames[slot])
		case it.Broken:
			g.Broken = append(g.Broken, slotNames[slot])
			gs.Item, gs.Level = it.Name, it.ItemLevel
		default:
			gs.Item, gs.Level = it.Name, it.ItemLevel
			q := qualityFactor[it.Quality]
			if q == 0 {
				q = 0.85
			}
			score = math.Min(1, float64(it.ItemLevel)/lvl*q)
		}
		if ok {
			ilvl += float64(it.ItemLevel)
		}
		gs.Score = int(math.Round(score * 100))
		sum, total = sum+score*weight, total+weight
		slots = append(slots, gs)
	}
	g.AvgItemLevel = math.Round(ilvl/17*10) / 10
	if total > 0 {
		g.Score = int(math.Round(sum / total * 100))
	}
	switch {
	case g.Score >= 85:
		g.Grade = "good"
	case g.Score >= 65:
		g.Grade = "ok"
	case g.Score >= 40:
		g.Grade = "weak"
	default:
		g.Grade = "poor"
	}
	if it, ok := worn[15]; ok {
		g.Weapon = &it
	}
	// weakest: lowest score first, heavier slots first among equals
	sort.Slice(slots, func(i, j int) bool {
		if slots[i].Score != slots[j].Score {
			return slots[i].Score < slots[j].Score
		}
		return slots[i].Slot < slots[j].Slot
	})
	for _, s := range slots {
		if len(g.Weakest) < 4 && s.Item != "" { // empty slots are listed in Empty
			g.Weakest = append(g.Weakest, s)
		}
	}
	sort.Strings(g.Empty)
	return g
}

// ---------- spellbook ----------

type classSpell struct {
	id, level, classMask, raceMask uint32
	name, rank                     string
}

var classSpells = struct {
	once sync.Once
	list []classSpell
}{}

// loadClassSpells: trainer-taught spells of the class skill lines (SkillLine category 7), from SkillLineAbility.dbc.
func loadClassSpells() {
	classSpells.once.Do(func() {
		read := func(name string) (n, rs uint32, recs []byte) {
			b, err := os.ReadFile(filepath.Join(spellsDir, "dbc", name))
			if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
				return 0, 0, nil
			}
			n, rs = binary.LittleEndian.Uint32(b[4:]), binary.LittleEndian.Uint32(b[12:])
			return n, rs, b[20 : 20+n*rs]
		}
		le := binary.LittleEndian
		classLines := map[uint32]bool{}
		n, rs, recs := read("SkillLine.dbc")
		for i := uint32(0); i < n; i++ {
			r := recs[i*rs:]
			if int32(le.Uint32(r[4:])) == 7 { // category: class skills
				classLines[le.Uint32(r)] = true
			}
		}
		loadTalents()
		talentSpell := map[uint32]bool{}
		for _, t := range talentList {
			for _, s := range t.ranks {
				talentSpell[s] = true
			}
		}
		n, rs, recs = read("SkillLineAbility.dbc")
		for i := uint32(0); i < n; i++ {
			r := recs[i*rs:]
			line, spell, race, class, acquire := le.Uint32(r[4:]), le.Uint32(r[8:]), le.Uint32(r[12:]), le.Uint32(r[16:]), le.Uint32(r[36:])
			if !classLines[line] || acquire != 0 || class == 0 || talentSpell[spell] {
				continue
			}
			sp := spellInfo(spell)
			if sp == nil || sp.Level == 0 || sp.Name == "" || sp.attrs&0xC0 != 0 { // passive or hidden: not trained
				continue
			}
			classSpells.list = append(classSpells.list, classSpell{id: spell, level: sp.Level, classMask: class, raceMask: race,
				name: sp.Name, rank: sp.Rank})
		}
		// a spell with ranks also has unranked internal effect spells of the same name (Execute's damage): drop those
		ranked := map[string]bool{}
		for _, c := range classSpells.list {
			if c.rank != "" {
				ranked[c.name] = true
			}
		}
		keep := classSpells.list[:0]
		for _, c := range classSpells.list {
			if c.rank != "" || !ranked[c.name] {
				keep = append(keep, c)
			}
		}
		classSpells.list = keep
	})
}

// spellbook: for each class spell (by name) the highest rank you could have at your level, and whether you do.
func (b *Bot) spellbook() types.Spellbook {
	loadClassSpells()
	w := b.world
	lvl, cls, race := b.GetLevel(), uint32(b.GetClass()), uint32(b.GetRace())
	best := map[string]classSpell{}    // name -> highest rank available at your level
	ranks := map[string][]classSpell{} // name -> all ranks
	for _, s := range classSpells.list {
		if cls == 0 || s.classMask&(1<<(cls-1)) == 0 || (s.raceMask != 0 && race > 0 && s.raceMask&(1<<(race-1)) == 0) {
			continue
		}
		ranks[s.name] = append(ranks[s.name], s)
		if s.level <= lvl && s.level > best[s.name].level {
			best[s.name] = s
		}
	}
	sb := types.Spellbook{Missing: []types.MissingSpell{}}
	for name, want := range best {
		sb.Total++
		known := false
		for _, r := range ranks[name] { // that rank or a higher one
			if r.level >= want.level && w.KnowsSpell(r.id) {
				known = true
				break
			}
		}
		if known {
			sb.Known++
		} else {
			sb.Missing = append(sb.Missing, types.MissingSpell{ID: want.id, Name: name, Rank: want.rank, Level: want.level})
		}
	}
	sort.Slice(sb.Missing, func(i, j int) bool { return sb.Missing[i].Level < sb.Missing[j].Level })
	return sb
}

// unspentTalents: talent points to spend
func unspentTalents(w *client.WorldClient) uint32 {
	if me := w.GetObject(w.CharGUID()); me != nil {
		return me.Value(fieldCharacterPoints)
	}
	return 0
}
