package main

// The viewer's state, server side: everything read from a run's logs, indexed by player and time, so the page asks
// for what it shows at a moment (a few samples, a panel) instead of downloading every event.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/azworld/world"
)

// Sample is one position sample.
type Sample struct {
	T                                        float64
	X, Y                                     float64
	M, Zone, Grp                             int
	HP, HPMax, PW, PWMax, PT, Lvl, XP, Money int
	Alive, Ghost, Combat                     bool
}

func sampleOf(e Ev) Sample {
	i := func(k string) int { return int(N(e, k)) }
	return Sample{T: N(e, "t"), X: N(e, "x"), Y: N(e, "y"), M: i("m"), Zone: i("zone"), Grp: i("grp"),
		HP: i("hp"), HPMax: i("hpmax"), PW: i("pw"), PWMax: i("pwmax"), PT: i("pt"), Lvl: i("lvl"), XP: i("xp"), Money: i("money"),
		Alive: Truthy(e["alive"]), Ghost: Truthy(e["ghost"]), Combat: Truthy(e["combat"])}
}

// UnitRow is a creature around a player: entry, level, where, health % and state (0 hostile, 1 neutral, 2 friendly, +4 dead).
type UnitRow struct {
	Entry          int32
	Lvl, HP, State uint8
	X, Y           float32
}

// UnitsSample is a player's "units" sample: the creatures within 60 yd, every 2 s.
type UnitsSample struct {
	T float64
	M int
	U []UnitRow
}

func unitsOf(e Ev) UnitsSample {
	s := UnitsSample{T: N(e, "t"), M: int(N(e, "m"))}
	rows, _ := e["u"].([]any)
	for _, r := range rows {
		r, ok := r.([]any)
		if !ok || len(r) < 6 {
			continue
		}
		f := func(i int) float64 { v, _ := r[i].(float64); return v }
		s.U = append(s.U, UnitRow{Entry: int32(f(0)), Lvl: uint8(f(1)), X: float32(f(2)), Y: float32(f(3)), HP: uint8(f(4)), State: uint8(f(5))})
	}
	return s
}

// Player is one character: its samples, and the events read per player.
type Player struct {
	Name, Cls string
	Track     []Sample
	Units     []UnitsSample
	tgts      []Ev // its target changing (server log)
	hits      []Ev // its damage to creatures
	says      []Ev // its agent's messages
	scores    []Ev // judge samples
}

// Store holds a run. Everything is kept in time order; events arriving late (a file read after another) are inserted.
type Store struct {
	mu      sync.RWMutex
	Start   float64 // the run's start (its go file), else the first event
	players []*Player
	byName  map[string]*Player
	events  []Ev                 // every event but pos and units
	byEv    map[string][]Ev      // the same by kind
	rows    map[string][]statRow // the same with the fields the stats count read out (a map lookup each was most of their time)
	feed    []Ev                 // the events the feed lists
	marks   []Ev                 // deaths, achievements, levels, quests: on the map and the seek bar
	ticks   []Ev                 // marks and chat: the seek bar
	groups  []Ev                 // group changes
	meter   []Ev                 // damage and heals: a fight is a run of them
	lastGrp map[string]string
	npcSaid map[string]Ev // speaker|text -> the latest NPC chat event, to merge each listener's copy into one
	items   map[int]world.ItemName
	names   map[int]string  // creature names
	radii   map[int]float64 // creature sizes (bounding radius, yd)
	detect  map[int]float64 // creature detection ranges (the aggro radius at an equal level, yd)
	zones   map[int]string  // area names
	agents  map[string]any  // name -> meta.json
	subs    map[chan liveMsg]bool
	last    float64 // the newest event
}

// liveMsg is one server-sent event: its type and JSON.
type liveMsg struct {
	typ string
	b   []byte
}

