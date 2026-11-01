package main

// The Stats tab: a metric per player over a window ending at the playhead (the current fight, the last minute to
// hour, the whole run): party tiles, a ranked list, a chart and an activity strip (SVG), and the selected player's
// breakdown. A sum adds up events (or each step between position samples) in the window; a gauge is a value at a
// moment.

import (
	"cmp"
	"fmt"
	"html/template"
	"math"
	"slices"
	"strconv"
	"strings"
)

// statDef is how a metric is counted. who: the player an event counts for (its "n" by default); val: how much (1 by
// default); rate: also shown per second ("s") or hour ("h"); by: what the selected player's total breaks down by.
type statDef struct {
	ev      string
	who     func(Ev) string
	val     func(Ev) float64
	track   func(a, b Sample, dt float64) float64
	gauge   func(p *Player, t float64) (float64, bool)
	rate    string
	by      func(Ev) string
	byLabel string
	format  string // time, gold, yd or ""
	unit    string
	share   bool // a time: also shown as a share of the window
}

// what a player was doing between two samples
const (
	idle = iota + 1
	moving
	combat
	ghost
	dead
)

var stateName = map[int][2]string{idle: {"idle", "#4a4a47"}, moving: {"moving", "#3f8f5a"}, combat: {"in combat", "#e04848"}, ghost: {"ghost", "#8fb8ff"}, dead: {"dead", "#8a8985"}}
var stateOrder = []int{combat, moving, idle, ghost, dead}

func stateOf(a Sample, b *Sample) int {
	if !a.Alive {
		if a.Ghost {
			return ghost
		}
		return dead
	}
	if a.Combat {
		return combat
	}
	if b != nil {
		if dt := b.T - a.T; dt > 0 && dt <= 5000 && a.M == b.M && math.Hypot(b.X-a.X, b.Y-a.Y)/dt > 0.0005 { // over 0.5 yd/s
			return moving
		}
	}
	return idle
}

func unitField(k string) func(Ev) string {
	return func(e Ev) string {
		if u, ok := e[k].(map[string]any); ok && Truthy(u["pl"]) {
			return S(u, "n")
		}
		return ""
	}
}

func unitNamed(k string) func(Ev) string {
	return func(e Ev) string { u, _ := e[k].(map[string]any); return S(u, "n") }
}

func field(k string) func(Ev) float64 { return func(e Ev) float64 { return N(e, k) } }

func countOr1(e Ev) float64 { return math.Max(1, N(e, "count")) }

func secsIf(f func(a, b Sample) bool) func(a, b Sample, dt float64) float64 {
	return func(a, b Sample, dt float64) float64 {
		if f(a, b) {
			return dt / 1000
		}
		return 0
	}
}

func scoreGauge(k string) func(p *Player, t float64) (float64, bool) {
	return func(p *Player, t float64) (float64, bool) {
		e := p.ScoreAt(t)
		if e == nil {
			return 0, false
		}
		return N(e, k), true
	}
}

