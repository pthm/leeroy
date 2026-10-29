package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

// Ints is a number or a list of numbers.
type Ints []int

func (n *Ints) UnmarshalYAML(b []byte) error {
	var one int
	if yaml.Unmarshal(b, &one) == nil {
		*n = Ints{one}
		return nil
	}
	var many []int
	if err := yaml.Unmarshal(b, &many); err != nil {
		return err
	}
	*n = many
	return nil
}

// Matrix is a grid of solo runs: every task crossed with races, classes, durations and repetitions. Every cell is a
// new character and an empty workspace, so cells don't share notes or scripts.
type Matrix struct {
	Name     string            `yaml:"name"`
	Parallel int               `yaml:"parallel"` // cells at once; default 1. Concurrent cells play on the same server
	Reps     int               `yaml:"reps"`     // runs per cell; default 1
	Minutes  Ints              `yaml:"minutes"`  // default for tasks without their own
	Races    []string          `yaml:"races"`
	Classes  []string          `yaml:"classes"`
	Runtime  string            `yaml:"runtime"`
	Models   map[string]string `yaml:"models"`
	Tasks    []Task            `yaml:"tasks"`
}

// Task is one row of a matrix: a goal, and optionally its own grid and starting conditions.
type Task struct {
	Key     string   `yaml:"key"`
	Goal    string   `yaml:"goal"`
	Minutes Ints     `yaml:"minutes"`
	Races   []string `yaml:"races"`
	Classes []string `yaml:"classes"`
	Reps    int      `yaml:"reps"`
	Level   int      `yaml:"level"`
	Money   int      `yaml:"money"`
	Start   *Place   `yaml:"start"`
	Channel string   `yaml:"channel"`
	Hint    string   `yaml:"hint"`
}

// Cell is one solo run of a matrix.
type Cell struct {
	Key         string
	Goal        string
	Minutes     int
	Race, Class int
	Rep         int
	Task        Task
}

// LoadMatrix reads a matrix and expands it into cells, keeping the race/class pairs allowed reports true for.
func (b *Bench) LoadMatrix(f string, allowed func(race, class int) bool) (*Matrix, []Cell, error) {
	var m Matrix
	if err := readYAML(f, &m); err != nil {
		return nil, nil, err
	}
	if m.Name == "" {
		m.Name = strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
	}
	if m.Parallel == 0 {
		m.Parallel = 1
	}
	if m.Runtime == "" {
		m.Runtime = "claude"
	}
	var cells []Cell
	for _, t := range m.Tasks {
		if !goalRe.MatchString(t.Goal) {
			return nil, nil, fmt.Errorf("%s: task %s: goal %q", m.Name, t.Key, t.Goal)
		}
		mins, races, classes, reps := pick(t.Minutes, m.Minutes), pickS(t.Races, m.Races), pickS(t.Classes, m.Classes), t.Reps
		if reps == 0 {
			reps = max(m.Reps, 1)
		}
		if len(mins) == 0 {
			mins = Ints{60}
		}
		for _, min := range mins {
			for _, rs := range races {
				r, err := ID(Races, rs)
				if err != nil {
					return nil, nil, fmt.Errorf("%s: %s: race: %v", m.Name, t.Key, err)
				}
				for _, cs := range classes {
					c, err := ID(Classes, cs)
					if err != nil {
						return nil, nil, fmt.Errorf("%s: %s: class: %v", m.Name, t.Key, err)
					}
					if allowed != nil && !allowed(r, c) {
						continue
					}
					for i := range reps {
						cells = append(cells, Cell{Key: t.Key, Goal: t.Goal, Minutes: min, Race: r, Class: c, Rep: i + 1, Task: t})
					}
				}
			}
		}
	}
	return &m, cells, nil
}

func pick(a, b Ints) Ints {
	if len(a) > 0 {
		return a
	}
	return b
}

func pickS(a, b []string) []string {
	if len(a) > 0 {
		return a
	}
	return b
}

// Scenario is the solo run of one cell, for a character and agent named by the caller.
func (b *Bench) CellScenario(m *Matrix, c Cell, agent, char string) (*Scenario, error) {
	s := &Scenario{Name: fmt.Sprintf("%s-%s", m.Name, c.Key), Goal: c.Goal, Minutes: c.Minutes, Start: c.Task.Start,
		Level: c.Task.Level, Money: c.Task.Money, Channel: c.Task.Channel, Hint: c.Task.Hint, RequireFresh: true,
		Models: m.Models, Runtime: m.Runtime,
		Agents: []Agent{{Name: agent, Char: char, Race: fmt.Sprint(c.Race), Class: fmt.Sprint(c.Class)}}}
	return s, b.Check(s)
}
