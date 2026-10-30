package session

// The fight log: where everyone was each second and what happened, kept for the last fights so an agent can look
// back at one (wow fights, wow fight <n>, combat.fight) and see how movement played out: who was pulled, what
// joined from where, who drifted out of range, where it died. Each fight is also written to <workspace>/fights.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

const (
	fightKeep      = 50               // fights kept in memory (older ones stay on disk)
	fightHistory   = 15 * time.Minute // positions and events kept for the fight that's still running
	fightRadius    = 50               // yards around you that are sampled
	fightPreroll   = 3 * time.Second  // positions from before the fight: the approach and the pull
	fightApartYd   = 40               // heal and buff range: party members farther than this are apart
	fightAddGap    = 1.5              // seconds after the first enemy for another to count as an add
	fightPackYd    = 15               // an add this close to an enemy already in the fight came with it
	fightTimelineN = 150              // timeline lines in the text (all of them in combat.fight)
)

type spotRec struct {
	guid, target uint64
	name         string
	level        uint32
	kind         string // you, party, player, hostile, neutral, friendly
	x, y, z, hp  float64
	alive        bool
}

type frameRec struct {
	t     time.Time
	spots []spotRec
}

type entryRec struct {
	t        time.Time
	kind     string // damage, heal, cast, kill, death
	src, dst types.UnitRef
	spell    string
	amount   uint32
	outcome  string
}

type fightLog struct {
	mu      sync.Mutex
	frames  []frameRec
	entries []entryRec
	fights  []types.Fight // the last fightKeep, oldest first
	next    int           // the next fight's id
	dir     string        // <workspace>/fights; "" without a workspace
	sampled time.Time
}

func newFightLog(workspace string) *fightLog {
	fl := &fightLog{next: 1}
	if workspace != "" {
		fl.dir = filepath.Join(workspace, "fights")
		files, _ := filepath.Glob(filepath.Join(fl.dir, "*.json"))
		for _, f := range files { // carry on numbering after the fights already on disk
			if n, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(f), ".json")); err == nil && n >= fl.next {
				fl.next = n + 1
			}
		}
	}
	return fl
}

// sample records where everyone around you is, once a second (from the tick).
func (b *Bot) sampleFightLog() {
	fl := b.fightLog
	if fl == nil || time.Since(fl.sampled) < time.Second {
		return
	}
	now := time.Now()
	party := map[uint64]bool{}
	if g, ok := b.lua.Native("group.info").(types.Group); ok {
		for _, m := range g.Members {
			party[uint64(m.GUID)] = true
		}
	}
	me := b.me3()
	hc, hm := b.GetHealth()
	spots := []spotRec{{guid: b.world.CharGUID(), name: b.config.CharacterName, level: uint32(b.GetLevel()), kind: "you",
		x: me.x, y: me.y, z: me.z, hp: frac(hc, hm), alive: b.IsAlive() && !b.IsGhost(), target: b.world.TargetGUID()}}
	myFaction := uint32(0)
	if o := b.world.GetObject(b.world.CharGUID()); o != nil {
		myFaction = o.Value(fieldUnitFaction)
	}
	add := func(u UnitInfo, kind string) {
		spots = append(spots, spotRec{guid: u.GUID, target: u.Target, name: u.Name, level: u.Level, kind: kind,
			x: float64(u.PosX), y: float64(u.PosY), z: float64(u.PosZ), hp: frac(u.Health, u.MaxHealth), alive: u.IsAlive})
	}
	for _, u := range b.GetNearbyUnits(fightRadius) {
		if u.Flags&0x2000000 == 0 {
			add(u, factionReaction(u.Faction, myFaction))
		}
	}
	for _, u := range b.GetNearbyPlayers(fightRadius) {
		add(u, map[bool]string{true: "party", false: "player"}[party[u.GUID]])
	}
	fl.mu.Lock()
	fl.sampled = now
	fl.frames = append(fl.frames, frameRec{now, spots})
	fl.trim(now)
	fl.mu.Unlock()
}

func frac(c, m uint32) float64 {
	if m == 0 {
		return 0
	}
	return math.Round(float64(c)/float64(m)*100) / 100
}