func (s *Store) statDefs() map[string]statDef {
	chatChannel := func(e Ev) string {
		if tag, ok := chatTag[int(N(e, "type"))]; ok {
			return tag
		}
		return or(map[int]string{1: "Say", 6: "Yell", 7: "Whisper", 10: "Emote", 17: "Channel"}[int(N(e, "type"))], "Other")
	}
	return map[string]statDef{
		"dmg":    {ev: "dmg", who: unitField("src"), val: field("amount"), rate: "s", by: unitNamed("dst"), byLabel: "target"},
		"taken":  {ev: "dmg", who: unitField("dst"), val: field("amount"), rate: "s", by: unitNamed("src"), byLabel: "source"},
		"heal":   {ev: "heal", who: unitField("src"), val: field("amount"), rate: "s", by: unitNamed("dst"), byLabel: "target"},
		"healed": {ev: "heal", who: unitField("dst"), val: field("amount"), rate: "s", by: unitNamed("src"), byLabel: "healer"},
		"kills":  {ev: "kill", rate: "h", by: unitNamed("victim"), byLabel: "victim"},
		"deaths": {ev: "death", by: unitNamed("killer"), byLabel: "killer"},
		"casts":  {ev: "cast", rate: "h", by: func(e Ev) string { return S(e, "name") }, byLabel: "spell"},
		"combat": {track: secsIf(func(a, b Sample) bool { return a.Combat }), format: "time", share: true},
		"lowhp":  {track: secsIf(func(a, b Sample) bool { return a.Alive && float64(a.HP) < 0.35*float64(a.HPMax) }), format: "time", share: true},
		"oom": {track: secsIf(func(a, b Sample) bool {
			return a.Alive && a.PT == 0 && a.PWMax > 0 && float64(a.PW) < 0.1*float64(a.PWMax)
		}), format: "time", share: true},
		"score":    {gauge: scoreGauge("score"), unit: "/h"},
		"progress": {gauge: scoreGauge("value")},
		"pace":     {gauge: scoreGauge("pace")},
		"recent":   {gauge: scoreGauge("recent_rate"), unit: "/h"},
		"xp":       {ev: "xp", val: field("amount"), rate: "h"},
		"quests":   {ev: "quest", rate: "h", by: func(e Ev) string { return or(S(e, "title"), fmt.Sprint("quest ", N(e, "id"))) }, byLabel: "quest"},
		"levels":   {ev: "level"},
		"level": {gauge: func(p *Player, t float64) (float64, bool) {
			pp, ok := p.At(t)
			return float64(pp.Lvl), ok
		}},
		"loot": {ev: "loot", val: countOr1, by: func(e Ev) string {
			if it, ok := s.items[int(N(e, "item"))]; ok {
				return it.Name
			}
			return fmt.Sprint("item ", N(e, "item"))
		}, byLabel: "item"},
		"money": {track: func(a, b Sample, dt float64) float64 { return float64(b.Money - a.Money) }, format: "gold", rate: "h"},
		"gold": {gauge: func(p *Player, t float64) (float64, bool) {
			pp, ok := p.At(t)
			return float64(pp.Money), ok
		}, format: "gold"},
		"distance": {track: func(a, b Sample, dt float64) float64 {
			if a.M != b.M {
				return 0
			}
			if d := math.Hypot(b.X-a.X, b.Y-a.Y); d/dt <= 0.04 { // over 40 yd/s: a teleport
				return d
			}
			return 0
		}, format: "yd", rate: "h"},
		"dead":    {track: secsIf(func(a, b Sample) bool { return !a.Alive }), format: "time", share: true},
		"idle":    {track: secsIf(func(a, b Sample) bool { return stateOf(a, &b) == idle }), format: "time", share: true},
		"moving":  {track: secsIf(func(a, b Sample) bool { return stateOf(a, &b) == moving }), format: "time", share: true},
		"grouped": {track: secsIf(func(a, b Sample) bool { return a.Grp != 0 }), format: "time", share: true},
		"say":     {ev: "say", rate: "h"},
		"api":     {ev: "api", rate: "h", by: func(e Ev) string { return S(e, "method") }, byLabel: "method"},
		"errors":  {ev: "script_error", val: countOr1},
		"chat": {ev: "chat", rate: "h", by: chatChannel, byLabel: "channel", who: func(e Ev) string {
			if S(e, "from") != "" {
				return ""
			}
			return S(e, "n")
		}},
	}
}

// ---------- numbers ----------

func short(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1e6:
		return strconv.FormatFloat(v/1e6, 'f', 1, 64) + "M"
	case a >= 1e4:
		return strconv.FormatFloat(v/1e3, 'f', 1, 64) + "k"
	case a < 10 && v != math.Trunc(v):
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
}

func pct(f float64) string {
	if f > 0 && f < 0.005 {
		return "<1%"
	}
	return fmt.Sprintf("%.0f%%", math.Round(100*f))
}

func dur(sec float64) string {
	s := int(math.Round(sec))
	h, m := s/3600, s/60%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, s%60)
	}
	return fmt.Sprintf("%ds", s)
}

