package main

// How events read: one line of HTML per event for the feed, the tooltips and the roster, in WoW's colours (class
// colours for names, chat-type colours, item qualities).

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"math"
	"slices"
	"strings"
)

// feedCats are the feed's filters: every feed event is in one. Combat and evals are off by default; they outnumber
// everything else a hundred to one.
var feedCats = []struct {
	Key, Label, Color string
	Kinds             []string
	On                bool
}{
	{"events", "Events", "#ffd100", []string{"death", "kill", "achievement", "level_up", "quest_turned_in", "item_received", "area_changed", "resurrected", "login", "logout", "group", "money"}, true},
	{"chat", "Chat", "#aaaaff", []string{"chat"}, true},
	{"agent", "Agent", "#d97757", []string{"say"}, true},
	{"combat", "Combat", "#ff8040", []string{"damage", "heal", "cast_done", "xp"}, false},
	{"api", "Evals", "#9b9a95", []string{"api"}, false},
	{"script_error", "Errors", "#ff4040", []string{"script_error"}, true},
}

// feedCat is the category of an event kind ("" for none).
func feedCat(kind string) (key, color string) {
	for _, c := range feedCats {
		for _, k := range c.Kinds {
			if k == kind {
				return c.Key, c.Color
			}
		}
	}
	return "", ""
}

var (
	// by the SDK's chat kinds
	chatColor = map[string]string{
		"say": "#ffffff", "party": "#aaaaff", "raid": "#ff7f00", "guild": "#40ff40", "officer": "#40c040",
		"yell": "#ff4040", "whisper": "#ff80ff", "whisper_sent": "#ff80ff", "emote": "#ff8040", "npc_say": "#ffffff", "npc_party": "#aaaaff",
		"npc_yell": "#ff4040", "npc_whisper": "#ff80ff", "npc_emote": "#ff8040", "channel": "#ffc0c0", "raid_warning": "#ff4800", "battleground": "#ff7f00",
	}
	qualities = []string{"poor", "common", "uncommon", "rare", "epic", "legendary", "artifact", "heirloom"} // the SDK's
	quality   = []string{"#9d9d9d", "#ffffff", "#1eff00", "#0070dd", "#a335ee", "#ff8000", "#e6cc80", "#e6cc80"}
	chatTag   = map[string]string{"party": "Party", "raid": "Raid", "guild": "Guild", "officer": "Officer", "raid_warning": "Raid Warning", "battleground": "Battleground"}
	npcVerb   = map[string]string{"npc_say": "says", "npc_party": "says", "npc_yell": "yells", "npc_whisper": "whispers"}
)

var esc = html.EscapeString

// ClassColor is a class's colour by name.
func ClassColor(cls string) string {
	for i, c := range Class {
		if c == cls && c != "" {
			return Color[i]
		}
	}
	return "#888888"
}

// Who is a character's name in its class colour.
func (s *Store) Who(n string) string {
	c := "inherit"
	if p := s.byName[n]; p != nil {
		c = ClassColor(p.Cls)
	}
	return fmt.Sprintf(`<span class="who" style="color:%s">%s</span>`, c, esc(n))
}

// Money is copper as the game writes it: 1g 2s 3c.
func Money(c int) string {
	var parts []string
	for _, x := range []struct {
		v int
		u string
	}{{c / 10000, "g"}, {c / 100 % 100, "s"}, {c % 100, "c"}} {
		if x.v != 0 {
			parts = append(parts, fmt.Sprint(x.v, x.u))
		}
	}
	if len(parts) == 0 {
		return "0c"
	}
	return strings.Join(parts, " ")
}

