package world

import (
	"os"
	"slices"
	"testing"
)

func TestDataFlag(t *testing.T) {
	old := os.Getenv("LEEROY_DATA")
	defer os.Setenv("LEEROY_DATA", old)
	got := DataFlag([]string{"azview", "live", "--data", "/srv/wow-data", "--port", "8801"})
	if !slices.Equal(got, []string{"azview", "live", "--port", "8801"}) || os.Getenv("LEEROY_DATA") != "/srv/wow-data" {
		t.Fatalf("args %v, LEEROY_DATA %q", got, os.Getenv("LEEROY_DATA"))
	}
	if got := DataFlag([]string{"azworld", "who", "Hogger"}); len(got) != 3 {
		t.Fatalf("no flag, no change: %v", got)
	}
}
