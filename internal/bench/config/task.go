package config

import (
	_ "embed"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"slices"
	"strings"
)

// Task is the benchmark's unit, one file in suite/tasks: a goal, a clock, starting conditions and a party, played by
// each model in its list. Apart (the default), every model's party plays its own run, one after another, so none
// share the world; together, every model's party plays one run in one world.
type Task struct {
	Conditions `yaml:",inline"`
	Party      []Member `yaml:"party"`
	Models     []string `yaml:"models"` // keys of models.yaml
	Together   bool     `yaml:"together"`
	// Versus: the models play each other in pairs, every pair once, in rounds where no model plays twice; a round is
	// one run, its pairs at once, each pair at its own place from Spots (rotating each round). The party is one
	// character; each pairing gets fresh ones.
	Versus bool     `yaml:"versus"`
	Spots  []string `yaml:"spots"` // places.yaml names, one for each pair in a round
	Page   *Page    `yaml:"page"`  // how the site presents it (azbench publish writes tasks.json); none: under its name
}

// Page is a task as the site presents it: its title, a short name for tight places, what it sets up and how it's
// scored for readers, and where it sorts among the tasks.
type Page struct {
	Title   string `yaml:"title" json:"title"`
	Short   string `yaml:"short" json:"short"`
	Summary string `yaml:"summary" json:"summary"`
	Scoring string `yaml:"scoring" json:"scoring"`
	Order   int    `yaml:"order" json:"order"`
}

// Pages are the published tasks' (suite/tasks) pages, by task name.
func (b *Bench) Pages() (map[string]*Page, error) {
	fs, err := filepath.Glob(filepath.Join(b.Dir, "tasks", "*.yaml"))
	if err != nil {
		return nil, err
	}
	pages := map[string]*Page{}
	for _, f := range fs {
		t, err := readTask(f)
		if err != nil {
			return nil, err
		}
		if t.Page != nil {
			pages[t.Name] = t.Page
		}
	}
	return pages, nil
}

// Member is one of a task's party: each model plays a character like it.
type Member struct {
	Class string `yaml:"class"`
	Spec  string `yaml:"spec"` // a key from specs.yaml
	Race  string `yaml:"race"` // default human
	Kit   string `yaml:"kit"`  // a starting kit, suite/kits/<name>.yaml: level, spells, gear (none: a new character)
}

// Model is who plays (models.yaml): a runtime, its model, and the reasoning effort, recorded with the run.
type Model struct {
	Runtime  string `yaml:"runtime"`  // claude, codex, opencode or none (no agent: for testing the harness)
	Model    string `yaml:"model"`    // default the runtime's own; opencode: provider/model
	Effort   string `yaml:"effort"`   // Agent.Effort: required for claude and codex
	Provider string `yaml:"provider"` // Agent.Provider: "" or openrouter
}