func fmtStat(m statDef, v float64) string {
	switch m.format {
	case "time":
		return dur(v)
	case "gold":
		sign := ""
		if v < 0 {
			sign = "−"
		}
		return sign + Money(int(math.Abs(math.Round(v))))
	case "yd":
		return short(v) + " yd"
	}
	return short(v) + m.unit
}

// ---------- the time grid ----------

// grid cuts [t0, t1] into cells of a round length (the smallest giving at most n cells, or fixed), counted from the
// run's start, so a cell's contents don't change once the playhead has passed it: the plots scroll a cell at a time.
type grid struct {
	g0, step, t1 float64
	nb           int
}

var steps = []float64{1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400}
var periods = []float64{10, 60, 300, 900, 3600} // what the rate chart can be per, in seconds

func (s *Store) grid(t0, t1 float64, n int, fixed float64) grid {
	step := fixed
	if step == 0 {
		step = steps[len(steps)-1] * 1000
		for _, x := range steps {
			if (t1-t0)/(x*1000) <= float64(n) {
				step = x * 1000
				break
			}
		}
	}
	g0 := s.Start + math.Floor((t0-s.Start)/step)*step
	return grid{g0: g0, step: step, t1: t1, nb: max(1, int(math.Ceil((t1-g0)/step)))}
}

func (g grid) end(k int) float64 { return math.Min(g.g0+float64(k+1)*g.step, g.t1) }

func periodName(p float64) string {
	switch {
	case p == 1:
		return "s"
	case p < 60:
		return fmt.Sprintf("%g s", p)
	case p == 60:
		return "min"
	case p < 3600:
		return fmt.Sprintf("%g min", p/60)
	case p == 3600:
		return "hour"
	}
	return fmt.Sprintf("%g h", p/3600)
}

// ---------- counting ----------

// statRange is the window ending at t: a fight runs back from the last damage or heal to a quiet gap of 10 s.
func (s *Store) statRange(rng string, t float64) (float64, float64) {
	switch rng {
	case "all":
		return s.Start, t
	case "fight":
		hi := uptoEv(s.meter, t)
		if hi == 0 {
			return t, t
		}
		lo := hi - 1
		for lo > 0 && N(s.meter[lo], "t")-N(s.meter[lo-1], "t") <= 10000 {
			lo--
		}
		end := t
		if t-N(s.meter[hi-1], "t") > 10000 {
			end = N(s.meter[hi-1], "t")
		}
		return N(s.meter[lo], "t"), end
	}
	secs, _ := strconv.ParseFloat(rng, 64)
	return math.Max(s.Start, t-secs*1000), t
}

type statResult struct {
	tot     map[string]float64
	buckets map[string][]float64
	by      map[string]float64
}

