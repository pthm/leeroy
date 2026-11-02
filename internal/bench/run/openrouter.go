package run

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// openrouterURL: the key's info (usage is what it has spent, in USD); a variable for the test
var openrouterURL = "https://openrouter.ai/api/v1/key"

// openrouterUsage: what the OPENROUTER_API_KEY has spent so far, in USD.
func openrouterUsage() (float64, error) {
	req, _ := http.NewRequest("GET", openrouterURL, nil)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("OPENROUTER_API_KEY"))
	c := http.Client{Timeout: 15 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var r struct{ Data struct{ Usage *float64 } }
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || r.Data.Usage == nil {
		return 0, fmt.Errorf("openrouter key usage: %s %v", resp.Status, err)
	}
	return *r.Data.Usage, nil
}

// noteOpenrouter records the key's spend in <run>/openrouter.json: at the start (start: once) and at each finish
// (end), so the report can show what OpenRouter billed for the run next to its own token-priced estimate. Anything
// else using the key at the same time counts too. Only for runs with an agent on provider openrouter.
func (r *Runner) noteOpenrouter(end bool) {
	uses := false
	for _, p := range r.players {
		uses = uses || p.a.Provider == "openrouter"
	}
	if !uses {
		return
	}
	usage, err := openrouterUsage()
	if err != nil {
		r.logf("%v", err)
		return
	}
	f := filepath.Join(r.dir, "openrouter.json")
	rec := map[string]float64{}
	if b, err := os.ReadFile(f); err == nil {
		json.Unmarshal(b, &rec)
	}
	if end {
		rec["end"] = usage
	} else if _, ok := rec["start"]; !ok {
		rec["start"] = usage
	}
	b, _ := json.Marshal(rec)
	os.WriteFile(f, b, 0o644)
}