// LoadTask reads a task and makes its runs: one per model (only those in only, if any), or one with every model's
// party when the task is played together. Agents and characters are named from the task, the model and the party
// slot (charNames), the same each time, so a model's next run of the task finds its workspace (or --reset clears it).
func (b *Bench) LoadTask(f string, only []string) ([]*Scenario, error) {
	t, err := readTask(f)
	if err != nil {
		return nil, err
	}
	names, err := charNames(b.Dir)
	if err != nil {
		return nil, err
	}
	models := t.Models
	if len(only) > 0 {
		models = nil
		for _, m := range only {
			if !slices.Contains(t.Models, m) {
				return nil, fmt.Errorf("%s: model %q isn't one of the task's (%s)", t.Name, m, strings.Join(t.Models, ", "))
			}
			models = append(models, m)
		}
	}
	if t.Versus {
		return b.versus(t, models, names, f)
	}
	var out []*Scenario
	for _, key := range models {
		m, ok := b.Models[key]
		if !ok {
			return nil, fmt.Errorf("%s: unknown model %q (models.yaml)", t.Name, key)
		}
		agents, err := b.agents(t, key, m, names)
		if err != nil {
			return nil, err
		}
		if t.Together && len(out) == 1 {
			out[0].Agents = append(out[0].Agents, agents...)
			continue
		}
		s := &Scenario{Conditions: t.Conditions, Task: t.Name, Agents: agents, Dir: filepath.Dir(f)}
		if !t.Together {
			s.Name = t.Name + "-" + key
		}
		out = append(out, s)
	}
	for _, s := range out {
		if err := b.Check(s); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func readTask(f string) (*Task, error) {
	var t Task
	if err := readYAML(f, &t); err != nil {
		return nil, err
	}
	if t.Name == "" {
		t.Name = strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
	}
	if len(t.Party) == 0 || len(t.Models) == 0 {
		return nil, fmt.Errorf("%s: a task needs a party and models", t.Name)
	}
	for _, c := range t.Checkpoints {
		if c.Name == "" || (c.Kill == 0) == (c.Zone == 0) {
			return nil, fmt.Errorf("%s: a checkpoint needs a name and one of kill (a creature entry) or zone (a zone id)", t.Name)
		}
	}
	return &t, nil
}

func (b *Bench) agents(t *Task, key string, m Model, names map[string]string) ([]Agent, error) {
	var out []Agent
	for i, p := range t.Party {
		char := names[seat(t.Name, key, i)]
		a := Agent{
			Name: strings.ToLower(char), Char: char, Race: p.Race, Class: p.Class, Spec: p.Spec,
			Runtime: m.Runtime, Model: m.Model, Effort: m.Effort, Provider: m.Provider,
		}
		if p.Kit != "" {
			k, err := b.LoadKit(p.Kit)
			if err != nil {
				return nil, fmt.Errorf("%s: %v", t.Name, err)
			}
			a.Kit = k
		}
		out = append(out, a)
	}
	return out, nil
}

func seat(task, model string, slot int) string { return fmt.Sprintf("%s/%s/%d", task, model, slot) }

// versusSeat: a model's seat in one pairing of a versus task (a character for each opponent)
func versusSeat(task, model, opponent string) string { return seat(task, model+" vs "+opponent, 0) }

// Rounds pairs every model with every other once, in rounds where none plays twice (the circle method: one stays,
// the rest turn a place each round). An odd number of models gives one a bye each round.
func Rounds(models []string) [][][2]string {
	ms := slices.Clone(models)
	if len(ms)%2 == 1 {
		ms = append(ms, "") // the bye
	}
	n := len(ms)
	var out [][][2]string
	for r := 0; r < n-1; r++ {
		var round [][2]string
		for i := 0; i < n/2; i++ {
			a, b := ms[i], ms[n-1-i]
			if a != "" && b != "" {
				round = append(round, [2]string{a, b})
			}
		}
		out = append(out, round)
		ms = append([]string{ms[0], ms[n-1]}, ms[1:n-1]...) // turn all but the first
	}
	return out
}

// versus makes a versus task's runs: a run per round, each pair at its spot, each agent told its opponent.
func (b *Bench) versus(t *Task, models []string, names map[string]string, f string) ([]*Scenario, error) {
	if len(t.Party) != 1 || t.Start != nil || len(t.Spots) == 0 || len(models) < 2 {
		return nil, fmt.Errorf("%s: versus needs a party of one, spots (not start) and two or more models", t.Name)
	}
	var spots []Place
	for _, n := range t.Spots {
		p, ok := b.Places[n]
		if !ok {
			return nil, fmt.Errorf("%s: unknown spot %q (places.yaml)", t.Name, n)
		}
		spots = append(spots, p)
	}
	rounds := Rounds(models)
	if len(rounds[0]) > len(spots) {
		return nil, fmt.Errorf("%s: %d pairs a round, %d spots", t.Name, len(rounds[0]), len(spots))
	}
	var out []*Scenario
	for r, round := range rounds {
		s := &Scenario{Conditions: t.Conditions, Task: t.Name, Dir: filepath.Dir(f)}
		s.Name = fmt.Sprintf("%s-r%02d", t.Name, r+1)
		for j, pair := range round {
			spot := spots[(j+r)%len(spots)]
			for k, key := range pair {
				m, ok := b.Models[key]
				if !ok {
					return nil, fmt.Errorf("%s: unknown model %q (models.yaml)", t.Name, key)
				}
				opp := pair[1-k]
				as, err := b.agents(t, key, m, map[string]string{seat(t.Name, key, 0): names[versusSeat(t.Name, key, opp)]})
				if err != nil {
					return nil, err
				}
				as[0].Start, as[0].Opponent = &spot, names[versusSeat(t.Name, opp, key)]
				s.Agents = append(s.Agents, as[0])
			}
		}
		if err := b.Check(s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

//go:embed names.txt
var nameList string

// charNames names every character of the suite's tasks and checks (suite/tasks, then suite/checks) from names.txt, so
// no two share one: each seat (task, model, party slot; a versus task: model and opponent) hashes to a name, and takes the next free one if it's taken.
// Seats are named in file order, so a seat's name stays the same unless a new task, sorting before its own, lands on
// it (it then gets the next free one).
// Neutral names: chat doesn't say which model plays a character.
func charNames(dir string) (map[string]string, error) {
	list := strings.Fields(nameList)
	files, _ := filepath.Glob(filepath.Join(dir, "tasks", "*.yaml"))
	checks, _ := filepath.Glob(filepath.Join(dir, "checks", "*.yaml"))
	files = append(files, checks...)
	taken, out := map[string]bool{}, map[string]string{}
	for _, f := range files {
		t, err := readTask(f)
		if err != nil {
			return nil, err
		}
		seats := []string{}
		for _, key := range t.Models {
			if t.Versus {
				for _, opp := range t.Models {
					if opp != key {
						seats = append(seats, versusSeat(t.Name, key, opp))
					}
				}
				continue
			}
			for i := range t.Party {
				seats = append(seats, seat(t.Name, key, i))
			}
		}
		for _, s := range seats {
			h := fnv.New64a()
			h.Write([]byte(s))
			n := int(h.Sum64() % uint64(len(list)))
			for j := 0; taken[list[n]]; j++ {
				if j == len(list) {
					return nil, fmt.Errorf("names.txt has %d names, too few for every task's characters", len(list))
				}
				n = (n + 1) % len(list)
			}
			taken[list[n]], out[s] = true, list[n]
		}
	}
	return out, nil
}
