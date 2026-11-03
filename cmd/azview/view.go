// Reading bench runs: the characters' game logs (gamelog), agent transcripts and bench
// run directories, for the viewer (live.go, store.go) and movecheck. Map rendering, area names and item/creature
// names come from world (package world).
package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pthm/leeroy/runfiles"

	"github.com/pthm/leeroy/world"
)

// Class names and colours by class id.
var (
	Class = []string{"", "Warrior", "Paladin", "Hunter", "Rogue", "Priest", "Death Knight", "Shaman", "Mage", "Warlock", "", "Druid"}
	Color = []string{"#888", "#C79C6E", "#F58CBA", "#ABD473", "#FFF569", "#FFFFFF", "#C41F3B", "#0070DE", "#69CCF0", "#9482C9", "#888", "#FF7D0A"}
)

// Ev is one event, as gamelog reads it: a game log line's data (the SDK's types) beside t (unix ms), type and
// char (the character). The viewer adds its own: say (an agent's message, from its transcript) and score (a judge
// sample); on chat it sets speaker and heard.
type Ev = map[string]any

// classIDs: the SDK's class names as Class indexes.
var classIDs = map[string]int{
	"warrior": 1, "paladin": 2, "hunter": 3, "rogue": 4, "priest": 5, "death_knight": 6,
	"shaman": 7, "mage": 8, "warlock": 9, "druid": 11,
}

// Unit is a unit in an event (types.UnitRef: guid, name, entry for a creature, level, player), nil if none.
func Unit(e Ev, k string) map[string]any { u, _ := e[k].(map[string]any); return u }

// IsPlayer: is u a player.
func IsPlayer(u map[string]any) bool { return u != nil && Truthy(u["player"]) }

// Res is a types.Resource field (health, power): current and max.
func Res(e Ev, k string) (cur, total float64) {
	r, _ := e[k].(map[string]any)
	return N(r, "current"), N(r, "max")
}

// Repo is the repository root (the parent of data/).
func Repo() string { return filepath.Dir(world.DataDir()) }

// N is a numeric field (0 if missing or not a number); S a string field ("" if missing).
func N(e Ev, k string) float64 {
	f, _ := e[k].(float64)
	return f
}

func S(e Ev, k string) string {
	s, _ := e[k].(string)
	return s
}

// Truthy is JavaScript truthiness, for flags the logs write as bools or numbers.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	}
	return true
}

// Flag is the value after --k in args (the tools take flags anywhere, around positional arguments).
func Flag(args []string, k string) (string, bool) {
	for i, a := range args {
		if a == "--"+k && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func lines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ParseLines parses JSON lines, skipping any that aren't objects.
func ParseLines(text string) []Ev {
	var out []Ev
	for _, l := range lines(text) {
		var e Ev
		if json.Unmarshal([]byte(l), &e) == nil && e != nil {
			out = append(out, e)
		}
	}
	return out
}

func parseTime(s string) (float64, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, false
	}
	return float64(t.UnixMilli()), true
}

// TranscriptEvents is an agent transcript as "say" events: only the agent's visible text messages. Claude Code
// stream-json (assistant messages), Codex --json (agent_message items, timestamped by azbench as they arrive), or
// opencode's json (text parts).
func TranscriptEvents(text, name string) []Ev {
	var out []Ev
	for _, l := range lines(text) {
		var m struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content []struct{ Type, Text string } `json:"content"`
			} `json:"message"`
			Item struct{ Type, Text string } `json:"item"`
			Part struct{ Text string }       `json:"part"`
		}
		if json.Unmarshal([]byte(l), &m) != nil || m.Timestamp == "" {
			continue
		}
		t, ok := parseTime(m.Timestamp)
		if !ok {
			continue
		}
		if m.Type == "item.completed" && m.Item.Type == "agent_message" && strings.TrimSpace(m.Item.Text) != "" {
			out = append(out, Ev{"t": t, "type": "say", "char": name, "text": strings.TrimSpace(m.Item.Text)})
			continue
		}
		if m.Type == "text" && strings.TrimSpace(m.Part.Text) != "" {
			out = append(out, Ev{"t": t, "type": "say", "char": name, "text": strings.TrimSpace(m.Part.Text)})
			continue
		}
		if m.Type != "assistant" {
			continue
		}
		for _, c := range m.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				out = append(out, Ev{"t": t, "type": "say", "char": name, "text": strings.TrimSpace(c.Text)})
			}
		}
	}
	return out
}

