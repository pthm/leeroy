package config

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"slices"
	"strings"
)

// Task is the benchmark's unit, one file in bench/tasks: a goal, a clock, starting conditions and a party, played by
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
// slot, the same each time, so a model's next run of the task finds its workspace (or --reset clears it).
func (b *Bench) LoadTask(f string, only []string) ([]*Scenario, error) {
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
		if t.Together && len(out) == 1 {
			out[0].Agents = append(out[0].Agents, t.agents(key, m)...)
			continue
		}
		s := &Scenario{Conditions: t.Conditions, Task: t.Name, Agents: t.agents(key, m), Dir: filepath.Dir(f)}
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

func (t *Task) agents(key string, m Model) []Agent {
	var out []Agent
	for i, p := range t.Party {
		char := charName(fmt.Sprintf("%s/%s/%d", t.Name, key, i))
		out = append(out, Agent{Name: strings.ToLower(char), Char: char, Race: p.Race, Class: p.Class, Spec: p.Spec,
			Runtime: m.Runtime, Model: m.Model, Effort: m.Effort, Provider: m.Provider})
	}
	return out
}

// charName: a neutral character name from a seed, consonant-vowel syllables (letters only, 8), so chat doesn't say
// which model plays it.
func charName(seed string) string {
	const cons, vow = "bcdfghjklmnprstvwxyz", "aeiou"
	h := fnv.New64a()
	h.Write([]byte(seed))
	n := h.Sum64()
	s := make([]byte, 0, 8)
	for range 4 {
		s = append(s, cons[n%20], vow[n/20%5])
		n /= 100
	}
	s[0] -= 'a' - 'A'
	return string(s)
}
