package main

// How events read: one line of HTML per event for the feed, the tooltips and the roster, in WoW's colours (class
// colours for names, chat-type colours, item qualities).

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"math"
	"strings"
)

// feedCats are the feed's filters: every feed event is in one. Combat and evals are off by default; they outnumber
// everything else a hundred to one.
var feedCats = []struct {
	Key, Label, Color string
	Kinds             []string
	On                bool
}{
	{"events", "Events", "#ffd100", []string{"death", "kill", "pvp", "achievement", "level", "quest", "loot", "zone", "release", "resurrect", "login", "logout", "spell", "group", "money"}, true},
	{"chat", "Chat", "#aaaaff", []string{"chat"}, true},
	{"agent", "Agent", "#d97757", []string{"say"}, true},
	{"combat", "Combat", "#ff8040", []string{"dmg", "heal", "cast", "xp"}, false},
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
	chatColor = map[int]string{0: "#ffff00", 1: "#ffffff", 2: "#aaaaff", 3: "#ff7f00", 4: "#40ff40", 5: "#40c040", 6: "#ff4040", 7: "#ff80ff", 9: "#ff80ff", 10: "#ff8040", 11: "#ff8040",
		12: "#ffffff", 13: "#aaaaff", 14: "#ff4040", 15: "#ff80ff", 16: "#ff8040", 17: "#ffc0c0", 39: "#ff4809", 40: "#ff4800", 41: "#ffdd00", 42: "#ff80ff", 51: "#76c8ff"}
	quality = []string{"#9d9d9d", "#ffffff", "#1eff00", "#0070dd", "#a335ee", "#ff8000", "#e6cc80", "#e6cc80"}
	chatTag = map[int]string{2: "Party", 3: "Raid", 4: "Guild", 5: "Officer", 39: "Raid Leader", 40: "Raid Warning", 51: "Party Leader"}
	npcVerb = map[int]string{12: "says", 13: "says", 14: "yells", 15: "whispers", 42: "whispers"}
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
	if Truthy(u["pl"]) {
		return s.Who(S(u, "n"))
	}
	out := esc(S(u, "n"))
	if out == "" {
		out = s.names[int(N(u, "id"))]
	}
	if l := N(u, "lvl"); l > 0 {
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
	w := s.Who(S(e, "n"))
	num := func(k string) string { return fmt.Sprint(N(e, k)) }
	var out string
	switch S(e, "ev") {
	case "chat":
		out = s.chatLine(e, sel)
	case "death":
		out = w + " was killed by " + s.unitName(e["killer"])
	case "kill":
		out = w + " killed " + s.unitName(e["victim"])
	case "pvp":
		out = w + " killed player " + s.unitName(e["victim"])
	case "achievement":
		out = fmt.Sprintf("%s earned <b>%s</b>", w, esc(or(S(e, "title"), "achievement "+num("id"))))
		if N(e, "points") > 0 {
			out += fmt.Sprintf(" (%s pts)", num("points"))
		}
	case "level":
		out = fmt.Sprintf("%s reached level <b>%s</b>", w, num("to"))
	case "quest":
		out = fmt.Sprintf("%s completed <b>%s</b>", w, esc(or(S(e, "title"), "quest "+num("id"))))
		if N(e, "xp") > 0 {
			extra := ""
			if N(e, "money") > 0 {
				extra = ", " + Money(int(N(e, "money")))
			}
			out += fmt.Sprintf(` <span class="muted">+%s xp%s</span>`, num("xp"), extra)
		}
	case "loot":
		it, ok := s.items[int(N(e, "item"))]
		if !ok {
			it.Name, it.Q = "item "+num("item"), 1
		}
		c := "inherit"
		if it.Q >= 0 && it.Q < len(quality) {
			c = quality[it.Q]
		}
		out = fmt.Sprintf(`%s looted <span style="color:%s">[%s]</span>`, w, c, esc(it.Name))
		if N(e, "count") > 1 {
			out += " ×" + num("count")
		}
	case "zone":
		out = w + " entered " + esc(or(s.zones[int(N(e, "area"))], or(s.zones[int(N(e, "zone"))], "zone "+num("zone"))))
	case "release":
		out = w + " released spirit"
	case "resurrect":
		out = w + " resurrected"
		if Truthy(e["sickness"]) {
			out += " (sickness)"
		}
	case "login":
		out = w + " logged in"
	case "logout":
		out = w + " logged out"
	case "spell":
		out = w + " learned spell " + num("id")
	case "dmg":
		out = fmt.Sprintf("%s hit %s for <b>%s</b>", s.unitName(e["src"]), s.unitName(e["dst"]), num("amount"))
	case "heal":
		out = fmt.Sprintf("%s healed %s for <b>%s</b>", s.unitName(e["src"]), s.unitName(e["dst"]), num("amount"))
	case "cast":
		out = w + " cast " + esc(S(e, "name"))
		if e["target"] != nil {
			out += " on " + s.unitName(e["target"])
		}
	case "xp":
		out = fmt.Sprintf("%s +%s xp", w, num("amount"))
	case "api":
		var p map[string]any
		json.Unmarshal([]byte(or(S(e, "params"), "{}")), &p)
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
		out = fmt.Sprintf(`%s <span style="color:var(--death)">script error ×%s</span> %s`, w, num("count"), esc(S(e, "last")))
	case "group":
		if N(e, "grp") == 0 {
			out = w + " left their group"
			break
		}
		members, _ := e["members"].([]any)
		var ms []string
		for _, m := range members {
			ms = append(ms, s.Who(fmt.Sprint(m)))
		}
		out = fmt.Sprintf("%s is in a group: %s", w, strings.Join(ms, ", "))
		if l := S(e, "leader"); l != "" {
			out += fmt.Sprintf(` <span class="muted">(leader %s)</span>`, esc(l))
		}
	case "money":
		sign := ""
		if N(e, "delta") > 0 {
			sign = "+"
		}
		out = fmt.Sprintf("%s %s%sc", w, sign, num("delta"))
	default:
		out = w + " " + esc(S(e, "ev"))
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

// chatLine is chat as the game prints it, in the chat type's colour with class-coloured [Names]. Server records name
// the sender (n) and the recipient (to: a name, "party", a channel); NPC speech comes from the agents' clients,
// speaker in from. Whispers read from the selected player's side: "To [B]:" when they sent it, "[A] whispers:" when
// they got it.
func (s *Store) chatLine(e Ev, sel string) string {
	typ, from, snd, to := int(N(e, "type")), S(e, "from"), S(e, "n"), S(e, "to")
	name := func(n string) string { return "[" + s.Who(n) + "]" }
	say := func(pre string) string {
		return fmt.Sprintf(`<span style="color:%s">%s%s</span>`, or(chatColor[typ], "inherit"), pre, esc(S(e, "msg")))
	}
	if from != "" {
		if v, ok := npcVerb[typ]; ok {
			return say(esc(from) + " " + v + ": ")
		}
		return say("") // NPC emotes carry the name
	}
	if tag, ok := chatTag[typ]; ok {
		return say(fmt.Sprintf("[%s] %s: ", tag, name(snd)))
	}
	switch typ {
	case 1:
		return say(name(snd) + " says: ")
	case 6:
		return say(name(snd) + " yells: ")
	case 7, 9:
		switch sel {
		case snd:
			return say("To " + name(to) + ": ")
		case to:
			return say(name(snd) + " whispers: ")
		}
		return say(name(snd) + " whispers to " + name(to) + ": ")
	case 10:
		return say(s.Who(snd) + " ")
	case 17:
		return say(fmt.Sprintf("[%s] %s: ", esc(to), name(snd)))
	}
	return say(name(snd) + ": ")
}

// Involves: does an event concern player n (theirs, or they're the speaker, attacker, target, killer or victim).
func Involves(e Ev, n string) bool {
	if S(e, "n") == n || S(e, "from") == n {
		return true
	}
	for _, k := range []string{"src", "dst", "victim", "killer", "target"} {
		if u, ok := e[k].(map[string]any); ok && Truthy(u["pl"]) && S(u, "n") == n {
			return true
		}
	}
	return false
}

// Hears: did player n see this chat message. Their own, whispers to them, their party's at the time, say and emote
// within 25 yd, yells within 300, channels (everyone in the run is assumed to be in them); NPC speech if their client
// recorded it.
func (s *Store) Hears(e Ev, n string) bool {
	if h, ok := e["heard"].([]string); ok {
		for _, x := range h {
			if x == n {
				return true
			}
		}
		return false
	}
	if S(e, "n") == n {
		return true
	}
	t := N(e, "t")
	switch int(N(e, "type")) {
	case 7, 9:
		return S(e, "to") == n
	case 2, 3, 39, 40, 51:
		g := s.grpOf(S(e, "n"), t)
		return g != 0 && g == s.grpOf(n, t)
	case 1, 10, 11:
		return s.yards(S(e, "n"), n, t) <= 25
	case 6:
		return s.yards(S(e, "n"), n, t) <= 300
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
