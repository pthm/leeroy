// Package gamelog reads a character's game log, game/<char>/events.jsonl: the event stream its agent saw (seq > 0:
// damage, kills, chat, loot, quests...) and the records its session kept beside it (no seq: state each second,
// nearby creatures, transports, group changes, API calls). Reader turns each line into what happened, the way the
// viewer, publish and the report read a run: maps with t (unix ms), ev (the kind) and n (the character), units as
// {id (a player's guid counter, a creature's entry), n, pl (players), lvl}.
//
//	state      -> pos (and tgt, when the target changes)
//	units      -> units
//	transports -> transport, one per boat or zeppelin
//	login, group, api, script_error -> the same
//	damage, heal -> dmg, heal       kill -> kill        death -> death       level_up -> level
//	xp -> xp                        item_received -> loot   money -> money   quest_turned_in -> quest
//	achievement -> achievement      cast_done -> cast   area_changed -> zone resurrected -> resurrect
//	chat -> chat: type (the game's chat type number), msg, to; from (an NPC's name), by (a player who said it to
//	this character; the character's own lines have neither)
package gamelog

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Ev is one thing that happened.
type Ev = map[string]any

// Reader converts one character's game log, line by line (it remembers the character's target, to mark changes).
type Reader struct {
	Char    string
	lastTgt string
}

