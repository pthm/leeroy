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

//go:embed names.txt
var nameList string

// charNames names every character of the suite's tasks and checks (suite/tasks, then suite/checks) from names.txt, so
// no two share one: each seat (task, model, party slot) hashes to a name, and takes the next free one if it's taken.
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
		for _, key := range t.Models {
			for i := range t.Party {
				s := seat(t.Name, key, i)
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
	}
	return out, nil
}