// trim drops what's older than fightHistory (caller holds mu).
func (fl *fightLog) trim(now time.Time) {
	cut := now.Add(-fightHistory)
	i := sort.Search(len(fl.frames), func(i int) bool { return fl.frames[i].t.After(cut) })
	fl.frames = fl.frames[i:]
	j := sort.Search(len(fl.entries), func(i int) bool { return fl.entries[i].t.After(cut) })
	fl.entries = fl.entries[j:]
}

// record keeps the events a fight's timeline is made of (bus listener: must not block or emit).
func (fl *fightLog) record(ev script.Event) {
	var e entryRec
	switch d := ev.Data.(type) {
	case types.Damage:
		e = entryRec{kind: ev.Type, src: d.Src, dst: d.Dst, spell: d.SpellName, amount: d.Amount, outcome: d.Outcome}
		if ev.Type != "heal" {
			e.kind = "damage"
		}
		if d.Periodic {
			e.outcome = strings.TrimSpace(e.outcome + " tick")
		}
	case types.Cast:
		if ev.Type != "cast_done" {
			return
		}
		e = entryRec{kind: "cast", src: types.UnitRef{Me: true}, spell: d.Name}
	case types.Kill:
		e = entryRec{kind: "kill", dst: d.Victim, src: types.UnitRef{Me: true}}
		if d.Killer != nil {
			e.src = *d.Killer
		}
	default:
		if ev.Type != "death" {
			return
		}
		e = entryRec{kind: "death", dst: types.UnitRef{Me: true}}
	}
	e.t = time.UnixMilli(ev.T)
	fl.mu.Lock()
	fl.entries = append(fl.entries, e)
	fl.mu.Unlock()
}

// finish builds the fight that ran from start to end into the log, fills the summary's id and positioning, and
// writes the fight to the workspace.
func (fl *fightLog) finish(sum *types.FightSummary, start, end time.Time, logf func(string, ...any)) {
	fl.mu.Lock()
	f := buildFight(fl.next, sum, start, end, fl.frames, fl.entries)
	fl.next++
	fl.fights = append(fl.fights, f)
	if len(fl.fights) > fightKeep {
		fl.fights = fl.fights[len(fl.fights)-fightKeep:]
	}
	dir := fl.dir
	fl.mu.Unlock()
	sum.ID, sum.Positioning = f.ID, f.Summary.Positioning
	if dir == "" {
		return
	}
	go func() {
		os.MkdirAll(dir, 0o755)
		b, _ := json.Marshal(f)
		name := fmt.Sprintf("%03d.json", f.ID)
		if os.WriteFile(filepath.Join(dir, name), b, 0o644) == nil {
			logf("fight %d: %s after %.0f s (wow fight %d; fights/%s)", f.ID, sum.Outcome, sum.Seconds, f.ID, name)
		}
	}()
}