// compute totals each player's metric over [t0, t1], the same in g's cells (cumulative: running totals from the
// grid's start), and the selected player's total broken down by m.by. only: the selected group's players, or nil.
func (s *Store) compute(m statDef, t0, t1 float64, g *grid, cumulative bool, sel string, only map[string]bool) statResult {
	r := statResult{tot: map[string]float64{}, buckets: map[string][]float64{}, by: map[string]float64{}}
	from := t0
	if g != nil {
		from = math.Min(t0, g.g0)
	}
	add := func(n string, v, at float64, e Ev) {
		if only != nil && !only[n] {
			return
		}
		if at >= t0 {
			r.tot[n] += v
			if e != nil && m.by != nil && n == sel {
				r.by[or(m.by(e), "?")] += v
			}
		}
		if g != nil {
			b := r.buckets[n]
			if b == nil {
				b = make([]float64, g.nb)
				r.buckets[n] = b
			}
			b[max(0, min(g.nb-1, int(math.Floor((at-g.g0)/g.step))))] += v
		}
	}
	switch {
	case m.gauge != nil:
		for _, p := range s.statPlayers(only) {
			if v, ok := m.gauge(p, t1); ok {
				r.tot[p.Name] = v
			}
			if g != nil {
				b := make([]float64, g.nb)
				for k := range b {
					v, ok := m.gauge(p, g.end(k))
					b[k] = v
					if !ok {
						b[k] = math.NaN()
					}
				}
				r.buckets[p.Name] = b
			}
		}
		return r
	case m.track != nil:
		at := func(x Sample) float64 { return x.T }
		for _, p := range s.statPlayers(only) {
			tr := p.Track
			i1 := upto(tr, t1, at) - 1
			for i := max(1, upto(tr, from, at)); i <= i1; i++ {
				a, b := tr[i-1], tr[i]
				if dt := b.T - a.T; dt > 0 && dt <= 5000 {
					if v := m.track(a, b, dt); v != 0 {
						add(p.Name, v, b.T, nil)
					}
				}
			}
		}
	default:
		list := s.byEv[m.ev]
		for i, end := uptoEv(list, from-1), uptoEv(list, t1); i < end; i++ {
			e := list[i]
			n := S(e, "n")
			if m.who != nil {
				n = m.who(e)
			}
			if n == "" {
				continue
			}
			v := 1.0
			if m.val != nil {
				v = m.val(e)
			}
			add(n, v, N(e, "t"), e)
		}
	}
	if g != nil && cumulative {
		for _, b := range r.buckets {
			for k := 1; k < len(b); k++ {
				b[k] += b[k-1]
			}
		}
	}
	return r
}

