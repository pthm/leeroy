package session

// Talents: from level 10 you get a talent point every level, spent in one of your class's three trees.
// talents.list() lists the trees with every talent's current rank and what it needs; talents.learn(id) spends a
// point. Talent.dbc / TalentTab.dbc from <data>/dbc.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	cmsgLearnTalent      = 0x251
	fieldCharacterPoints = 0x3FC // PLAYER_CHARACTER_POINTS1 (UNIT_END + 0x368): unspent talent points
)

type talentEntry struct {
	id, tab, row, col uint32
	ranks             []uint32 // spell per rank
	dependsOn, depRnk uint32
}

type talentTab struct {
	id, classMask, order uint32
	name                 string
}

var (
	talentOnce sync.Once
	talentList []talentEntry
	talentTabs map[uint32]talentTab
)

func loadTalents() {
	talentOnce.Do(func() {
		talentTabs = map[uint32]talentTab{}
		le := binary.LittleEndian
		if b := dbcFile("TalentTab.dbc"); b != nil {
			n, rs := le.Uint32(b[4:]), le.Uint32(b[12:])
			strs := b[20+n*rs:]
			for i := uint32(0); i < n; i++ {
				rec := b[20+i*rs:]
				f := func(k int) uint32 { return le.Uint32(rec[k*4:]) }
				name := ""
				if off := f(1); off < uint32(len(strs)) {
					name = string(strs[off : off+uint32(bytes.IndexByte(strs[off:], 0))])
				}
				talentTabs[f(0)] = talentTab{f(0), f(20), f(22), name}
			}
		}
		for id, rec := range dbcRows("Talent.dbc") {
			f := func(k int) uint32 { return le.Uint32(rec[k*4:]) }
			t := talentEntry{id: id, tab: f(1), row: f(2), col: f(3), dependsOn: f(13), depRnk: f(16)}
			for k := 4; k <= 8; k++ {
				if f(k) != 0 {
					t.ranks = append(t.ranks, f(k))
				}
			}
			talentList = append(talentList, t)
		}
		sort.Slice(talentList, func(i, j int) bool {
			a, b := talentList[i], talentList[j]
			if a.tab != b.tab {
				return a.tab < b.tab
			}
			if a.row != b.row {
				return a.row < b.row
			}
			return a.col < b.col
		})
	})
}

func dbcFile(name string) []byte {
	b, err := os.ReadFile(filepath.Join(spellsDir, "dbc", name))
	if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
		return nil
	}
	return b
}

func (b *Bot) setupTalents() {
	w := b.world
	e := b.lua
	rank := func(t talentEntry) int { // ranks you have: the highest rank spell you know
		for r := len(t.ranks); r > 0; r-- {
			if w.KnowsSpell(t.ranks[r-1]) {
				return r
			}
		}
		return 0
	}
	myTabs := func() []talentTab {
		loadTalents()
		cls := uint32(b.GetClass())
		tabs := []talentTab{}
		for _, t := range talentTabs {
			if cls > 0 && t.classMask&(1<<(cls-1)) != 0 {
				tabs = append(tabs, t)
			}
		}
		sort.Slice(tabs, func(i, j int) bool { return tabs[i].order < tabs[j].order })
		return tabs
	}
	e.Func("talents.list", "() → Talents", "your unspent points and your class's trees: each talent's rank, whether you can learn its next rank now, and what it requires", func() types.Talents {
		out := types.Talents{Trees: []types.TalentTree{}}
		if me := w.GetObject(w.CharGUID()); me != nil {
			out.Points = me.Value(fieldCharacterPoints)
		}
		name := func(t talentEntry) string {
			if sp := spellInfo(t.ranks[0]); sp != nil {
				return sp.Name
			}
			return fmt.Sprint(t.id)
		}
		for _, tab := range myTabs() {
			tree := types.TalentTree{Name: tab.name, Talents: []types.Talent{}}
			for _, t := range talentList {
				if t.tab == tab.id {
					tree.Spent += rank(t)
				}
			}
			for _, t := range talentList {
				if t.tab != tab.id || len(t.ranks) == 0 {
					continue
				}
				r := rank(t)
				tl := types.Talent{ID: t.id, Name: name(t), Rank: r, MaxRank: len(t.ranks), Row: t.row, Col: t.col,
					Spell: t.ranks[min(r, len(t.ranks)-1)]}
				ok := out.Points > 0 && r < len(t.ranks) && tree.Spent >= int(t.row)*5
				if t.dependsOn != 0 {
					for _, d := range talentList {
						if d.id == t.dependsOn {
							tl.Requires = fmt.Sprintf("%s rank %d", name(d), t.depRnk+1)
							ok = ok && rank(d) > int(t.depRnk)
						}
					}
				}
				tl.Available = ok
				tree.Talents = append(tree.Talents, tl)
			}
			out.Trees = append(out.Trees, tree)
		}
		return out
	})
	e.Func("talents.learn", "(id) → request<TalentLearned>", "spend a point on the next rank of a talent (ids from talents.list)", func(id uint32) map[string]any {
		args := map[string]any{"talent": id}
		loadTalents()
		for _, t := range talentList {
			if t.id == id {
				r := rank(t)
				if r >= len(t.ranks) {
					return b.refuse("talents.learn", args, "max_rank", "already at max rank")
				}
				body := binary.LittleEndian.AppendUint32(nil, id)
				w.SendPacketRaw(cmsgLearnTalent, binary.LittleEndian.AppendUint32(body, uint32(r)))
				return b.request("talents.learn", args, 3*time.Second, func() *script.Outcome {
					if rank(t) > r {
						return script.Ok(types.TalentLearned{Rank: r + 1})
					}
					return nil
				}, nil)
			}
		}
		return b.refuse("talents.learn", args, "no_such_talent", fmt.Sprintf("no talent %d", id))
	})
}
