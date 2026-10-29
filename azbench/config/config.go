// Package config reads the benchmark's YAML: scenarios (one run: a team, or a single agent), matrices (a grid of solo
// runs) and the shared tables they refer to, bench/specs.yaml (class specs and roles) and bench/places.yaml (named
// starting positions). See bench/scenarios/README.md for the fields.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// Scenario is one run: every agent starts on one clock.
type Scenario struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Goal        string `yaml:"goal"`    // a judge goal: score:100000, level:10, quest:176, skill:164:75 ...
	Minutes     int    `yaml:"minutes"` // default 60
	// starting conditions, applied in the database while the characters are offline
	Start   *Place   `yaml:"start"` // a name from places.yaml, or {x, y, z, map, zone}; nil: where they are
	Level   int      `yaml:"level"`
	Money   int      `yaml:"money"`   // copper
	Setup   []string `yaml:"setup"`   // revive, repair, reset_talents (for characters from earlier runs)
	Channel string   `yaml:"channel"` // the zone channel joined at login; default General - Elwynn Forest
	Hint    string   `yaml:"hint"`    // appended to the goal in the prompt
	Notice  string   `yaml:"notice"`  // added to the prompt: text, or a .md file next to the YAML
	// RequireFresh refuses to start if any agent's workspace exists (a run for new agents)
	RequireFresh bool              `yaml:"require_fresh"`
	Models       map[string]string `yaml:"models"`  // runtime -> model (default: the runtime's own)
	Runtime      string            `yaml:"runtime"` // default runtime for agents without one: claude
	Race         string            `yaml:"race"`    // default race: human
	Agents       []Agent           `yaml:"agents"`

	Dir string `yaml:"-"` // the YAML's directory (for notice files)
}

// Agent is one player: an agent (its workspace, bench/workspaces/<name>) and the character it plays.
type Agent struct {
	Name    string `yaml:"name"`
	Char    string `yaml:"char"`
	Race    string `yaml:"race"`    // human, dwarf, night elf, gnome, orc, undead, tauren, troll, blood elf, draenei
	Class   string `yaml:"class"`   // warrior, paladin, hunter, rogue, priest, death knight, shaman, mage, warlock, druid
	Spec    string `yaml:"spec"`    // a key from specs.yaml: the prompt states class, spec, role and key stats
	Runtime string `yaml:"runtime"` // claude, codex, or none (no agent: the session and judge only, for testing)
	Model   string `yaml:"model"`
}

// Place is a starting position: x y z map zone.
type Place struct {
	Name      string
	X, Y, Z   float64
	Map, Zone int
}

// UnmarshalYAML takes a place name (resolved later against places.yaml), "x y z map zone", or a mapping.
func (p *Place) UnmarshalYAML(b []byte) error {
	var s string
	if yaml.Unmarshal(b, &s) == nil {
		if f := strings.Fields(s); len(f) == 5 {
			return p.parse(f)
		}
		p.Name = s
		return nil
	}
	var m struct {
		X, Y, Z   float64
		Map, Zone int
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return err
	}
	*p = Place{X: m.X, Y: m.Y, Z: m.Z, Map: m.Map, Zone: m.Zone}
	return nil
}

// MarshalYAML writes a place as "x y z map zone" (a run dir's scenario.yaml).
func (p Place) MarshalYAML() (any, error) {
	return fmt.Sprintf("%g %g %g %d %d", p.X, p.Y, p.Z, p.Map, p.Zone), nil
}

func (p *Place) parse(f []string) error {
	var v [5]float64
	for i, s := range f {
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return fmt.Errorf("place %q: %v", strings.Join(f, " "), err)
		}
		v[i] = n
	}
	*p = Place{X: v[0], Y: v[1], Z: v[2], Map: int(v[3]), Zone: int(v[4])}
	return nil
}

// Spec is a class specialisation (specs.yaml).
type Spec struct {
	Title    string `yaml:"title"` // Warrior - Arms
	Role     string `yaml:"role"`  // DPS, Tank, Healer
	Optimise string `yaml:"optimise"`
	Stats    string `yaml:"stats"`
}

var Races = map[string]int{"human": 1, "orc": 2, "dwarf": 3, "night elf": 4, "undead": 5, "tauren": 6, "gnome": 7,
	"troll": 8, "blood elf": 10, "draenei": 11}
var Classes = map[string]int{"warrior": 1, "paladin": 2, "hunter": 3, "rogue": 4, "priest": 5, "death knight": 6,
	"shaman": 7, "mage": 8, "warlock": 9, "druid": 11}

// RaceName and ClassName: the display name of an id ("Night Elf").
func RaceName(id int) string  { return title(key(Races, id)) }
func ClassName(id int) string { return title(key(Classes, id)) }

func key(m map[string]int, id int) string {
	for k, v := range m {
		if v == id {
			return k
		}
	}
	return strconv.Itoa(id)
}

func title(s string) string {
	w := strings.Fields(s)
	for i, x := range w {
		w[i] = strings.ToUpper(x[:1]) + x[1:]
	}
	return strings.Join(w, " ")
}

// ID resolves a race or class by name (any case) or number.
func ID(m map[string]int, s string) (int, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return n, nil
	}
	if n, ok := m[strings.ToLower(strings.TrimSpace(s))]; ok {
		return n, nil
	}
	return 0, fmt.Errorf("unknown %q", s)
}