// buildFight turns the positions and events between start and end into a Fight: who took part (labelled for the
// map), where each was every second, the timeline, and what the movement did.
func buildFight(id int, sum *types.FightSummary, start, end time.Time, frames []frameRec, entries []entryRec) types.Fight {
	from, to := start.Add(-fightPreroll), end.Add(time.Second)
	secs := func(t time.Time) float64 { return math.Round(t.Sub(start).Seconds()*10) / 10 }
	var fr []frameRec
	for _, f := range frames {
		if !f.t.Before(from) && !f.t.After(to) {
			fr = append(fr, f)
		}
	}
	var en []entryRec
	for _, e := range entries {
		if !e.t.Before(start.Add(-fightPreroll)) && !e.t.After(to) {
			en = append(en, e)
		}
	}
	// who was in it: you, the enemies (in the summary, hitting or hit, or targeting you or your party), your party
	var me uint64
	party := map[uint64]bool{}
	for _, f := range fr {
		for _, s := range f.spots {
			switch s.kind {
			case "you":
				me = s.guid
			case "party":
				party[s.guid] = true
			}
		}
	}
	first := map[uint64]time.Time{} // when each enemy got involved
	involve := func(g uint64, t time.Time) {
		if g == 0 || g == me || party[g] {
			return
		}
		if old, ok := first[g]; !ok || t.Before(old) {
			first[g] = t
		}
	}
	players := map[uint64]bool{}
	for _, e := range en {
		for _, u := range []types.UnitRef{e.src, e.dst} {
			if u.Player && !u.Me {
				players[uint64(u.GUID)] = true
			}
		}
		if e.kind == "damage" || e.kind == "kill" {
			for _, u := range []types.UnitRef{e.src, e.dst} {
				if !u.Me && !u.Player {
					involve(uint64(u.GUID), e.t)
				}
			}
		}
	}
	for _, f := range fr {
		if f.t.Before(start) {
			continue
		}
		for _, s := range f.spots {
			if s.alive && s.kind != "you" && s.kind != "party" && s.kind != "player" && (s.target == me || party[s.target]) {
				involve(s.guid, f.t)
			}
		}
	}
	for _, u := range sum.Enemies {
		if _, ok := first[uint64(u.GUID)]; !ok {
			involve(uint64(u.GUID), start)
		}
	}
	enemies := make([]uint64, 0, len(first))
	for g := range first {
		enemies = append(enemies, g)
	}
	sort.Slice(enemies, func(i, j int) bool { return first[enemies[i]].Before(first[enemies[j]]) })

	// labels: @ you, A-N enemies, P-Z party then other players
	label := map[uint64]string{me: "@"}
	actors := []types.FightActor{}
	info := map[uint64]spotRec{}
	for _, f := range fr {
		for _, s := range f.spots {
			if _, ok := info[s.guid]; !ok || info[s.guid].name == "" {
				info[s.guid] = s
			}
		}
	}
	for _, u := range sum.Enemies {
		if s, ok := info[uint64(u.GUID)]; ok && s.name == "" {
			s.name = u.Name
			info[uint64(u.GUID)] = s
		}
	}
	actor := func(g uint64, l, kind string) {
		label[g] = l
		s := info[g]
		actors = append(actors, types.FightActor{Label: l, GUID: types.GUID(g), Name: s.name, Level: s.level, Kind: kind})
	}
	if me != 0 {
		s := info[me]
		actors = append(actors, types.FightActor{Label: "@", GUID: types.GUID(me), Name: s.name, Level: s.level, Kind: "you"})
	}
	for i, g := range enemies {
		if i >= len(enemyLabels) {
			break
		}
		actor(g, string(enemyLabels[i]), "enemy")
	}
	others := []uint64{}
	for g := range party {
		others = append(others, g)
	}
	for g := range players {
		if !party[g] {
			others = append(others, g)
		}
	}
	sort.Slice(others, func(i, j int) bool {
		if party[others[i]] != party[others[j]] {
			return party[others[i]]
		}
		return info[others[i]].name < info[others[j]].name
	})
	for i, g := range others {
		if i >= len(playerLabels) {
			break
		}
		actor(g, string(playerLabels[i]), map[bool]string{true: "party", false: "player"}[party[g]])
	}

	frm := []types.FightFrame{}
	for _, f := range fr {
		ff := types.FightFrame{S: secs(f.t)}
		for _, s := range f.spots {
			if l, ok := label[s.guid]; ok {
				ff.At = append(ff.At, types.FightSpot{Label: l, X: round2(s.x), Y: round2(s.y), Z: round2(s.z), HP: s.hp, Alive: s.alive, Target: label[s.target]})
			}
		}
		frm = append(frm, ff)
	}
	name := func(u types.UnitRef) string {
		if u.Me {
			return "@"
		}
		if l, ok := label[uint64(u.GUID)]; ok {
			return l
		}
		return u.Name
	}
	tl := []types.FightEntry{}
	for _, e := range en {
		tl = append(tl, types.FightEntry{S: secs(e.t), Kind: e.kind, Src: name(e.src), Dst: name(e.dst), Spell: e.spell, Amount: e.amount, Outcome: e.outcome})
	}
	for i, g := range enemies {
		if i < len(enemyLabels) {
			tl = append(tl, types.FightEntry{S: secs(first[g]), Kind: "join", Src: label[g]})
		}
	}
	sort.SliceStable(tl, func(i, j int) bool { return tl[i].S < tl[j].S })

	f := types.Fight{ID: id, Start: start.UnixMilli(), Summary: *sum, Actors: actors, Frames: frm, Timeline: tl}
	f.Summary.ID = id
	f.Summary.Positioning = positioning(f, first, enemies, label, start)
	return f
}

