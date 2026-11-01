// Package recorder is the shared side of the run viewer (cmd replay: static replays; cmd live: the live viewer;
// cmd movecheck): reading recorder-format logs (the server's mod-recorder log and azsdk -record files), agent
// transcripts and bench run directories, and filling in the viewer page (replay.html). Map rendering, area names
// and item/creature names come from azeroth-sdk/world.
package main

import (
	_ "embed"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pthm/leeroy/azworld/world"
)

//go:embed replay.html
var page string

// Class names and colours by class id.
var (
	Class = []string{"", "Warrior", "Paladin", "Hunter", "Rogue", "Priest", "Death Knight", "Shaman", "Mage", "Warlock", "", "Druid"}
	Color = []string{"#888", "#C79C6E", "#F58CBA", "#ABD473", "#FFF569", "#FFFFFF", "#C41F3B", "#0070DE", "#69CCF0", "#9482C9", "#888", "#FF7D0A"}
)

// Ev is one recorder event: {"t": ms, "ev": kind, "n": character, ...}.
type Ev = map[string]any

type ev = Ev

// Repo is the repository root (the parent of data/).
func Repo() string { return filepath.Dir(world.DataDir()) }

// ServerLog is the server's mod-recorder log.
func ServerLog() string {
	return filepath.Join(Repo(), "server", "azerothcore", "env", "dist", "logs", "recorder.jsonl")
}

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
			out = append(out, Ev{"t": t, "ev": "say", "n": name, "text": strings.TrimSpace(m.Item.Text)})
			continue
		}
		if m.Type == "text" && strings.TrimSpace(m.Part.Text) != "" {
			out = append(out, Ev{"t": t, "ev": "say", "n": name, "text": strings.TrimSpace(m.Part.Text)})
			continue
		}
		if m.Type != "assistant" {
			continue
		}
		for _, c := range m.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				out = append(out, Ev{"t": t, "ev": "say", "n": name, "text": strings.TrimSpace(c.Text)})
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

var (
	recordFile = regexp.MustCompile(`(^|\.)record\.jsonl$`)
	charLine   = regexp.MustCompile(`Your\s+character (\w+)|You are (\w+), an? `)
)