func (s *Store) unitName(v any) string {
	u, ok := v.(map[string]any)
	if !ok {
		return "?"
	}
	if IsPlayer(u) {
		return s.Who(S(u, "name"))
	}
	out := esc(S(u, "name"))
	if out == "" {
		out = s.names[int(N(u, "entry"))]
	}
	if l := N(u, "level"); l > 0 {
		out += fmt.Sprintf(` <span class="muted">(%g)</span>`, l)
	}
	return out
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Line is an event as one line of HTML. sel is the selected player: whispers read from their side.
func (s *Store) Line(e Ev, sel string) template.HTML {
	w := s.Who(S(e, "char"))
	num := func(k string) string { return fmt.Sprint(N(e, k)) }
	var out string
	switch S(e, "type") {
	case "chat":
		out = s.chatLine(e, sel)
	case "death":
		if e["killer"] == nil && S(e, "cause") != "" {
			out = w + " died: " + esc(S(e, "cause"))
			break
		}
		out = w + " was killed by " + s.unitName(e["killer"])
	case "kill":
		out = w + " killed " + s.unitName(e["victim"])
	case "achievement":
		out = fmt.Sprintf("%s earned <b>%s</b>", w, esc(or(S(e, "title"), "achievement "+num("id"))))
		if N(e, "points") > 0 {
			out += fmt.Sprintf(" (%s pts)", num("points"))
		}
	case "level_up":
		out = fmt.Sprintf("%s reached level <b>%s</b>", w, num("level"))
	case "quest_turned_in":
		out = fmt.Sprintf("%s completed <b>%s</b>", w, esc(or(S(e, "title"), "quest "+num("quest"))))
		if N(e, "xp") > 0 {
			extra := ""
			if N(e, "money") > 0 {
				extra = ", " + Money(int(N(e, "money")))
			}
			out += fmt.Sprintf(` <span class="muted">+%s xp%s</span>`, num("xp"), extra)
		}
	case "item_received":
		item := Unit(e, "item")
		it, ok := s.items[int(N(item, "entry"))]
		if !ok {
			it.Name, it.Q = or(S(item, "name"), fmt.Sprint("item ", N(item, "entry"))), max(0, slices.Index(qualities, S(item, "quality")))
		}
		c := "inherit"
		if it.Q >= 0 && it.Q < len(quality) {
			c = quality[it.Q]
		}
		out = fmt.Sprintf(`%s looted <span style="color:%s">[%s]</span>`, w, c, esc(it.Name))
		if c := N(item, "count"); c > 1 {
			out += fmt.Sprint(" ×", c)
		}
	case "area_changed":
		out = w + " entered " + esc(or(S(e, "area"), or(s.zones[int(N(e, "area_id"))], or(S(e, "zone"), "zone "+num("zone_id")))))
	case "resurrected":
		out = w + " resurrected"
	case "login":
		out = w + " logged in"
	case "logout":
		out = w + " logged out"
	case "damage":
		out = fmt.Sprintf("%s hit %s for <b>%s</b>", s.unitName(e["src"]), s.unitName(e["dst"]), num("amount"))
	case "heal":
		out = fmt.Sprintf("%s healed %s for <b>%s</b>", s.unitName(e["src"]), s.unitName(e["dst"]), num("amount"))
	case "cast_done":
		out = w + " cast " + esc(or(S(e, "name"), "spell "+num("spell")))
	case "xp":
		out = fmt.Sprintf("%s +%s xp", w, num("amount"))
	case "api":
		var p map[string]any
		_ = json.Unmarshal([]byte(or(S(e, "params"), "{}")), &p) // bad params show as null
		code, _ := p["code"].(string)
		if code == "" {
			delete(p, "char")
			b, _ := json.Marshal(p)
			code = string(b)
		}
		out = fmt.Sprintf(`%s <span class="muted">%s</span> <code>%s</code>`, w, esc(S(e, "method")), esc(cut(code, 160)))
	case "say":
		out = fmt.Sprintf(`%s <span style="color:var(--accent)">“%s”</span>`, w, esc(cut(S(e, "text"), 600)))
	case "script_error":
		out = fmt.Sprintf(`%s <span style="color:var(--death)">script error</span> %s: %s`, w, esc(S(e, "where")), esc(S(e, "error")))
	case "group":
		if N(e, "group") == 0 {
			out = w + " left their group"
			break
		}
		members, _ := e["members"].([]any)
		ms := make([]string, 0, len(members))
		for _, m := range members {
			ms = append(ms, s.Who(fmt.Sprint(m)))
		}
		out = fmt.Sprintf("%s is in a group: %s", w, strings.Join(ms, ", "))
		if l := S(e, "leader"); l != "" {
			out += fmt.Sprintf(` <span class="muted">(leader %s)</span>`, esc(l))
		}
	case "money":
		sign := ""
		if N(e, "amount") > 0 {
			sign = "+"
		}
		out = fmt.Sprintf("%s %s%s", w, sign, Money(int(N(e, "amount"))))
	default:
		out = w + " " + esc(S(e, "type"))
	}
	return template.HTML(out)
}

// cut shortens s to n runes, with an ellipsis.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// chatLine is chat as the game prints it, in its kind's colour with class-coloured [Names] (ChatSides: the speaker,
// and a whisper's other side). Whispers read from the selected player's side: "To [B]:" when they sent it, "[A]
// whispers:" when they got it.
func (s *Store) chatLine(e Ev, sel string) string {
	kind := S(e, "kind")
	snd, to, npc := ChatSides(e)
	if kind == "channel" {
		to = S(e, "channel")
	}
	name := func(n string) string { return "[" + s.Who(n) + "]" }
	say := func(pre string) string {
		return fmt.Sprintf(`<span style="color:%s">%s%s</span>`, or(chatColor[kind], "inherit"), pre, esc(S(e, "text")))
	}
	if npc {
		if v, ok := npcVerb[kind]; ok {
			return say(esc(snd) + " " + v + ": ")
		}
		return say("") // NPC emotes carry the name
	}
	if tag, ok := chatTag[kind]; ok {
		return say(fmt.Sprintf("[%s] %s: ", tag, name(snd)))
	}
	switch kind {
	case "say":
		return say(name(snd) + " says: ")
	case "yell":
		return say(name(snd) + " yells: ")
	case "whisper", "whisper_sent":
		switch sel {
		case snd:
			return say("To " + name(to) + ": ")
		case to:
			return say(name(snd) + " whispers: ")
		}
		return say(name(snd) + " whispers to " + name(to) + ": ")
	case "emote":
		return say(s.Who(snd) + " ")
	case "channel":
		return say(fmt.Sprintf("[%s] %s: ", esc(to), name(snd)))
	}
	return say(name(snd) + ": ")
}

// Involves: does an event concern player n (theirs, or they're the attacker, target, killer or victim).
func Involves(e Ev, n string) bool {
	if S(e, "char") == n {
		return true
	}
	for _, k := range []string{"src", "dst", "victim", "killer"} {
		if u := Unit(e, k); IsPlayer(u) && S(u, "name") == n {
			return true
		}
	}
	return false
}

// Hears: did player n see this chat message: everyone whose game log has it (heard). With only the speaker's copy:
// their own, whispers to them, their party's at the time, say and emote within 25 yd, yells within 300, channels
// (everyone in the run is assumed to be in them).
func (s *Store) Hears(e Ev, n string) bool {
	if h, ok := e["heard"].([]string); ok {
		for _, x := range h {
			if x == n {
				return true
			}
		}
		return false
	}
	snd, to, _ := ChatSides(e)
	if snd == n {
		return true
	}
	t := N(e, "t")
	switch S(e, "kind") {
	case "whisper", "whisper_sent":
		return to == n
	case "party", "raid", "raid_warning", "battleground":
		g := s.grpOf(snd, t)
		return g != 0 && g == s.grpOf(n, t)
	case "say", "emote":
		return s.yards(snd, n, t) <= 25
	case "yell":
		return s.yards(snd, n, t) <= 300
	}
	return true
}

func (s *Store) grpOf(n string, t float64) int {
	if p := s.byName[n]; p != nil {
		if pp, ok := p.At(t); ok {
			return pp.Grp
		}
	}
	return 0
}

func (s *Store) yards(a, b string, t float64) float64 {
	pa, pb := s.byName[a], s.byName[b]
	if pa == nil || pb == nil {
		return math.Inf(1)
	}
	sa, ok1 := pa.At(t)
	sb, ok2 := pb.At(t)
	if !ok1 || !ok2 || sa.M != sb.M {
		return math.Inf(1)
	}
	return math.Hypot(sa.X-sb.X, sa.Y-sb.Y)
}

// Fmt is a time since the start as h:mm:ss (m:ss under an hour).
func Fmt(ms float64) string {
	sec := int(math.Max(0, math.Floor(ms/1000)))
	h, m, ss := sec/3600, sec/60%60, sec%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, ss)
	}
	return fmt.Sprintf("%d:%02d", m, ss)
}