var enemyLabels = "ABCDEFGHIJKLMN"
var playerLabels = "PQRSTUVWXYZ"

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// spotAt: where label was in the frame nearest s (nil if it wasn't seen then)
func spotAt(f types.Fight, label string, s float64) *types.FightSpot {
	var best *types.FightSpot
	bd := math.Inf(1)
	for i := range f.Frames {
		for j := range f.Frames[i].At {
			if sp := &f.Frames[i].At[j]; sp.Label == label {
				if d := math.Abs(f.Frames[i].S - s); d < bd {
					best, bd = sp, d
				}
			}
		}
	}
	return best
}

func dist2(a, b *types.FightSpot) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

// positioning works out what the movement did: how far you walked, who joined from where, who was out of range.
func positioning(f types.Fight, first map[uint64]time.Time, enemies []uint64, label map[uint64]string, start time.Time) *types.FightPositioning {
	p := &types.FightPositioning{Joins: []types.FightJoin{}}
	var prev *types.FightSpot
	for i := range f.Frames {
		if f.Frames[i].S < 0 {
			continue
		}
		for j := range f.Frames[i].At {
			if sp := &f.Frames[i].At[j]; sp.Label == "@" && sp.Alive { // a corpse doesn't walk
				if prev != nil {
					p.MovedYd += dist2(prev, sp)
				}
				prev = sp
			}
		}
	}
	p.MovedYd = math.Round(p.MovedYd)
	byLabel := map[string]types.FightActor{}
	for _, a := range f.Actors {
		byLabel[a.Label] = a
	}
	var joined []string
	for i, g := range enemies {
		l := label[g]
		if l == "" {
			continue
		}
		s := math.Round(first[g].Sub(start).Seconds()*10) / 10
		j := types.FightJoin{Label: l, Name: byLabel[l].Name, Level: byLabel[l].Level, S: s}
		me, it := spotAt(f, "@", s), spotAt(f, l, s)
		if me != nil && it != nil {
			j.Distance = math.Round(dist2(me, it))
			j.From = compassOf(it.X-me.X, it.Y-me.Y)
		}
		if i > 0 && it != nil {
			for _, o := range joined {
				if os := spotAt(f, o, s); os != nil && dist2(os, it) <= fightPackYd {
					j.Near = o
					break
				}
			}
		}
		p.Joins = append(p.Joins, j)
		joined = append(joined, l)
	}
	for _, a := range f.Actors {
		if a.Kind != "party" {
			continue
		}
		ap := types.FightApart{Label: a.Label, Name: a.Name}
		for _, fr := range f.Frames {
			if fr.S < 0 {
				continue
			}
			me, them := spotAt(f, "@", fr.S), (*types.FightSpot)(nil)
			for j := range fr.At {
				if fr.At[j].Label == a.Label {
					them = &fr.At[j]
				}
			}
			if me != nil && them != nil {
				d := dist2(me, them)
				ap.MaxYd = math.Max(ap.MaxYd, math.Round(d))
				if d > fightApartYd {
					ap.Seconds++
				}
			}
		}
		if ap.Seconds > 0 {
			p.Apart = append(p.Apart, ap)
		}
	}
	if f.Summary.Outcome == "died" && len(f.Frames) > 0 {
		last := f.Frames[len(f.Frames)-1]
		var me *types.FightSpot
		for i := len(f.Frames) - 1; i >= 0 && me == nil; i-- { // where you last stood alive
			for j := range f.Frames[i].At {
				if sp := &f.Frames[i].At[j]; sp.Label == "@" && sp.Alive {
					me, last = sp, f.Frames[i]
				}
			}
		}
		if me != nil {
			d := &types.FightDeath{}
			for j := range last.At {
				sp := &last.At[j]
				switch k := byLabel[sp.Label].Kind; {
				case k == "enemy" && sp.Alive && dist2(me, sp) <= 10:
					d.EnemiesWithin10++
				case k == "party":
					yd := math.Round(dist2(me, sp))
					if d.NearestPartyYd == nil || yd < *d.NearestPartyYd {
						d.NearestPartyYd = &yd
					}
				}
			}
			p.Died = d
		}
	}
	return p
}