func NewStore() *Store {
	return &Store{byName: map[string]*Player{}, byEv: map[string][]Ev{}, rows: map[string][]statRow{}, lastGrp: map[string]string{}, npcSaid: map[string]Ev{},
		items: map[int]world.ItemName{}, names: map[int]string{}, radii: map[int]float64{}, detect: map[int]float64{}, zones: map[int]string{}, agents: map[string]any{}, subs: map[chan liveMsg]bool{}}
}

// insert puts v into s, sorted by t; appending is the usual case.
func insert[T any](s []T, v T, t func(T) float64) []T {
	i := len(s)
	for i > 0 && t(s[i-1]) > t(v) {
		i--
	}
	s = append(s, v)
	if i < len(s)-1 {
		copy(s[i+1:], s[i:len(s)-1])
		s[i] = v
	}
	return s
}

var evT = func(e Ev) float64 { return N(e, "t") }

func (s *Store) player(n string) *Player {
	p := s.byName[n]
	if p == nil {
		p = &Player{Name: n}
		s.byName[n] = p
		s.players = append(s.players, p)
	}
	return p
}

var (
	markKinds = map[string]bool{"death": true, "achievement": true, "level": true, "quest": true}
	feedKinds = func() map[string]bool {
		m := map[string]bool{}
		for _, c := range feedCats {
			for _, k := range c.Kinds {
				m[k] = true
			}
		}
		return m
	}()
)

// add files one event; the caller holds the lock. False if it was merged away (a repeated group list, a second
// listener's copy of NPC speech).
func (s *Store) add(e Ev) bool {
	t, n, kind := N(e, "t"), S(e, "n"), S(e, "ev")
	s.last = math.Max(s.last, t)
	switch kind {
	case "pos":
		p := s.player(n)
		p.Track = insert(p.Track, sampleOf(e), func(x Sample) float64 { return x.T })
		return true
	case "units":
		p := s.player(n)
		p.Units = insert(p.Units, unitsOf(e), func(x UnitsSample) float64 { return x.T })
		return true
	case "chat":
		if from := S(e, "from"); from != "" { // NPC speech, once per agent that heard it: one event, heard = the listeners
			k := from + "|" + S(e, "msg")
			if first, ok := s.npcSaid[k]; ok && math.Abs(t-N(first, "t")) < 3000 {
				if h, _ := first["heard"].([]string); !slices.Contains(h, n) {
					first["heard"] = append(h, n)
				}
				return false
			}
			e["heard"] = []string{n}
			s.npcSaid[k] = e
		}
	case "group": // older client records repeat the group list unchanged dozens of times per change: keep changes
		b, _ := json.Marshal([]any{e["grp"], e["leader"], e["members"]})
		if s.lastGrp[n] == string(b) {
			return false
		}
		s.lastGrp[n] = string(b)
		s.groups = insert(s.groups, e, evT)
	case "login":
		if c := int(N(e, "cls")); c > 0 && c < len(Class) {
			s.player(n).Cls = Class[c]
		}
	case "tgt":
		s.player(n).tgts = insert(s.player(n).tgts, e, evT)
	case "say":
		s.player(n).says = insert(s.player(n).says, e, evT)
	case "score":
		s.player(n).scores = insert(s.player(n).scores, e, evT)
	case "dmg":
		if src, _ := e["src"].(map[string]any); src != nil && Truthy(src["pl"]) {
			if dst, _ := e["dst"].(map[string]any); dst == nil || !Truthy(dst["pl"]) {
				p := s.player(S(src, "n"))
				p.hits = insert(p.hits, e, evT)
			}
		}
	}
	s.events = insert(s.events, e, evT)
	s.byEv[kind] = insert(s.byEv[kind], e, evT)
	s.rows[kind] = insert(s.rows[kind], rowOf(e), func(r statRow) float64 { return r.T })
	if (kind == "dmg" || kind == "heal") && N(e, "amount") > 0 {
		s.meter = insert(s.meter, e, evT)
	}
	if feedKinds[kind] {
		s.feed = insert(s.feed, e, evT)
	}
	if markKinds[kind] {
		s.marks = insert(s.marks, e, evT)
	}
	if markKinds[kind] || kind == "chat" {
		s.ticks = insert(s.ticks, e, evT)
	}
	return true
}

