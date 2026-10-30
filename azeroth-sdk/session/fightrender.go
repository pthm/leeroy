package session

// The fight log as text: the list (wow fights) and one fight (wow fight <n>): its summary and positioning, a map
// of everyone's movement (each one's trail in its lowercase letter, where it ended in its capital, @ you), the key
// moments as small maps, and the timeline.

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func renderFightList(hs []types.FightHeader) string {
	if len(hs) == 0 {
		return "no fights in the log yet\n"
	}
	var sb strings.Builder
	sb.WriteString("fight log, newest first (wow fight <id>: its map of everyone's movement, the key moments, the timeline)\n")
	fmt.Fprintf(&sb, "%4s  %-8s  %6s  %-7s  %6s  %6s  %4s  %s\n", "id", "start", "length", "outcome", "dealt", "taken", "adds", "enemies")
	for _, h := range hs {
		fmt.Fprintf(&sb, "%4d  %-8s  %5.0fs  %-7s  %6d  %6d  %4d  %s\n", h.ID, time.UnixMilli(h.Start).Format("15:04:05"), h.Seconds,
			h.Outcome, h.DamageDealt, h.DamageTaken, h.Adds, strings.Join(h.Enemies, ", "))
	}
	return sb.String()
}

// view is a map's frame: the box it shows and its scale
type view struct {
	minX, maxX, minY, maxY float64
	yd                     float64 // yards across a column; a row is twice that
	cols, rows             int
}

// newView fits every position of the fight into at most maxCols x maxRows, north up.
func newView(f types.Fight, maxCols, maxRows int) view {
	v := view{minX: math.Inf(1), maxX: math.Inf(-1), minY: math.Inf(1), maxY: math.Inf(-1)}
	for _, fr := range f.Frames {
		for _, s := range fr.At {
			v.minX, v.maxX = math.Min(v.minX, s.X), math.Max(v.maxX, s.X)
			v.minY, v.maxY = math.Min(v.minY, s.Y), math.Max(v.maxY, s.Y)
		}
	}
	if math.IsInf(v.minX, 1) {
		v.minX, v.maxX, v.minY, v.maxY = 0, 0, 0, 0
	}
	v.minX, v.maxX, v.minY, v.maxY = v.minX-4, v.maxX+4, v.minY-4, v.maxY+4
	for _, yd := range []float64{1, 2, 3, 4, 5, 6, 8, 10, 15, 20, 30, 50} {
		v.yd = yd
		v.cols, v.rows = int((v.maxY-v.minY)/yd)+1, int((v.maxX-v.minX)/(2*yd))+1
		if v.cols <= maxCols && v.rows <= maxRows {
			break
		}
	}
	v.cols, v.rows = min(v.cols, maxCols), min(v.rows, maxRows)
	return v
}

// cell: x runs north (up), y west (left)
func (v view) cell(x, y float64) (int, int, bool) {
	r, c := int(math.Round((v.maxX-x)/(2*v.yd))), int(math.Round((v.maxY-y)/v.yd))
	return r, c, r >= 0 && r < v.rows && c >= 0 && c < v.cols
}

func (v view) blank() [][]byte {
	g := make([][]byte, v.rows)
	for r := range g {
		g[r] = []byte(strings.Repeat(".", v.cols))
	}
	return g
}

func gridString(g [][]byte) string {
	var sb strings.Builder
	for _, row := range g {
		sb.Write(row)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func trailChar(label string) byte {
	if label == "@" {
		return 'o'
	}
	return strings.ToLower(label)[0]
}

// constellation: everyone's path through the fight on one map
func constellation(f types.Fight, v view) string {
	g := v.blank()
	last := map[string]types.FightSpot{}
	var start *types.FightSpot
	for _, fr := range f.Frames {
		for _, s := range fr.At {
			if r, c, ok := v.cell(s.X, s.Y); ok && g[r][c] == '.' {
				g[r][c] = trailChar(s.Label)
			}
			if s.Label == "@" && start == nil && fr.S >= 0 {
				sp := s
				start = &sp
			}
			last[s.Label] = s
		}
	}
	if start != nil {
		if r, c, ok := v.cell(start.X, start.Y); ok {
			g[r][c] = '*'
		}
	}
	labels := make([]string, 0, len(last))
	for l := range last {
		labels = append(labels, l)
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i] == "@" || labels[i] < labels[j] && labels[j] != "@" })
	for i := len(labels) - 1; i >= 0; i-- { // @ last, on top
		s := last[labels[i]]
		if r, c, ok := v.cell(s.X, s.Y); ok {
			g[r][c] = labels[i][0]
		}
	}
	return gridString(g)
}