var (
	nameRe  = regexp.MustCompile(`^[A-Za-z]{2,12}$`)
	goalRe  = regexp.MustCompile(`^((level|item|quality|money|quests|quest|achpoints|score):\d+|skill:\d+:\d+)$`)
	agentRe = regexp.MustCompile(`^[a-z0-9-]{1,16}$`)
)

// Bench is the bench directory's shared tables.
type Bench struct {
	Dir    string // bench/
	Specs  map[string]Spec
	Places map[string]Place
}

// LoadBench reads specs.yaml and places.yaml from the bench directory.
func LoadBench(dir string) (*Bench, error) {
	b := &Bench{Dir: dir, Specs: map[string]Spec{}, Places: map[string]Place{}}
	if err := readYAML(filepath.Join(dir, "specs.yaml"), &b.Specs); err != nil {
		return nil, err
	}
	var places map[string]string
	if err := readYAML(filepath.Join(dir, "places.yaml"), &places); err != nil {
		return nil, err
	}
	for k, v := range places {
		var p Place
		if err := p.parse(strings.Fields(v)); err != nil {
			return nil, err
		}
		p.Name = k
		b.Places[k] = p
	}
	return b, nil
}

func readYAML(f string, v any) error {
	data, err := os.ReadFile(f)
	if err != nil {
		return err
	}
	if err := readYAMLBytes(data, v); err != nil {
		return fmt.Errorf("%s: %v", f, err)
	}
	return nil
}

// readYAMLBytes decodes strictly: an unknown field is an error, not a silently ignored typo.
func readYAMLBytes(data []byte, v any) error {
	if err := yaml.UnmarshalWithOptions(data, v, yaml.Strict()); err != nil {
		return fmt.Errorf("%s", yaml.FormatError(err, false, true))
	}
	return nil
}

// LoadScenario reads and checks a scenario.
func (b *Bench) LoadScenario(f string) (*Scenario, error) {
	var s Scenario
	if err := readYAML(f, &s); err != nil {
		return nil, err
	}
	s.Dir = filepath.Dir(f)
	if s.Name == "" {
		s.Name = strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
	}
	return &s, b.Check(&s)
}

// Check fills in defaults and validates a scenario.
func (b *Bench) Check(s *Scenario) error {
	if s.Minutes == 0 {
		s.Minutes = 60
	}
	if s.Channel == "" {
		s.Channel = "General - Elwynn Forest"
	}
	if s.Runtime == "" {
		s.Runtime = "claude"
	}
	if s.Race == "" {
		s.Race = "human"
	}
	if !goalRe.MatchString(s.Goal) {
		return fmt.Errorf("%s: goal %q: want level:<n>, score:<n>, quest:<id>, skill:<id>:<n>, ...", s.Name, s.Goal)
	}
	if s.Start != nil && s.Start.Name != "" {
		p, ok := b.Places[s.Start.Name]
		if !ok {
			return fmt.Errorf("%s: unknown place %q (places.yaml)", s.Name, s.Start.Name)
		}
		s.Start = &p
	}
	for _, st := range s.Setup {
		if st != "revive" && st != "repair" && st != "reset_talents" {
			return fmt.Errorf("%s: unknown setup step %q (revive, repair, reset_talents)", s.Name, st)
		}
	}
	if len(s.Agents) == 0 {
		return fmt.Errorf("%s: no agents", s.Name)
	}
	seen := map[string]bool{}
	for i := range s.Agents {
		a := &s.Agents[i]
		if a.Race == "" {
			a.Race = s.Race
		}
		if a.Runtime == "" {
			a.Runtime = s.Runtime
		}
		if a.Model == "" {
			a.Model = s.Models[a.Runtime]
		}
		switch {
		case !agentRe.MatchString(a.Name):
			return fmt.Errorf("%s: agent name %q: lowercase letters, digits and -, up to 16", s.Name, a.Name)
		case !nameRe.MatchString(a.Char):
			return fmt.Errorf("%s: character name %q: 2-12 letters", s.Name, a.Char)
		case seen[a.Name] || seen["char:"+a.Char]:
			return fmt.Errorf("%s: %s/%s appears twice", s.Name, a.Name, a.Char)
		case a.Runtime != "claude" && a.Runtime != "codex" && a.Runtime != "none":
			return fmt.Errorf("%s: %s: runtime %q (claude, codex or none)", s.Name, a.Name, a.Runtime)
		}
		seen[a.Name], seen["char:"+a.Char] = true, true
		if _, err := ID(Races, a.Race); err != nil {
			return fmt.Errorf("%s: %s: race: %v", s.Name, a.Name, err)
		}
		if _, err := ID(Classes, a.Class); err != nil {
			return fmt.Errorf("%s: %s: class: %v", s.Name, a.Name, err)
		}
		if a.Spec != "" {
			if _, ok := b.Specs[a.Spec]; !ok {
				return fmt.Errorf("%s: %s: unknown spec %q (specs.yaml)", s.Name, a.Name, a.Spec)
			}
		}
	}
	return nil
}

// NoticeText is the notice: the file's text when it names a file next to the scenario, else the text itself.
func (s *Scenario) NoticeText() (string, error) {
	n := strings.TrimSpace(s.Notice)
	if strings.HasSuffix(n, ".md") && !strings.Contains(n, "\n") {
		b, err := os.ReadFile(filepath.Join(s.Dir, n))
		return strings.TrimRight(string(b), "\n"), err
	}
	return n, nil
}