func charOf(runDir string) string {
	b, err := os.ReadFile(filepath.Join(runDir, "prompt.md"))
	if err != nil {
		return ""
	}
	m := charLine.FindStringSubmatch(string(b)) // "Your character X is..." or, with a spec, "You are X, a Human Warrior..."
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// RecordFiles are a directory's record.jsonl and <agent>.record.jsonl files.
func RecordFiles(dir string) []string {
	var out []string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if recordFile.MatchString(e.Name()) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// RunSources is everything a bench run directory has: record files, and each agent's transcript with its character.
// A team dir holds <agent>.record.jsonl; the agents' own run dirs (<time>-<agent>/) sit beside it.
func RunSources(dir string) (records []string, transcripts []Transcript) {
	records = RecordFiles(dir)
	if exists(filepath.Join(dir, "agent.jsonl")) { // a single run
		if name := charOf(dir); name != "" {
			transcripts = append(transcripts, Transcript{name, filepath.Join(dir, "agent.jsonl")})
		}
	}
	parent := filepath.Dir(dir)
	sibs, _ := os.ReadDir(parent)
	for _, r := range records { // team: find each agent's run dir by its suffix
		agent := regexp.MustCompile(`\.?record\.jsonl$`).ReplaceAllString(filepath.Base(r), "")
		if agent == "" {
			continue
		}
		runDir := filepath.Join(dir, agent) // azbench: the agent's files in the run dir
		if !exists(filepath.Join(runDir, "agent.jsonl")) {
			runDir = ""
			for _, d := range sibs { // older team runs: a sibling <time>-<agent> dir (sorted: the last is the latest)
				if strings.HasSuffix(d.Name(), "-"+agent) {
					runDir = filepath.Join(parent, d.Name())
				}
			}
		}
		if runDir == "" {
			continue
		}
		if name := charOf(runDir); name != "" {
			transcripts = append(transcripts, Transcript{name, filepath.Join(runDir, "agent.jsonl")})
		}
	}
	return records, transcripts
}

// ServerEvents is what the server's recorder log has better than the agents' own records, for the run's characters:
//   - player chat, one event per message, n = sender, to = channel/party/whisper target (every agent's client log has
//     its own copy of each message it received, with no sender name)
//   - damage and heals, every one involving a player (client logs have only melee and spell damage, no heals)
//   - targets: a "tgt" event each time a character's target (the pos samples' tgt) changes, tgt absent for none.
//     lastTgt holds each character's current target across calls, for a log read in pieces.
func ServerEvents(text string, chars map[string]bool, lastTgt map[string]string) []Ev {
	var out []Ev
	for _, l := range lines(text) {
		pos := strings.Contains(l, `"ev":"pos"`)
		if !pos && !strings.Contains(l, `"ev":"chat"`) && !strings.Contains(l, `"ev":"dmg"`) && !strings.Contains(l, `"ev":"heal"`) {
			continue
		}
		var e Ev
		if json.Unmarshal([]byte(l), &e) != nil || e == nil || !chars[S(e, "n")] {
			continue
		}
		if !pos {
			out = append(out, e)
			continue
		}
		n := S(e, "n")
		tgt, _ := json.Marshal(e["tgt"])
		if string(tgt) == lastTgt[n] {
			continue
		}
		lastTgt[n] = string(tgt)
		t := Ev{"t": e["t"], "ev": "tgt", "n": n, "p": e["p"]}
		if e["tgt"] != nil {
			t["tgt"] = e["tgt"]
		}
		out = append(out, t)
	}
	return out
}

// player chat (say, party, raid, guild, officer, yell, whisper and its echo, emote, channel, raid leader and warning,
// party leader) and the achievement broadcast (the achievement event has it)
var playerChat = map[float64]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 9: true, 10: true, 17: true, 39: true, 40: true, 48: true, 51: true}

// ClientChatFilter keeps client events minus player chat (that comes from ServerEvents). NPC speech is kept once per
// listener: the page merges the copies and knows who heard it. With the server log (server), damage comes from there too.
// ponytail: the server log's existence stands in for "it covers this run"; check its time range if old runs lose damage.
func ClientChatFilter(server bool) func(Ev) bool {
	return func(e Ev) bool {
		switch {
		case server && S(e, "ev") == "dmg":
			return false
		case S(e, "ev") == "chat":
			return !playerChat[N(e, "type")]
		}
		return true
	}
}

// TrackRow is one position sample as the viewer stores it:
// [t, x, y, hp, hpmax, pw, pwmax, pt, lvl, xp, money, alive, ghost, combat, zone, map, group id (0 = none)]
func TrackRow(p Ev, t0 float64) []any {
	grp := p["grp"]
	if grp == nil {
		grp = 0.0
	}
	return []any{N(p, "t") - t0, p["x"], p["y"], p["hp"], p["hpmax"], p["pw"], p["pwmax"], p["pt"], p["lvl"], p["xp"],
		p["money"], p["alive"], p["ghost"], p["combat"], p["zone"], p["m"], grp}
}

// NameUnits fills in what SDK recordings carry only as ids: creature names, quest titles, achievement titles and points.
func NameUnits(events []Ev) {
	var refs []map[string]any
	var ids, questIDs []int
	var questEvs []Ev
	for _, e := range events {
		switch S(e, "ev") {
		case "quest":
			if S(e, "title") == "" {
				questEvs = append(questEvs, e)
				questIDs = append(questIDs, int(N(e, "id")))
			}
		case "achievement":
			if a, ok := world.Achievements()[int(N(e, "id"))]; ok && S(e, "title") == "" {
				e["title"], e["points"] = a.Title, a.Points
			}
		}
		for _, k := range []string{"src", "dst", "victim", "killer", "target", "tgt"} {
			u, ok := e[k].(map[string]any)
			if !ok || Truthy(u["pl"]) || Truthy(u["n"]) || !Truthy(u["id"]) {
				continue
			}
			refs = append(refs, u)
			ids = append(ids, int(N(u, "id")))
		}
	}
	if len(questEvs) > 0 {
		titles := world.QuestTitles(questIDs)
		for _, e := range questEvs {
			e["title"] = titles[int(N(e, "id"))]
		}
	}
	if len(refs) == 0 {
		return
	}
	names := world.CreatureNames(ids)
	for _, u := range refs {
		u["n"] = names[int(N(u, "id"))]
	}
}

// SortByTime sorts events by t, keeping the order of equal times.
func SortByTime(evs []Ev) {
	sort.SliceStable(evs, func(i, j int) bool { return N(evs[i], "t") < N(evs[j], "t") })
}

// Page is the viewer page with its title, map image (a data: URL, or "" for live tiles) and data filled in.
func Page(title, mapURL string, data any) (string, error) {
	b, err := json.Marshal(data) // escapes < > &, so the data can't close its <script> tag
	if err != nil {
		return "", err
	}
	tmpl := page
	if f := os.Getenv("AZVIEW_PAGE"); f != "" { // working on the page: read it from disk on every request
		if b, err := os.ReadFile(f); err == nil {
			tmpl = string(b)
		}
	}
	s := strings.Replace(tmpl, "__TITLE__", title, 1)
	s = strings.Replace(s, "__MAP__", mapURL, 1)
	return strings.Replace(s, "__DATA__", string(b), 1), nil
}