// frameMap: where everyone was at one moment
func frameMap(fr types.FightFrame, v view) string {
	g := v.blank()
	for i := len(fr.At) - 1; i >= 0; i-- {
		s := fr.At[i]
		if r, c, ok := v.cell(s.X, s.Y); ok {
			ch := s.Label[0]
			if !s.Alive && s.Label != "@" {
				ch = 'x'
			}
			g[r][c] = ch
		}
	}
	for _, s := range fr.At { // you on top
		if s.Label == "@" {
			if r, c, ok := v.cell(s.X, s.Y); ok {
				g[r][c] = '@'
			}
		}
	}
	return gridString(g)
}

// status: one line of who's where at a moment: your health, your party's distance and health, the enemies' health
func status(f types.Fight, fr types.FightFrame, kinds map[string]string) string {
	var me *types.FightSpot
	for i := range fr.At {
		if fr.At[i].Label == "@" {
			me = &fr.At[i]
		}
	}
	var parts []string
	if me != nil {
		parts = append(parts, fmt.Sprintf("@ %.0f%%", me.HP*100))
	}
	for _, s := range fr.At {
		switch kinds[s.Label] {
		case "party", "player":
			p := fmt.Sprintf("%s %.0f%%", s.Label, s.HP*100)
			if me != nil {
				p += fmt.Sprintf(" %.0f yd %s", dist2(me, &s), compassOf(s.X-me.X, s.Y-me.Y))
			}
			if !s.Alive {
				p = s.Label + " dead"
			}
			parts = append(parts, p)
		case "enemy":
			if s.Alive {
				parts = append(parts, fmt.Sprintf("%s %.0f%%", s.Label, s.HP*100))
			}
		}
	}
	return strings.Join(parts, " · ")
}

type moment struct {
	s    float64
	what string
}

// moments: the points that changed the fight: the pull, each add, your health past half and a fifth, deaths, the end
func moments(f types.Fight) []moment {
	var ms []moment
	if p := f.Summary.Positioning; p != nil {
		for i, j := range p.Joins {
			if i == 0 {
				ms = append(ms, moment{j.S, fmt.Sprintf("%s %s pulled, %.0f yd %s", j.Label, j.Name, j.Distance, j.From)})
				continue
			}
			w := fmt.Sprintf("%s %s joins from %.0f yd %s", j.Label, j.Name, j.Distance, j.From)
			if j.Near != "" {
				w += " (near " + j.Near + ": its pack)"
			}
			ms = append(ms, moment{j.S, w})
		}
	}
	if len(ms) == 0 {
		ms = append(ms, moment{0, "the fight starts"})
	}
	hpMark := map[float64]bool{}
	alive := map[string]bool{}
	for _, fr := range f.Frames {
		if fr.S < 0 {
			for _, s := range fr.At {
				alive[s.Label] = s.Alive
			}
			continue
		}
		for _, s := range fr.At {
			if s.Label == "@" {
				for _, th := range []float64{0.5, 0.2} {
					if s.Alive && s.HP < th && !hpMark[th] {
						hpMark[th] = true
						ms = append(ms, moment{fr.S, fmt.Sprintf("you're below %.0f%% health", th*100)})
					}
				}
			}
			if was, seen := alive[s.Label]; (!seen || was) && !s.Alive && s.Label != "@" {
				ms = append(ms, moment{fr.S, s.Label + " dies"})
			}
			if was := alive[s.Label]; was && !s.Alive && s.Label == "@" {
				ms = append(ms, moment{fr.S, "you die"})
			}
			alive[s.Label] = s.Alive
		}
	}
	if n := len(f.Frames); n > 0 {
		ms = append(ms, moment{f.Frames[n-1].S, "end: " + f.Summary.Outcome})
	}
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].s < ms[j].s })
	var out []moment // merge what happens within a second and a half of each other
	for _, m := range ms {
		if n := len(out); n > 0 && m.s-out[n-1].s < 1.5 {
			out[n-1].what += "; " + m.what
			continue
		}
		out = append(out, m)
	}
	if len(out) > 8 { // keep the first, the last and the 6 in between that come first
		out = append(out[:7], out[len(out)-1])
	}
	return out
}

func frameAt(f types.Fight, s float64) types.FightFrame {
	best := types.FightFrame{}
	bd := math.Inf(1)
	for _, fr := range f.Frames {
		if d := math.Abs(fr.S - s); d < bd {
			best, bd = fr, d
		}
	}
	return best
}