type line struct {
	Seq  uint64          `json:"seq"`
	T    float64         `json:"t"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// chat kinds (the SDK's names) as the game's chat type numbers
var chatTypes = map[string]int{"system": 0, "say": 1, "party": 2, "raid": 3, "guild": 4, "officer": 5, "yell": 6,
	"whisper": 7, "whisper_sent": 7, "emote": 10, "npc_say": 12, "npc_party": 13, "npc_yell": 14, "npc_whisper": 15,
	"npc_emote": 16, "channel": 17, "raid_warning": 40, "battleground": 44, "achievement": 48}

type unitRef struct {
	GUID   string `json:"guid"`
	Name   string `json:"name"`
	Entry  uint32 `json:"entry"`
	Level  uint32 `json:"level"`
	Player bool   `json:"player"`
}

// unit: a unit as {id, n, pl | lvl}
func (u *unitRef) unit() map[string]any {
	if u == nil {
		return nil
	}
	if u.Player {
		g, _ := strconv.ParseUint(u.GUID, 10, 64)
		return map[string]any{"id": float64(g & 0xFFFFFFFF), "n": u.Name, "pl": float64(1)}
	}
	return map[string]any{"id": float64(u.Entry), "n": u.Name, "lvl": float64(u.Level)}
}

// Line converts one line ("" or a line it doesn't keep: nil).
func (r *Reader) Line(b []byte) []Ev {
	var l line
	if json.Unmarshal(b, &l) != nil || l.Type == "" {
		return nil
	}
	ev := func(kind string, body map[string]any) Ev {
		if body == nil {
			body = map[string]any{}
		}
		body["t"], body["ev"], body["n"] = l.T, kind, r.Char
		return body
	}
	var m map[string]any
	var out []Ev
	switch l.Type {
	case "state":
		json.Unmarshal(l.Data, &m)
		tgt, _ := m["tgt"].(map[string]any)
		delete(m, "tgt")
		out = append(out, ev("pos", m))
		key := ""
		if tgt != nil {
			b, _ := json.Marshal(tgt)
			key = string(b)
		}
		if key != r.lastTgt { // the target changed (or went)
			r.lastTgt = key
			t := ev("tgt", nil)
			if tgt != nil {
				t["tgt"] = tgt
			}
			out = append(out, t)
		}
	case "units", "login", "group", "api", "script_error":
		json.Unmarshal(l.Data, &m)
		out = append(out, ev(l.Type, m))
	case "transports":
		var rows [][]any
		json.Unmarshal(l.Data, &rows)
		for _, x := range rows {
			if len(x) >= 7 {
				out = append(out, Ev{"t": l.T, "ev": "transport", "id": x[0], "e": x[1], "n": x[2], "m": x[3], "x": x[4], "y": x[5], "o": x[6]})
			}
		}
	case "damage", "heal":
		var d struct {
			Src, Dst *unitRef
			Amount   float64
			Spell    float64
			Outcome  string
			Overheal float64
		}
		if json.Unmarshal(l.Data, &d) != nil || d.Amount <= 0 {
			return nil
		}
		kind := map[string]string{"damage": "dmg", "heal": "heal"}[l.Type]
		e := ev(kind, map[string]any{"src": d.Src.unit(), "dst": d.Dst.unit(), "amount": d.Amount})
		if d.Spell > 0 {
			e["spell"] = d.Spell
		}
		out = append(out, e)
	case "kill":
		var k struct{ Victim *unitRef }
		json.Unmarshal(l.Data, &k)
		out = append(out, ev("kill", map[string]any{"victim": k.Victim.unit()}))
	case "death":
		var d struct{ Killer *unitRef }
		json.Unmarshal(l.Data, &d)
		e := ev("death", nil)
		if d.Killer != nil {
			e["killer"] = d.Killer.unit()
		}
		out = append(out, e)
	case "level_up":
		var d struct{ Level float64 }
		json.Unmarshal(l.Data, &d)
		out = append(out, ev("level", map[string]any{"to": d.Level}))
	case "xp":
		var d struct {
			Amount float64
			Source string
			Victim *unitRef
		}
		json.Unmarshal(l.Data, &d)
		e := ev("xp", map[string]any{"amount": d.Amount, "src": d.Source})
		if d.Victim != nil {
			e["victim"] = d.Victim.unit()
		}
		out = append(out, e)
	case "item_received":
		var d struct {
			Item struct{ Entry, Count float64 }
			From string
		}
		json.Unmarshal(l.Data, &d)
		out = append(out, ev("loot", map[string]any{"item": d.Item.Entry, "count": max(1, d.Item.Count), "from": d.From}))
	case "money":
		var d struct{ Amount float64 }
		json.Unmarshal(l.Data, &d)
		out = append(out, ev("money", map[string]any{"delta": d.Amount}))
	case "quest_turned_in":
		var d struct {
			Quest     float64
			Title     string
			XP, Money float64
		}
		json.Unmarshal(l.Data, &d)
		out = append(out, ev("quest", map[string]any{"id": d.Quest, "title": d.Title, "xp": d.XP, "money": d.Money}))
	case "achievement":
		var d struct{ ID float64 }
		json.Unmarshal(l.Data, &d)
		out = append(out, ev("achievement", map[string]any{"id": d.ID}))
	case "cast_done":
		var d struct {
			Spell float64
			Name  string
		}
		json.Unmarshal(l.Data, &d)
		out = append(out, ev("cast", map[string]any{"spell": d.Spell, "name": d.Name}))
	case "area_changed":
		var d struct {
			ZoneID float64 `json:"zone_id"`
			AreaID float64 `json:"area_id"`
		}
		json.Unmarshal(l.Data, &d)
		out = append(out, ev("zone", map[string]any{"zone": d.ZoneID, "area": d.AreaID}))
	case "resurrected":
		out = append(out, ev("resurrect", nil))
	case "chat":
		var c struct {
			Kind, From, Channel, Text string
			Self                      bool
		}
		if json.Unmarshal(l.Data, &c) != nil {
			return nil
		}
		typ, ok := chatTypes[c.Kind]
		if !ok {
			return nil // the game's system lines (loot, money, xp, skill...): their own events say it
		}
		e := ev("chat", map[string]any{"type": float64(typ), "msg": c.Text})
		switch {
		case strings.HasPrefix(c.Kind, "npc_"):
			e["from"] = c.From
		case c.Kind == "whisper_sent":
			e["to"] = c.From // the SDK names who it went to
		case c.Self || c.Kind == "system":
		default:
			e["by"] = c.From
			if c.Kind == "whisper" {
				e["to"] = r.Char
			}
		}
		if c.Channel != "" {
			e["to"] = c.Channel
		}
		out = append(out, e)
	}
	return out
}

// Lines converts lines of text (a file, or what it gained).
func (r *Reader) Lines(text string) []Ev {
	var out []Ev
	for _, l := range strings.Split(text, "\n") {
		if l != "" {
			out = append(out, r.Line([]byte(l))...)
		}
	}
	return out
}

// Samples converts the judge's samples (judge/<char>/samples.jsonl) to score events.
func Samples(text, char string) []Ev {
	var out []Ev
	for _, l := range strings.Split(text, "\n") {
		var m map[string]any
		if l == "" || json.Unmarshal([]byte(l), &m) != nil || m["t"] == nil {
			continue
		}
		delete(m, "signals")
		m["ev"], m["n"] = "score", char
		out = append(out, m)
	}
	return out
}