func (s *Store) statPlayers(only map[string]bool) []*Player {
	if only == nil {
		return s.players
	}
	var out []*Player
	for _, p := range s.players {
		if only[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

// activity is the seconds a player spent in each state: per cell of g, and over [t0, t1].
func activity(p *Player, t0, t1 float64, g grid) (cells [][6]float64, all [6]float64) {
	cells = make([][6]float64, g.nb)
	tr := p.Track
	at := func(x Sample) float64 { return x.T }
	i := max(0, upto(tr, math.Min(g.g0, t0), at)-1)
	i1 := upto(tr, t1, at) - 1
	for ; i >= 0 && i <= i1; i++ {
		a := tr[i]
		var b *Sample
		if i+1 < len(tr) {
			b = &tr[i+1]
		}
		st := stateOf(a, b)
		from := math.Max(a.T, g.g0)
		to := a.T + 1000
		if b != nil && b.T-a.T <= 5000 {
			to = b.T
		}
		to = math.Min(to, t1)
		for from < to { // split the step at cell edges, and at t0
			k := min(g.nb-1, int(math.Floor((from-g.g0)/g.step)))
			edge := math.Min(to, g.g0+float64(k+1)*g.step)
			if from < t0 {
				edge = math.Min(edge, t0)
			}
			if edge <= from {
				break
			}
			d := (edge - from) / 1000
			cells[k][st] += d
			if from >= t0 {
				all[st] += d
			}
			from = edge
		}
	}
	return cells, all
}

func split(v [6]float64) [][2]float64 {
	tot := 0.0
	for _, x := range v {
		tot += x
	}
	var out [][2]float64
	for _, k := range stateOrder {
		if v[k] > 0 {
			out = append(out, [2]float64{float64(k), v[k] / math.Max(1e-9, tot)})
		}
	}
	return out
}

// ---------- the panel ----------

// StatRow is a player's line in the ranked list, or a part of the selected player's breakdown.
type StatRow struct {
	Name, Color, Value, Extra, Pct, Title string
	Width                                 float64
	Sel                                   bool
}

// Stats is the Stats tab at q.T.
func (s *Store) Stats(q Query) map[string]any {
	defs := s.statDefs()
	key := q.Metric
	if _, ok := defs[key]; !ok {
		key = "dmg"
	}
	m, label := defs[key], key
	for _, x := range metrics {
		if x.Key == key {
			label = x.Label
		}
	}
	var only map[string]bool
	if q.Grp != 0 {
		only = map[string]bool{}
		for _, n := range s.focus(q) {
			only[n] = true
		}
	}
	w0, t1 := s.statRange(q.Range, q.T)
	rate := q.Chart == "rate" && m.gauge == nil
	var pers []float64
	for _, p := range periods {
		if n := (t1 - w0) / (p * 1000); n >= 3 && n <= 400 {
			pers = append(pers, p)
		}
	}
	period, _ := strconv.ParseFloat(q.Per, 64)
	if !rate || !slices.Contains(pers, period) {
		period = 0
	}
	var g grid
	if period > 0 {
		g = s.grid(w0, t1, 0, period*1000)
	} else {
		g = s.grid(w0, t1, 80, 0)
	}
	t0, secs := g.g0, math.Max(1, (t1-g.g0)/1000)
	r := s.compute(m, t0, t1, &g, !rate, q.Sel, only)
	fmtv := func(v float64) string { return fmtStat(statDef{format: m.format}, v) }
	if rate {
		for _, b := range r.buckets {
			for k := range b {
				if m.share {
					b[k] = 100 * b[k] / math.Max(1, g.end(k)-(g.g0+float64(k)*g.step)) * 1000
				}
			}
		}
		fmtv = func(v float64) string {
			switch {
			case m.format == "gold":
				return fmtStat(m, v)
			case m.share:
				return fmt.Sprintf("%.0f%%", v)
			}
			return short(v)
		}
	}
	// party tiles: the window's totals, each a shortcut to its metric
	type tile struct{ Key, Value, Label, Title string }
	var tiles []tile
	for _, k := range []string{"dmg", "heal", "kills", "deaths", "xp", "quests"} {
		tot := 0.0
		for _, v := range s.compute(defs[k], t0, t1, nil, false, "", only).tot {
			tot += v
		}
		l := map[string]string{"dmg": "Damage", "heal": "Healing", "kills": "Kills", "deaths": "Deaths", "xp": "XP", "quests": "Quests"}[k]
		tiles = append(tiles, tile{k, fmtStat(defs[k], tot), l, l})
	}
	// ranked players; per-second rates (DPS, HPS) are over each player's own time in combat, like Recount
	type kv struct {
		n string
		v float64
	}
	var ranked []kv
	sum, top := 0.0, 1e-9
	for n, v := range r.tot {
		ranked = append(ranked, kv{n, v})
		sum += math.Max(0, v)
		top = math.Max(top, math.Abs(v))
	}
	slices.SortFunc(ranked, func(a, b kv) int { return cmp.Or(cmp.Compare(b.v, a.v), cmp.Compare(a.n, b.n)) })
	inCombat := map[string]float64{}
	if m.rate == "s" {
		inCombat = s.compute(defs["combat"], t0, t1, nil, false, "", only).tot
	}
	perSec := func(v float64, n string) float64 { return v / math.Max(1, inCombat[n]) }
	partySec := 0.0
	for _, x := range ranked {
		partySec += perSec(x.v, x.n)
	}
	extra := func(v float64, n string) string {
		switch {
		case m.gauge != nil:
			return ""
		case m.share:
			return pct(v / secs)
		case m.rate == "s" && n != "":
			return short(perSec(v, n)) + "/s"
		case m.rate == "s":
			return short(partySec) + "/s"
		case m.rate == "h" && secs >= 60:
			return fmtStat(statDef{format: m.format}, v*3600/secs) + "/h"
		}
		return ""
	}
	var rows []StatRow
	for _, x := range ranked {
		row := StatRow{Name: x.n, Color: s.color(x.n), Value: fmtStat(m, x.v), Extra: extra(x.v, x.n), Width: 100 * math.Abs(x.v) / top, Sel: q.Sel == x.n}
		if m.gauge == nil && !m.share && sum > 0 {
			row.Pct = fmt.Sprintf("%.0f%%", math.Round(100*math.Max(0, x.v)/sum))
		}
		if m.rate == "s" {
			row.Title = "per second in combat (" + dur(inCombat[x.n]) + ")"
		}
		rows = append(rows, row)
	}
	// the selected player's breakdown
	var parts []StatRow
	more := 0
	if q.Sel != "" && m.by != nil {
		var bs []kv
		ptot := 0.0
		for k, v := range r.by {
			bs = append(bs, kv{k, v})
			ptot += v
		}
		slices.SortFunc(bs, func(a, b kv) int { return cmp.Or(cmp.Compare(b.v, a.v), cmp.Compare(a.n, b.n)) })
		if len(bs) > 10 {
			more = len(bs) - 10
			bs = bs[:10]
		}
		for _, x := range bs {
			parts = append(parts, StatRow{Name: x.n, Color: s.color(q.Sel), Value: fmtStat(m, x.v), Width: 100 * x.v / bs[0].v,
				Pct: fmt.Sprintf("%.0f%%", math.Round(100*x.v/math.Max(1, ptot)))})
		}
	}
	total := ""
	if m.gauge == nil {
		total = fmtStat(m, sum)
	}
	var perOpts []map[string]any
	if rate {
		word := "per "
		if m.share {
			word = "each "
		}
		perOpts = append(perOpts, map[string]any{"V": "0", "L": "Auto (" + word + periodName(g.step/1000) + ")", "Sel": period == 0})
		for _, p := range pers {
			perOpts = append(perOpts, map[string]any{"V": strconv.FormatFloat(p, 'f', -1, 64), "L": word + periodName(p), "Sel": p == period})
		}
	}
	rangeNote := ""
	if q.Range == "fight" {
		rangeNote = "fight "
	}
	return map[string]any{
		"Label": label, "Range": rangeNote + Fmt(t0-s.Start) + "–" + Fmt(t1-s.Start), "Dur": dur(secs), "Total": total, "TotalExtra": extra(sum, ""),
		"Tiles": tiles, "Metric": key, "Rows": rows, "Gauge": m.gauge != nil, "Rate": rate, "Share": m.share, "PerOpts": perOpts,
		"Chart": s.chartSVG(r.buckets, g, fmtv, q.Sel, label, only), "Strip": s.stripSVG(t0, t1, only, q.Sel),
		"Sel": q.Sel, "SelColor": s.color(q.Sel), "ByLabel": m.byLabel, "HasBy": q.Sel != "" && m.by != nil, "Parts": parts, "More": more,
		"Score": label == "Score" || key == "progress" || key == "pace" || key == "recent", "Only": s.only(q, s.focus(q)),
		"States": func() (out [][2]string) {
			for _, k := range []int{idle, moving, combat, ghost, dead} {
				out = append(out, stateName[k])
			}
			return
		}(),
	}
}

func (s *Store) color(n string) string {
	if p := s.byName[n]; p != nil {
		return ClassColor(p.Cls)
	}
	return "#888888"
}

// ---------- SVG ----------

const chartW, chartH, padL, padR = 480.0, 170.0, 58.0, 58.0

func fmtAxis(ms float64) string {
	if ms >= 3600000 {
		f := Fmt(ms)
		return f[:len(f)-3] // h:mm
	}
	return Fmt(ms)
}

// chartSVG is the metric over the window, a line per player, the selected one thicker with a fill; each cell has a
// hover target with every player's value there.
func (s *Store) chartSVG(buckets map[string][]float64, g grid, fmtv func(float64) string, sel, label string, only map[string]bool) template.HTML {
	lo, hi := 0.0, 0.0
	first := true
	for _, b := range buckets {
		for _, v := range b {
			if math.IsNaN(v) {
				continue
			}
			if first {
				hi, first = v, false
			}
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
	}
	if hi <= lo {
		hi = lo + 1e-9
	}
	H := chartH - 18 - 8
	W := chartW - padL - padR
	a0, a1 := g.g0, g.g0+float64(g.nb)*g.step
	X := func(k int) float64 { return padL + (g.end(k)-a0)/(a1-a0)*W }
	Y := func(v float64) float64 { return 6 + H - (v-lo)/(hi-lo)*H }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %g %g" role="img" aria-label="%s over time">`, chartW, chartH, esc(label))
	for k := 0; k <= 3; k++ {
		v := lo + (hi-lo)*float64(k)/3
		y := math.Round(Y(v)) + 0.5
		dash := ""
		if k > 0 {
			dash = ` stroke-dasharray="2 4"`
		}
		fmt.Fprintf(&b, `<line x1="%g" x2="%g" y1="%g" y2="%g" class="grid"%s/><text x="%g" y="%g" text-anchor="end" class="ax">%s</text>`, padL, padL+W, y, y, dash, padL-6, y+4, esc(fmtv(v)))
	}
	for k := 0; k <= 3; k++ {
		anchor := []string{"start", "middle", "middle", "end"}[k]
		fmt.Fprintf(&b, `<text x="%g" y="%g" text-anchor="%s" class="ax">%s</text>`, padL+W*float64(k)/3, chartH-3, anchor, fmtAxis(a0+(a1-a0)*float64(k)/3-s.Start))
	}
	ps := s.statPlayers(only)
	order := slices.Clone(ps)
	slices.SortStableFunc(order, func(a, b *Player) int { return cmp.Compare(boolInt(a.Name == sel), boolInt(b.Name == sel)) }) // the selected line on top
	type end struct {
		y     float64
		v     string
		color string
		dim   bool
	}
	var ends []end
	for _, p := range order {
		bk := buckets[p.Name]
		if bk == nil {
			continue
		}
		var d strings.Builder
		pen := false
		lastK := -1
		for k, v := range bk {
			if math.IsNaN(v) {
				pen = false
				continue
			}
			cmd := "L"
			if !pen {
				cmd = "M"
			}
			fmt.Fprintf(&d, "%s%.1f %.1f", cmd, X(k), Y(v))
			pen, lastK = true, k
		}
		if d.Len() == 0 {
			continue
		}
		c := ClassColor(p.Cls)
		width, op := 1.6, 1.0
		if sel != "" && p.Name != sel {
			op = 0.3
		}
		if p.Name == sel {
			width = 3
			fmt.Fprintf(&b, `<path d="%s L%.1f %.1f L%.1f %.1f Z" fill="%s" opacity=".18"/>`, d.String(), X(lastK), Y(lo), X(firstK(bk)), Y(lo), c)
		}
		dash := ""
		if s.classIndex(p) > 1 {
			dash = ` stroke-dasharray="6 4"`
		}
		fmt.Fprintf(&b, `<path d="%s" fill="none" stroke="%s" stroke-width="%g" opacity="%g"%s stroke-linejoin="round"/>`, d.String(), c, width, op, dash)
		ends = append(ends, end{Y(bk[lastK]), fmtv(bk[lastK]), c, sel != "" && p.Name != sel})
	}
	// the latest values in the right margin, nudged apart so they don't overlap
	slices.SortFunc(ends, func(a, b end) int { return cmp.Compare(a.y, b.y) })
	lastY := -1e9
	for _, e := range ends {
		y := math.Max(e.y+4, lastY+12)
		lastY = y
		op := 1.0
		if e.dim {
			op = 0.4
		}
		fmt.Fprintf(&b, `<text x="%g" y="%g" class="end" fill="%s" opacity="%g">%s</text>`, padL+W+6, y, e.color, op, esc(e.v))
	}
	// hover targets: a column per cell, its tooltip every player's value there
	for k := 0; k < g.nb; k++ {
		x0 := padL + (g.g0+float64(k)*g.step-a0)/(a1-a0)*W
		var tip strings.Builder // plain text, made into HTML by the client: a title line, then label<TAB>value lines
		fmt.Fprintf(&tip, "%s · %s", Fmt(g.end(k)-s.Start), label)
		type pv struct {
			n string
			v float64
		}
		var vals []pv
		for _, p := range ps {
			if bk := buckets[p.Name]; bk != nil && !math.IsNaN(bk[k]) {
				vals = append(vals, pv{p.Name, bk[k]})
			}
		}
		slices.SortFunc(vals, func(a, b pv) int { return cmp.Compare(b.v, a.v) })
		for _, x := range vals {
			fmt.Fprintf(&tip, "\n%s\t%s", x.n, fmtv(x.v))
		}
		fmt.Fprintf(&b, `<rect class="hov" x="%.1f" y="6" width="%.1f" height="%g" data-tip="%s"/>`, x0, X(k)-x0, H, esc(tip.String()))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func firstK(b []float64) int {
	for k, v := range b {
		if !math.IsNaN(v) {
			return k
		}
	}
	return 0
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// stripSVG is what each player was doing across the window: a row each of equal cells, coloured by the state that
// took most of a cell (a thin line when they were logged out for most of it), then the whole window's split.
func (s *Store) stripSVG(t0, t1 float64, only map[string]bool, sel string) template.HTML {
	const rh, gap, sw = 12.0, 7.0, padR - 10
	W := chartW - padL - padR
	g := s.grid(t0, t1, 44, 0)
	ps := s.statPlayers(only)
	h := math.Max(1, float64(len(ps)))*(rh+gap) - gap + 2
	a0, a1 := g.g0, g.g0+float64(g.nb)*g.step
	xOf := func(tm float64) float64 { return padL + (tm-a0)/(a1-a0)*W }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="strip" viewBox="0 0 %g %g" role="img" aria-label="activity">`, chartW, h)
	tipOf := func(name, when string, v [6]float64) string { // see chartSVG's tooltips; a third field is a swatch colour
		t := name + " · " + when
		for _, x := range split(v) {
			st := stateName[int(x[0])]
			t += fmt.Sprintf("\n%s\t%s\t%s", st[0], pct(x[1]), st[1])
		}
		return t
	}
	for row, p := range ps {
		y := float64(row)*(rh+gap) + 1
		op := 1.0
		if sel != "" && p.Name != sel {
			op = 0.4
		}
		cells, all := activity(p, t0, t1, g)
		fmt.Fprintf(&b, `<g opacity="%g"><text x="0" y="%g" class="name" fill="%s">%s</text>`, op, y+rh/2+4, ClassColor(p.Cls), esc(p.Name))
		for k, v := range cells {
			c0, c1 := g.g0+float64(k)*g.step, g.end(k)
			x0, x1 := math.Max(padL, xOf(c0)), math.Min(padL+W, xOf(c1))
			if x1-x0 < 1 {
				continue
			}
			best, covered := 0, 0.0
			for _, st := range stateOrder {
				if v[st] > v[best] {
					best = st
				}
			}
			for _, x := range v {
				covered += x
			}
			tip := esc(tipOf(p.Name, Fmt(c0-s.Start)+"–"+Fmt(c1-s.Start), v))
			if best == 0 || covered < (c1-c0)/1000*0.3 { // logged out
				fmt.Fprintf(&b, `<rect x="%.1f" y="%g" width="%.1f" height="2" rx="1" class="off"/><rect class="hov" x="%.1f" y="%g" width="%.1f" height="%g" data-tip="%s"/>`, x0+0.5, y+rh/2-1, x1-x0-1, x0, y, x1-x0, rh, tip)
				continue
			}
			fmt.Fprintf(&b, `<rect x="%.1f" y="%g" width="%.1f" height="%g" rx="2" fill="%s" data-tip="%s"/>`, x0+0.5, y, math.Max(1, x1-x0-1), rh, stateName[best][1], tip)
		}
		x := padL + W + 10
		tip := esc(tipOf(p.Name, Fmt(t0-s.Start)+"–"+Fmt(t1-s.Start), all))
		for _, part := range split(all) {
			fmt.Fprintf(&b, `<rect x="%.1f" y="%g" width="%.1f" height="%g" fill="%s" data-tip="%s"/>`, x, y, part[1]*sw, rh, stateName[int(part[0])][1], tip)
			x += part[1] * sw
		}
		b.WriteString(`</g>`)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}