// renderFight: one fight as text. mapOnly leaves out the summary and the timeline; at shows only the map at that
// second.
func renderFight(f types.Fight, mapOnly bool, at *float64) string {
	var sb strings.Builder
	s := f.Summary
	kinds := map[string]string{}
	names := map[string]string{}
	for _, a := range f.Actors {
		kinds[a.Label], names[a.Label] = a.Kind, a.Name
	}
	fmt.Fprintf(&sb, "fight %d · %s · %.1f s · %s · dealt %d, taken %d, healed %d, healed by others %d · xp %d\n", f.ID,
		time.UnixMilli(f.Start).Format("15:04:05"), s.Seconds, s.Outcome, s.DamageDealt, s.DamageTaken, s.HealingDone, s.HealingTaken, s.XP)
	var who []string
	for _, a := range f.Actors {
		w := a.Label + " " + a.Name
		if a.Kind != "enemy" {
			w += " (" + a.Kind + ")"
		}
		if a.Level > 0 {
			w += fmt.Sprintf(" L%d", a.Level)
		}
		who = append(who, w)
	}
	fmt.Fprintf(&sb, "who: %s\n", strings.Join(who, " · "))
	big := newView(f, 61, 25)
	scale := fmt.Sprintf("north up, %g yd across a column, %g down a row", big.yd, 2*big.yd)

	if at != nil {
		fr := frameAt(f, *at)
		fmt.Fprintf(&sb, "\nat %+.1f s (%s; x dead):\n%s%s\n", fr.S, scale, frameMap(fr, big), status(f, fr, kinds))
		return sb.String()
	}
	if p := s.Positioning; p != nil && !mapOnly {
		sb.WriteString("\npositioning:\n")
		fmt.Fprintf(&sb, "  you moved %.0f yd\n", p.MovedYd)
		for i, j := range p.Joins {
			if i == 0 {
				fmt.Fprintf(&sb, "  %s %s pulled at %.1f s, %.0f yd %s of you\n", j.Label, j.Name, j.S, j.Distance, j.From)
				continue
			}
			add := ""
			if j.S-p.Joins[0].S >= fightAddGap {
				add = ", an add"
			}
			near := ""
			if j.Near != "" {
				near = " (near " + j.Near + ": its pack)"
			}
			fmt.Fprintf(&sb, "  %s %s joined at %.1f s from %.0f yd %s%s%s\n", j.Label, j.Name, j.S, j.Distance, j.From, near, add)
		}
		for _, a := range p.Apart {
			fmt.Fprintf(&sb, "  %s %s was more than %d yd from you for %.0f s (up to %.0f yd): out of heal range\n", a.Label, a.Name, fightApartYd, a.Seconds, a.MaxYd)
		}
		if d := p.Died; d != nil {
			fmt.Fprintf(&sb, "  you died with %d enemies within 10 yd", d.EnemiesWithin10)
			if d.NearestPartyYd != nil {
				fmt.Fprintf(&sb, "; the nearest party member %.0f yd away", *d.NearestPartyYd)
			}
			sb.WriteString("\n")
		}
	}
	fmt.Fprintf(&sb, "\nmovement (%s; each one's trail in its lowercase letter, where it ended in its capital; @ you, o your trail, * where you were when it started):\n%s",
		scale, constellation(f, big))
	small := newView(f, 41, 13)
	sb.WriteString("\nkey moments:\n")
	for _, m := range moments(f) {
		fr := frameAt(f, m.s)
		fmt.Fprintf(&sb, "%+.1f s  %s\n        %s\n%s", m.s, m.what, status(f, fr, kinds), frameMap(fr, small))
	}
	if mapOnly {
		return sb.String()
	}
	sb.WriteString("\ntimeline (seconds into the fight):\n")
	for i, e := range f.Timeline {
		if i == fightTimelineN {
			fmt.Fprintf(&sb, "... %d more (combat.fight(%d).timeline has them all)\n", len(f.Timeline)-i, f.ID)
			break
		}
		fmt.Fprintf(&sb, "%+6.1f  %s\n", e.S, entryText(e, names))
	}
	return sb.String()
}

func entryText(e types.FightEntry, names map[string]string) string {
	n := func(l string) string {
		if names[l] != "" && l != "@" {
			return l + " " + names[l]
		}
		return l
	}
	what := e.Spell
	if what == "" {
		what = "melee"
	}
	out := ""
	if e.Outcome != "" {
		out = " (" + e.Outcome + ")"
	}
	switch e.Kind {
	case "damage":
		return fmt.Sprintf("%s → %s  %s %d%s", n(e.Src), n(e.Dst), what, e.Amount, out)
	case "heal":
		return fmt.Sprintf("%s heals %s  %s %d%s", n(e.Src), n(e.Dst), what, e.Amount, out)
	case "cast":
		return fmt.Sprintf("%s casts %s", n(e.Src), e.Spell)
	case "kill":
		return fmt.Sprintf("%s killed %s", n(e.Src), n(e.Dst))
	case "death":
		return fmt.Sprintf("%s died", n(e.Dst))
	case "join":
		return fmt.Sprintf("%s joins the fight", n(e.Src))
	}
	return e.Kind
}
