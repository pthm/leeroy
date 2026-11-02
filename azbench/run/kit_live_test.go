package run

import (
	"os"
	"slices"
	"testing"
)

// A generated kit put on an offline character reads back as given: level, gear, bags, the trained spells and the
// weapon skills at their cap. WOWBENCH_LIVE_KIT=<Char>: an existing warrior, offline, that the test may change.
func TestKitLive(t *testing.T) {
	char := os.Getenv("WOWBENCH_LIVE_KIT")
	if char == "" {
		t.Skip("set WOWBENCH_LIVE_KIT to a throwaway warrior character (offline)")
	}
	k, _, err := GenKit(1, 1, "protwarrior", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Gear) < 8 || len(k.Bags) != 4 {
		t.Fatalf("thin kit: %+v", k)
	}
	if err := applyKit(k, char, 1); err != nil {
		t.Fatal(err)
	}
	got, _, err := SaveKit(char)
	if err != nil {
		t.Fatal(err)
	}
	if got.Level != 20 || got.Money != k.Money || len(got.Bags) != 4 {
		t.Fatalf("level %d money %d bags %v", got.Level, got.Money, got.Bags)
	}
	for s, e := range k.Gear {
		if got.Gear[s] != e {
			t.Errorf("%s: %d, want %d", s, got.Gear[s], e)
		}
	}
	for _, s := range []int{100, 72, 7384, 2687, 71, 355, 7386} { // Charge, Shield Bash, Overpower, Bloodrage (trainer); Defensive Stance, Taunt, Sunder Armor (the level-10 quest)
		if !slices.Contains(got.Spells, s) {
			t.Errorf("spell %d not trained", s)
		}
	}
	r, err := chars().Query("SELECT value, max FROM character_skills WHERE skill = 95 AND guid = (SELECT guid FROM characters WHERE name = ?)", char)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var v, m int
	if !r.Next() || r.Scan(&v, &m) != nil || v != 100 || m != 100 {
		t.Fatalf("Defense %d/%d, want 100/100", v, m)
	}
}
