package judge

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Price is a model's list price in USD per million tokens, from models.dev.
type Price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"` // Anthropic's 5-minute cache write; 0 where writes are billed as input
}

// prices.json: provider/model -> Price, for the providers in pricedProviders; `azbench prices` refreshes it.
//
//go:embed prices.json
var pricesJSON []byte

var pricedProviders = []string{"anthropic", "openai", "openrouter", "google", "deepseek", "moonshotai", "zai",
	"xai", "mistral", "alibaba", "minimax"}

// priceKey is the models.dev key for an agent's model: opencode's model is already provider/model; Claude and Codex
// report the bare model, served by Anthropic and OpenAI unless the provider (meta.json) says otherwise.
func priceKey(runtime, provider, model string) string {
	switch {
	case runtime == "opencode":
		return model
	case provider != "":
		return provider + "/" + model
	case runtime == "codex":
		return "openai/" + model
	}
	return "anthropic/" + model
}

// cost is what tokens cost at a model's list price; ok is false for a model not in the table. Claude Code writes
// its cache with a 1-hour TTL, which Anthropic bills at 2x input rather than the 5-minute rate models.dev lists;
// OpenRouter bills the 5-minute rate (measured: a smoke run's bill matched it).
// ponytail: flat rates; long-context tiers (a prompt over 200k-272k) need per-request sizes, not totals
func cost(runtime, provider, model string, in, out, cr, cw float64) (usd float64, ok bool) {
	var table map[string]Price
	if json.Unmarshal(pricesJSON, &table) != nil {
		return 0, false
	}
	p, ok := table[priceKey(runtime, provider, model)]
	if !ok {
		return 0, false
	}
	write := p.CacheWrite
	if runtime == "claude" && provider == "" {
		write = 2 * p.Input
	} else if write == 0 {
		write = p.Input
	}
	return (in*p.Input + out*p.Output + cr*p.CacheRead + cw*write) / 1e6, true
}

// Prices fetches models.dev and writes the table (azbench/judge/prices.json) for the priced providers.
func Prices(out string) error {
	resp, err := http.Get("https://models.dev/api.json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("models.dev: %s", resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var all map[string]struct {
		Models map[string]struct{ Cost *Price }
	}
	if err := json.Unmarshal(b, &all); err != nil {
		return fmt.Errorf("models.dev: %v", err)
	}
	table := map[string]Price{}
	for _, prov := range pricedProviders {
		for id, m := range all[prov].Models {
			if m.Cost != nil {
				table[prov+"/"+id] = *m.Cost
			}
		}
	}
	j, _ := json.MarshalIndent(table, "", " ")
	if err := os.WriteFile(out, append(j, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("%d models from %s\n", len(table), strings.Join(pricedProviders, ", "))
	return nil
}