// ---------- queries; callers hold the read lock ----------

// upto is the index of the first element after t in a time-sorted slice.
func upto[T any](a []T, t float64, at func(T) float64) int {
	lo, hi := 0, len(a)
	for lo < hi {
		m := (lo + hi) / 2
		if at(a[m]) <= t {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}

func uptoEv(a []Ev, t float64) int { return upto(a, t, evT) }

// At is where p is at t: the last sample at or before t, its position interpolated towards the next one (held over a
// jump: a gap of more than 5 s, another map, or faster than 40 yd/s, a teleport). False before p's first sample.
func (p *Player) At(t float64) (Sample, bool) {
	i := upto(p.Track, t, func(x Sample) float64 { return x.T }) - 1
	if i < 0 {
		return Sample{}, false
	}
	a := p.Track[i]
	if i+1 < len(p.Track) {
		if b := p.Track[i+1]; b.T-a.T <= 5000 && b.T > a.T && b.M == a.M && math.Hypot(b.X-a.X, b.Y-a.Y) <= math.Max(30, (b.T-a.T)*0.04) {
			f := (t - a.T) / (b.T - a.T)
			a.X += (b.X - a.X) * f
			a.Y += (b.Y - a.Y) * f
		}
	}
	return a, true
}

// last is the newest of a player's events at or before t.
func last(a []Ev, t float64) Ev {
	if i := uptoEv(a, t); i > 0 {
		return a[i-1]
	}
	return nil
}

func (p *Player) ScoreAt(t float64) Ev { return last(p.scores, t) }
func (p *Player) SayAt(t float64) Ev   { return last(p.says, t) }

// Target is what a player is targeting, and where it is.
type Target struct {
	Name     string   `json:"n"`
	Player   bool     `json:"pl,omitempty"`
	Lvl      int      `json:"lvl,omitempty"`
	X, Y     *float64 `json:"-"`
	M        int      `json:"-"`
	HP       *int     `json:"-"`
	Dead     bool     `json:"-"`
	creature int
}

// TargetOf is p's target at t, and where it is: the server log names the target (a creature entry, or a player), and
// p's own "units" samples place it: the nearest creature of that entry around them, with its health %. The server's
// target is who they're meleeing, so for casters it's usually empty: then it's what they last hit, within 3 s.
func (s *Store) TargetOf(p *Player, t float64) *Target {
	var tg map[string]any
	if e := last(p.tgts, t); e != nil {
		tg, _ = e["tgt"].(map[string]any)
	}
	if tg == nil {
		if h := last(p.hits, t); h != nil && t-N(h, "t") <= 3000 {
			tg, _ = h["dst"].(map[string]any)
		}
	}
	if tg == nil {
		return nil
	}
	pp, ok := p.At(t)
	if !ok {
		return nil
	}
	out := &Target{Name: S(tg, "n"), Player: Truthy(tg["pl"]), Lvl: int(N(tg, "lvl")), creature: int(N(tg, "id"))}
	if out.Player {
		if q := s.byName[out.Name]; q != nil {
			if qp, ok := q.At(t); ok {
				hp := int(math.Round(100 * float64(qp.HP) / math.Max(1, float64(qp.HPMax))))
				out.X, out.Y, out.M, out.HP = &qp.X, &qp.Y, qp.M, &hp
			}
		}
		return out
	}
	i := upto(p.Units, t, func(x UnitsSample) float64 { return x.T }) - 1
	if i < 0 || t-p.Units[i].T > 4000 {
		return out
	}
	smp := p.Units[i]
	out.M = smp.M
	bd := 80.0
	for _, u := range smp.U {
		if int(u.Entry) != out.creature {
			continue
		}
		if d := math.Hypot(float64(u.X)-pp.X, float64(u.Y)-pp.Y); d < bd {
			bd = d
			x, y, hp := float64(u.X), float64(u.Y), int(u.HP)
			out.X, out.Y, out.HP, out.Dead = &x, &y, &hp, u.State&4 != 0
		}
	}
	if out.Name == "" {
		out.Name = s.names[out.creature]
	}
	return out
}

// LeaderAt is group g's leader at t, from the latest group event naming one.
func (s *Store) LeaderAt(g int, t float64) string {
	for i := uptoEv(s.groups, t) - 1; i >= 0; i-- {
		if e := s.groups[i]; int(N(e, "grp")) == g && S(e, "leader") != "" {
			return S(e, "leader")
		}
	}
	return ""
}

// Group is a party at a moment: its members (with their samples) and leader.
type Group struct {
	ID      int
	Leader  string
	Members []*Player
}

// GroupsAt is the groups at t, from each player's latest sample (group id), largest first, and the players in none.
func (s *Store) GroupsAt(t float64) (groups []*Group, solo []*Player) {
	byID := map[int]*Group{}
	for _, p := range s.players {
		pp, ok := p.At(t)
		if !ok {
			continue
		}
		if pp.Grp == 0 {
			solo = append(solo, p)
			continue
		}
		g := byID[pp.Grp]
		if g == nil {
			g = &Group{ID: pp.Grp, Leader: s.LeaderAt(pp.Grp, t)}
			byID[pp.Grp] = g
			groups = append(groups, g)
		}
		g.Members = append(g.Members, p)
	}
	slices.SortStableFunc(groups, func(a, b *Group) int { return len(b.Members) - len(a.Members) })
	return groups, solo
}

// ---------- reading the logs ----------

// Source is the logs a store reads: files to tail, which are transcripts (path -> character), the server log, the
// characters to keep and the earliest time.
type Source struct {
	Files       []string
	Transcripts map[string]string
	ServerLog   string
	RunChars    map[string]bool
	Chars       []string
	Since       float64
	Dir         string    // a bench run dir: scanned for agents that come up after the viewer started
	scanned     time.Time // the last scan of Dir
	goRead      bool      // Start is the run's go file (not the first event)
	offsets     map[string]int64
	partial     map[string]string
	lastTgt     map[string]string
	keep        func(Ev) bool
}

// Poll reads what the files gained since the last call and files it; new events also go to live subscribers.
func (s *Store) Poll(src *Source) {
	if src.offsets == nil {
		src.offsets, src.partial, src.lastTgt = map[string]int64{}, map[string]string{}, map[string]string{}
		_, err := os.Stat(src.ServerLog)
		src.keep = ClientChatFilter(err == nil && src.ServerLog != "")
	}
	if src.Dir != "" && time.Since(src.scanned) > 2*time.Second {
		s.scan(src)
	}
	var fresh, live []Ev
	for _, f := range src.Files {
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		size, from := st.Size(), src.offsets[f]
		if size < from { // truncated or rotated: start over
			from = 0
			src.offsets[f] = 0
		}
		fd, err := os.Open(f)
		if err != nil {
			continue
		}
		// in chunks: a long run's files are hundreds of megabytes, read whole they made a 2 GB heap
		for ; from < size; from = src.offsets[f] {
			buf := make([]byte, min(size-from, 16<<20))
			n, _ := fd.ReadAt(buf, from)
			if n == 0 {
				break
			}
			src.offsets[f] = from + int64(n)
			text := src.partial[f] + string(buf[:n])
			cut := strings.LastIndex(text, "\n") + 1
			src.partial[f] = text[cut:]
			// samples go in as each chunk is read (they're four in five events, and in order per player): kept as
			// maps until the end, a long run's backlog peaked at 800 MB
			var samples []Ev
			for _, e := range src.parse(f, text[:cut]) {
				if k := S(e, "ev"); k == "pos" || k == "units" {
					samples = append(samples, e)
				} else {
					fresh = append(fresh, e)
				}
			}
			live = append(live, s.addSamples(samples)...)
		}
		fd.Close()
	}
	if len(fresh) == 0 && len(live) == 0 { // nothing new: no lock, no sort (this runs 20 times a second)
		return
	}
	SortByTime(fresh)
	NameUnits(fresh)
	s.resolve(fresh)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range fresh {
		if s.add(e) {
			live = append(live, e)
		}
	}
	if s.Start == 0 {
		for _, p := range s.players {
			if len(p.Track) > 0 && (s.Start == 0 || p.Track[0].T < s.Start) {
				s.Start = p.Track[0].T
			}
		}
		if len(s.events) > 0 && (s.Start == 0 || N(s.events[0], "t") < s.Start) {
			s.Start = N(s.events[0], "t")
		}
	}
	s.broadcast(live)
}

// addSamples files position and units samples; it returns them when someone is subscribed (for the live stream).
func (s *Store) addSamples(evs []Ev) []Ev {
	if len(evs) == 0 {
		return nil
	}
	s.resolve(evs) // creature names for the units
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range evs {
		s.add(e)
	}
	if len(s.subs) == 0 {
		return nil
	}
	return evs
}

func (src *Source) parse(f, lines string) []Ev {
	var got []Ev
	if name, ok := src.Transcripts[f]; ok {
		got = TranscriptEvents(lines, name)
	} else if f == src.ServerLog && len(src.RunChars) > 0 {
		got = ServerEvents(lines, src.RunChars, src.lastTgt)
	} else {
		for _, l := range strings.Split(lines, "\n") {
			// the SDK's action and diagnostic records (no "ev"): two thirds of a record file, nothing the viewer shows
			if l == "" || !strings.Contains(l, `"ev":`) {
				continue
			}
			var e Ev
			if json.Unmarshal([]byte(l), &e) == nil && e != nil && (len(src.RunChars) == 0 || src.keep(e)) {
				got = append(got, e)
			}
		}
	}
	out := got[:0]
	for _, e := range got {
		if S(e, "ev") != "" && N(e, "t") >= src.Since && (len(src.Chars) == 0 || slices.Contains(src.Chars, S(e, "n"))) {
			out = append(out, e)
		}
	}
	return out
}

// resolve looks up item names for loot and creature names for units, once each.
func (s *Store) resolve(evs []Ev) {
	s.mu.RLock()
	var loot, units []int
	for _, e := range evs {
		switch S(e, "ev") {
		case "loot":
			if id := int(N(e, "item")); !slices.Contains(loot, id) {
				if _, ok := s.items[id]; !ok {
					loot = append(loot, id)
				}
			}
		case "units":
			rows, _ := e["u"].([]any)
			for _, r := range rows {
				if r, ok := r.([]any); ok && len(r) > 0 {
					v, _ := r[0].(float64)
					id := int(v)
					if _, ok := s.names[id]; !ok && !slices.Contains(units, id) {
						units = append(units, id)
					}
				}
			}
		}
	}
	s.mu.RUnlock()
	if len(loot) == 0 && len(units) == 0 {
		return
	}
	items := world.ItemNames(loot)
	var names map[int]string
	var radii, detect map[int]float64
	if len(units) > 0 {
		names, radii, detect = world.CreatureNames(units), world.CreatureRadii(units), world.CreatureDetection(units)
	}
	s.mu.Lock()
	for id, it := range items {
		i, _ := strconv.Atoi(id)
		s.items[i] = it
	}
	for _, id := range units {
		s.names[id] = names[id] // "" for an unknown entry: asked once
		s.radii[id] = radii[id]
		s.detect[id] = detect[id]
	}
	s.mu.Unlock()
}

// ---------- live subscribers ----------

// Subscribe returns a channel of new data as server-sent events; unsubscribe closes it.
func (s *Store) Subscribe() (chan liveMsg, func()) {
	c := make(chan liveMsg, 4096)
	s.mu.Lock()
	s.subs[c] = true
	s.mu.Unlock()
	return c, func() {
		s.mu.Lock()
		if s.subs[c] {
			delete(s.subs, c)
			close(c)
		}
		s.mu.Unlock()
	}
}

// broadcast sends new events to subscribers (the caller holds the lock): samples for the map, marks and ticks for the
// seek bar, and "data" with the newest time when anything else arrived (the panels refresh when the playhead gets there).
func (s *Store) broadcast(evs []Ev) {
	if len(s.subs) == 0 || len(evs) == 0 {
		return
	}
	var msgs []liveMsg
	other := false
	newest := 0.0 // the newest of the other events: when the playhead reaches it, the panels have something new
	for _, e := range evs {
		p := s.byName[S(e, "n")]
		switch kind := S(e, "ev"); {
		case kind == "pos" && p != nil:
			msgs = append(msgs, s.liveJSON("s", map[string]any{"n": p.Name, "cls": p.Cls, "s": s.mapSample(p, sampleOf(e))}))
		case kind == "units" && p != nil:
			msgs = append(msgs, s.liveJSON("u", map[string]any{"n": p.Name, "u": s.mapUnits(unitsOf(e))}))
		default:
			if (kind == "dmg" || kind == "heal") && N(e, "amount") > 0 {
				for _, h := range mapHits(e) {
					msgs = append(msgs, s.liveJSON("hit", h))
				}
			}
			if markKinds[kind] {
				msgs = append(msgs, s.liveJSON("mark", s.mapMark(e)))
			}
			if markKinds[kind] || kind == "chat" {
				msgs = append(msgs, s.liveJSON("tick", []any{N(e, "t"), kind}))
			}
			other, newest = true, math.Max(newest, N(e, "t"))
		}
	}
	if other {
		msgs = append(msgs, s.liveJSON("data", map[string]any{"t": newest}))
	}
	for c := range s.subs {
		for _, m := range msgs {
			select {
			case c <- m:
			default: // a client that can't keep up is dropped
				delete(s.subs, c)
				close(c)
			}
			if !s.subs[c] {
				break
			}
		}
	}
}

func (s *Store) liveJSON(typ string, v any) liveMsg {
	b, _ := json.Marshal(v)
	return liveMsg{typ, b}
}

// ---------- what the map draws ----------

// mapSample is a sample as the map draws it: where, state, group and leader, and the target with where it is.
func (s *Store) mapSample(p *Player, a Sample) map[string]any {
	out := map[string]any{"t": a.T, "x": a.X, "y": a.Y, "m": a.M, "a": a.Alive, "g": a.Ghost, "c": a.Combat, "l": a.Lvl}
	if a.Grp != 0 {
		out["grp"], out["ld"] = a.Grp, s.LeaderAt(a.Grp, a.T)
	}
	if a.Alive {
		if tg := s.TargetOf(p, a.T); tg != nil && tg.X != nil {
			hp := -1
			if tg.HP != nil && !tg.Dead {
				hp = *tg.HP
			}
			out["tg"] = []any{*tg.X, *tg.Y, tg.M, hp}
		}
	}
	return out
}

// mapUnits is a units sample as the map draws it: [name, level, x, y, health %, state, radius in yd, detection range
// in yd] per creature.
func (s *Store) mapUnits(u UnitsSample) map[string]any {
	rows := make([][]any, len(u.U))
	for i, r := range u.U {
		name := s.names[int(r.Entry)]
		if name == "" {
			name = strconv.Itoa(int(r.Entry))
		}
		rows[i] = []any{name, r.Lvl, r.X, r.Y, r.HP, r.State, math.Round(s.radii[int(r.Entry)]*100) / 100, s.detect[int(r.Entry)]}
	}
	return map[string]any{"t": u.T, "m": u.M, "u": rows}
}

// mapHits is a damage or heal event as the map's combat text: per player it concerns, the amount and how (d: dealt, by
// the player, shown over their target; t: taken; h: healed), and that player.
func mapHits(e Ev) []map[string]any {
	unit := func(k string) (string, bool) {
		u, _ := e[k].(map[string]any)
		return S(u, "n"), u != nil && Truthy(u["pl"])
	}
	src, srcPl := unit("src")
	dst, dstPl := unit("dst")
	t, a := N(e, "t"), N(e, "amount")
	var out []map[string]any
	if S(e, "ev") == "heal" {
		if dstPl {
			out = append(out, map[string]any{"t": t, "a": a, "k": "h", "n": dst})
		}
		return out
	}
	if dstPl {
		out = append(out, map[string]any{"t": t, "a": a, "k": "t", "n": dst})
	}
	if srcPl {
		out = append(out, map[string]any{"t": t, "a": a, "k": "d", "n": src})
	}
	return out
}

// mapMark is a marked event with where it happened (its player's position then) and its line for the tooltip.
func (s *Store) mapMark(e Ev) map[string]any {
	out := map[string]any{"t": N(e, "t"), "ev": S(e, "ev"), "n": S(e, "n"), "html": string(s.Line(e, ""))}
	if p := s.byName[S(e, "n")]; p != nil {
		if pp, ok := p.At(N(e, "t")); ok {
			out["x"], out["y"], out["m"] = pp.X, pp.Y, pp.M
		}
	}
	return out
}

// scan adds what a run dir has that src doesn't yet: the record files and transcripts of agents whose sessions came up
// since the last scan (a viewer started early in a run), their meta, the server log once there are characters, and
// the go file. A record file's events from before Since are still dropped, so a late file reads from the start.
func (s *Store) scan(src *Source) {
	src.scanned = time.Now()
	records, transcripts := RunSources(src.Dir)
	have := map[string]bool{}
	for _, f := range src.Files {
		have[f] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range records {
		if !have[f] {
			src.Files = append(src.Files, f)
		}
	}
	for _, t := range transcripts {
		if have[t.Path] {
			continue
		}
		src.Transcripts[t.Path] = t.Name
		src.RunChars[t.Name] = true
		src.Files = append(src.Files, t.Path)
		s.player(t.Name)
	}
	if _, err := os.Stat(src.ServerLog); err == nil && len(src.RunChars) > 0 && !have[src.ServerLog] {
		src.Files = append(src.Files, src.ServerLog)
	}
	s.agents = AgentMeta(transcripts)
	if !src.goRead {
		if b, err := os.ReadFile(filepath.Join(src.Dir, "go")); err == nil {
			if f, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); err == nil {
				s.Start, src.goRead = f*1000, true
				if src.offsets == nil { // nothing read yet: start reading at the run
					src.Since = s.Start - 60_000
				}
			}
		}
	}
}

// OpenRun is a store for a bench run dir (its record files, transcripts and, for chat, damage, heals and targets, the
// server log), or for log files when dir is "" (default: the server log). It reads from the run's start (its go
// file), else from sinceMin minutes ago. Call Poll to read.
func OpenRun(dir string, files []string, sinceMin float64) (*Store, *Source) {
	s, src := NewStore(), &Source{Transcripts: map[string]string{}, RunChars: map[string]bool{}, ServerLog: ServerLog()}
	src.Since = float64(time.Now().UnixMilli()) - sinceMin*60_000
	src.Files, src.Dir = files, dir
	if dir != "" {
		s.scan(src)
	} else if len(src.Files) == 0 {
		src.Files = []string{src.ServerLog}
	}
	for _, r := range world.MustRows(world.Bench(), "SELECT id, name FROM area") {
		id, _ := strconv.Atoi(r[0])
		s.zones[id] = r[1]
	}
	return s, src
}