// Transcript is one agent's transcript and the character it plays.
type Transcript struct{ Name, Path string }

// AgentMeta is who plays each character (azbench writes meta.json in each agent's run dir), as written.
func AgentMeta(ts []Transcript) map[string]any {
	out := map[string]any{}
	for _, t := range ts {
		b, err := os.ReadFile(filepath.Join(filepath.Dir(t.Path), "meta.json"))
		var m any
		if err == nil && json.Unmarshal(b, &m) == nil { // else being written
			out[t.Name] = m
		}
	}
	return out
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// RunSources is what a bench run has for the viewer (runfiles), by run.json: each player's game log
// (game/<char>/events.jsonl) and judge samples (its score), and its agent's transcript, each with the character.
func RunSources(dir string) (games, scores, transcripts []Transcript) {
	f := runfiles.Run(dir)
	m, err := f.ReadManifest()
	if err != nil {
		return
	}
	for _, p := range m.Players {
		if exists(f.Events(p.Char)) {
			games = append(games, Transcript{p.Char, f.Events(p.Char)})
		}
		if exists(f.Samples(p.Char)) {
			scores = append(scores, Transcript{p.Char, f.Samples(p.Char)})
		}
		if exists(f.Transcript(p.Agent)) {
			transcripts = append(transcripts, Transcript{p.Char, f.Transcript(p.Agent)})
		}
	}
	return
}

// chatKinds are the chat kinds the viewer keeps (the SDK's kinds): what players say, and NPC speech. The game's own
// lines (system, loot, money, xp, skill...) are left out: their events say it.
var chatKinds = map[string]bool{
	"say": true, "party": true, "raid": true, "raid_warning": true, "battleground": true,
	"guild": true, "officer": true, "yell": true, "whisper": true, "whisper_sent": true, "emote": true, "channel": true,
	"npc_say": true, "npc_party": true, "npc_yell": true, "npc_whisper": true, "npc_emote": true,
}

// ChatSides is who said a chat event (a player, or an NPC: npc) and, for a whisper, who it went to, from the SDK's
// chat: from is the speaker, except on the character's own lines (self: from is the character) and a whisper it
// sent (whisper_sent: from is who it went to).
func ChatSides(e Ev) (speaker, to string, npc bool) {
	kind, from, char := S(e, "kind"), S(e, "from"), S(e, "char")
	switch {
	case strings.HasPrefix(kind, "npc_"):
		return from, "", true
	case kind == "whisper_sent":
		return char, from, false
	case Truthy(e["self"]):
		return char, "", false
	case kind == "whisper":
		return from, char, false
	}
	return from, "", false
}

// NameUnits fills in what game logs carry only as ids: creature names, quest titles, achievement titles and points.
func NameUnits(events []Ev) {
	var refs []map[string]any
	var ids, questIDs []int
	var questEvs []Ev
	for _, e := range events {
		switch S(e, "type") {
		case "quest_turned_in":
			if S(e, "title") == "" {
				questEvs = append(questEvs, e)
				questIDs = append(questIDs, int(N(e, "quest")))
			}
		case "achievement":
			if a, ok := world.Achievements()[int(N(e, "id"))]; ok && S(e, "title") == "" {
				e["title"], e["points"] = a.Title, a.Points
			}
		}
		for _, k := range []string{"src", "dst", "victim", "killer", "target"} {
			u := Unit(e, k)
			if u == nil || IsPlayer(u) || Truthy(u["name"]) || !Truthy(u["entry"]) {
				continue
			}
			refs = append(refs, u)
			ids = append(ids, int(N(u, "entry")))
		}
	}
	if len(questEvs) > 0 {
		titles := world.QuestTitles(questIDs)
		for _, e := range questEvs {
			e["title"] = titles[int(N(e, "quest"))]
		}
	}
	if len(refs) == 0 {
		return
	}
	names := world.CreatureNames(ids)
	for _, u := range refs {
		u["name"] = names[int(N(u, "entry"))]
	}
}

// SortByTime sorts events by t, keeping the order of equal times.
func SortByTime(evs []Ev) {
	sort.SliceStable(evs, func(i, j int) bool { return N(evs[i], "t") < N(evs[j], "t") })
}
