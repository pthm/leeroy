package spelltext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func book(t *testing.T) *Book {
	dir := os.Getenv("AC_DATA_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "data")
	}
	b, err := Load(dir)
	if err != nil {
		t.Skip(err)
	}
	return b
}

func TestDescriptions(t *testing.T) {
	b := book(t)
	for id, want := range map[uint32]string{
		53:   "Backstab the target, causing 150% weapon damage plus 15 to the target.  Must be behind the target.  Requires a dagger in the main hand.  Awards 1 combo point.",
		133:  "Hurls a fiery ball that causes 14 to 22 Fire damage and an additional 2 Fire damage over 4 sec.",
		2061: "Heals a friendly target for 193 to 237.",
		6673: "The warrior shouts, increasing attack power of all raid and party members within 30 yards by 15.  Lasts 2 min.",
	} {
		if got := b.Description(id); got != want {
			t.Errorf("%d:\n got %q\nwant %q", id, got, want)
		}
	}
	if got := b.Tooltip(133); got != "1 Fire damage every 2 seconds." {
		t.Errorf("tooltip 133: %q", got)
	}
}

func TestStatScaling(t *testing.T) {
	b := book(t)
	// Revenge rank 1: ${$m1+$AP*0.310} to ${$M1+$AP*0.310}
	want := "Instantly counterattack an enemy for 99 (+31% of attack power) to 121 (+31% of attack power) damage."
	if got := b.Description(6572); !strings.HasPrefix(got, want) {
		t.Errorf("revenge:\n got %q\nwant %q...", got, want)
	}
	if got := b.DescriptionWith(6572, Stats{"AP": 76}); !strings.HasPrefix(got, "Instantly counterattack an enemy for 123 to 145 damage.") {
		t.Errorf("revenge with AP 76 (rounded like the client): %q", got)
	}
	if got := b.DescriptionWith(6572, Stats{"AP": 100}); !strings.HasPrefix(got, "Instantly counterattack an enemy for 130 to 152 damage.") {
		t.Errorf("revenge with AP 100: %q", got)
	}
}
