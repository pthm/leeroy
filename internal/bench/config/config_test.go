package config

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
)

// every scenario and matrix in docs/ loads and checks out
func TestBenchConfigs(t *testing.T) {
	b, err := LoadBench("../../../suite")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Specs) < 10 || b.Places["goldshire"].Zone != 12 {
		t.Fatalf("tables: %d specs, goldshire %+v", len(b.Specs), b.Places["goldshire"])
	}
	files, _ := filepath.Glob("../../../suite/tasks/*.yaml")
	checks, _ := filepath.Glob("../../../suite/checks/*.yaml")
	files = append(files, checks...)
	chars := map[string]string{} // every task's characters and agents are their own
	for _, f := range files {
		ss, err := b.LoadTask(f, nil)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		for _, s := range ss {
			if _, err := s.NoticeText(); err != nil {
				t.Errorf("%s: notice: %v", f, err)
			}
			for _, a := range s.Agents {
				if other, ok := chars[a.Char]; ok {
					t.Errorf("%s: %s is also %s", s.Name, a.Char, other)
				}
				chars[a.Char] = s.Name
			}
		}
	}
	if len(files) == 0 || len(chars) < 20 {
		t.Fatalf("%d tasks, %d characters", len(files), len(chars))
	}
	if ss, err := b.LoadTask("../../../suite/tasks/hardcore.yaml", []string{"sol"}); err != nil || len(ss) != 1 || ss[0].Name != "hardcore-sol" || ss[0].Task != "hardcore" || ss[0].Agents[0].Effort != "medium" {
		t.Fatalf("hardcore, sol only: %v %+v", err, ss)
	} else if y, err := yaml.Marshal(ss[0]); err != nil || readYAMLBytes(y, &Scenario{}) != nil { // as a run dir keeps it
		t.Fatalf("scenario.yaml doesn't read back (%v):\n%s", err, y)
	}
	if ss, _ := b.LoadTask("../../../suite/tasks/travel.yaml", nil); len(ss) != 1 || len(ss[0].Agents) != 16 {
		t.Fatalf("travel is played together: %d runs", len(ss))
	}
}

func TestPlace(t *testing.T) {
	var s Scenario
	if err := readYAMLBytes([]byte("goal: level:5\nstart: \"-9460 70 56.6 0 12\"\nagents: [{name: a1, char: Abc, class: mage}]"), &s); err != nil {
		t.Fatal(err)
	}
	if s.Start.X != -9460 || s.Start.Zone != 12 {
		t.Fatalf("%+v", s.Start)
	}
}

func TestRounds(t *testing.T) {
	for _, n := range []int{2, 3, 4, 5, 16} {
		var ms []string
		for i := range n {
			ms = append(ms, fmt.Sprint("m", i))
		}
		met := map[[2]string]int{}
		rounds := Rounds(ms)
		for _, r := range rounds {
			in := map[string]bool{}
			for _, p := range r {
				if in[p[0]] || in[p[1]] {
					t.Fatalf("%d models: %v plays twice in a round", n, p)
				}
				in[p[0]], in[p[1]] = true, true
				a, b := min(p[0], p[1]), max(p[0], p[1])
				met[[2]string{a, b}]++
			}
		}
		if len(met) != n*(n-1)/2 {
			t.Fatalf("%d models: %d pairs, want %d", n, len(met), n*(n-1)/2)
		}
		for p, c := range met {
			if c != 1 {
				t.Fatalf("%d models: %v met %d times", n, p, c)
			}
		}
		if want := n - 1 + n%2; len(rounds) != want {
			t.Fatalf("%d models: %d rounds, want %d", n, len(rounds), want)
		}
	}
}
