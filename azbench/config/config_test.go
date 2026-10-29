package config

import (
	"path/filepath"
	"testing"
)

// every scenario and matrix in bench/ loads and checks out
func TestBenchConfigs(t *testing.T) {
	b, err := LoadBench("../../bench")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Specs) < 10 || b.Places["goldshire"].Zone != 12 {
		t.Fatalf("tables: %d specs, goldshire %+v", len(b.Specs), b.Places["goldshire"])
	}
	files, _ := filepath.Glob("../../bench/scenarios/*.yaml")
	for _, f := range files {
		s, err := b.LoadScenario(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if _, err := s.NoticeText(); err != nil {
			t.Errorf("%s: notice: %v", f, err)
		}
	}
	m, cells, err := b.LoadMatrix("../../bench/matrices/starter.yaml", func(r, c int) bool { return !(r == 7 && (c == 2 || c == 5)) && !(r == 3 && c == 8) })
	if err != nil {
		t.Fatal(err)
	}
	if m.Parallel != 4 || len(cells) != 82 { // as the old matrix.sh's dry run
		t.Fatalf("matrix: parallel %d, %d cells", m.Parallel, len(cells))
	}
	if _, err := b.CellScenario(m, cells[0], "mtest", "Bakuso"); err != nil {
		t.Fatal(err)
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