// header is a fight's line in the list.
func header(f types.Fight) types.FightHeader {
	h := types.FightHeader{ID: f.ID, Start: f.Start, Seconds: f.Summary.Seconds, Outcome: f.Summary.Outcome,
		DamageDealt: f.Summary.DamageDealt, DamageTaken: f.Summary.DamageTaken, Enemies: []string{}}
	count := map[string]int{}
	var order []string
	for _, a := range f.Actors {
		if a.Kind == "enemy" {
			if count[a.Name] == 0 {
				order = append(order, a.Name)
			}
			count[a.Name]++
			h.Adds++
		}
	}
	if h.Adds > 0 {
		h.Adds--
	}
	for _, n := range order {
		if count[n] > 1 {
			n = fmt.Sprintf("%s ×%d", n, count[n])
		}
		h.Enemies = append(h.Enemies, n)
	}
	return h
}

// fight returns a fight by id (0 or less: the last), from memory or the workspace.
func (fl *fightLog) fight(id int) (types.Fight, bool) {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if id <= 0 {
		if len(fl.fights) > 0 {
			return fl.fights[len(fl.fights)-1], true
		}
		id = fl.next - 1
	}
	for _, f := range fl.fights {
		if f.ID == id {
			return f, true
		}
	}
	if fl.dir != "" {
		if b, err := os.ReadFile(filepath.Join(fl.dir, fmt.Sprintf("%03d.json", id))); err == nil {
			var f types.Fight
			if json.Unmarshal(b, &f) == nil {
				return f, true
			}
		}
	}
	return types.Fight{}, false
}

// list: the fights in memory and on disk, newest first, at most n.
func (fl *fightLog) list(n int) []types.FightHeader {
	fl.mu.Lock()
	seen := map[int]bool{}
	out := []types.FightHeader{}
	for _, f := range fl.fights {
		out, seen[f.ID] = append(out, header(f)), true
	}
	dir := fl.dir
	fl.mu.Unlock()
	if dir != "" {
		files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		for _, file := range files {
			if id, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(file), ".json")); err == nil && !seen[id] {
				if b, err := os.ReadFile(file); err == nil {
					var f types.Fight
					if json.Unmarshal(b, &f) == nil {
						out = append(out, header(f))
					}
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b types.FightHeader) int { return b.ID - a.ID })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// setupFightLog starts the fight log and its API.
func (b *Bot) setupFightLog() {
	e := b.lua
	b.fightLog = newFightLog(b.config.LuaWorkspace)
	b.lua.Bus.Listen(b.fightLog.record)
	e.Func("combat.fights", "([n = 20]) → FightHeader[]", "your last fights, newest first: id, when, how long, outcome, enemies, damage and adds (the fight log; wow fights)",
		func(n int) []types.FightHeader { return b.fightLog.list(cmpOr(n, 20)) })
	e.Func("combat.fight", "([id = last]) → Fight?", "one fight from the log: its summary and positioning, every actor's position each second, and the timeline of hits, heals, casts, kills and deaths",
		func(id int) any {
			if f, ok := b.fightLog.fight(id); ok {
				return f
			}
			return nil
		})
	e.Func("combat.fights_text", "([n = 20]) → string", "the fight log's list as text (what wow fights prints)",
		func(n int) string { return renderFightList(b.fightLog.list(cmpOr(n, 20))) })
	e.Func("combat.fight_text", "([id = last [, {[map = false, at = nil]}]]) → string", "one fight as text (what wow fight prints): summary and positioning, a map of everyone's movement, the key moments, the timeline. map = true: the map and moments only; at = s: the map at that second",
		func(id int, o map[string]any) string {
			f, ok := b.fightLog.fight(id)
			if !ok {
				if id <= 0 {
					return "no fights in the log yet\n"
				}
				return fmt.Sprintf("no fight %d in the log (wow fights lists them)\n", id)
			}
			var at *float64
			if v, ok := o["at"].(float64); ok {
				at = &v
			}
			mapOnly, _ := o["map"].(bool)
			return renderFight(f, mapOnly, at)
		})
}

func cmpOr(n, def int) int {
	if n <= 0 {
		return def
	}
	return n
}
