package types

import (
	"encoding/json"
	"testing"
)

func TestGUIDJSONRoundTrip(t *testing.T) {
	for _, want := range []GUID{0, 42, 0xF130000000000042} {
		b, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		got := GUID(99)
		if err := json.Unmarshal(b, &got); err != nil || got != want {
			t.Fatalf("%s -> %v, %v", b, got, err)
		}
	}
	for _, bad := range []string{`""`, `"bad"`, `true`, `{}`, `-1`, `1.5`} {
		var g GUID
		if err := json.Unmarshal([]byte(bad), &g); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
