package run

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// The shared Codex account's quota, from the newest rate_limits Codex wrote in a rollout log (every token_count
// carries them). The supervisor reads it at preflight and every codexQuotaEvery, records it in
// <run>/codex-quota.json (start: once, end: the latest), alerts at 80% and 90%, and pauses the run at 97% or when
// Codex says a limit is reached.
var (
	codexSessions   = func() string { h, _ := os.UserHomeDir(); return filepath.Join(h, ".codex/sessions") } // for the test
	codexQuotaEvery = 5 * time.Minute
)

const codexQuotaPause = 97.0

// codexQuota: the newest rate_limits and when they were written. Used is the highest window's used_percent; a
// window whose resets_at has passed counts 0.
type codexQuota struct {
	At      string         `json:"at"`
	Used    float64        `json:"used_percent"`
	Reached bool           `json:"limit_reached"`
	Limits  map[string]any `json:"rate_limits"`
}

func readCodexQuota() (*codexQuota, error) {
	files, _ := filepath.Glob(filepath.Join(codexSessions(), "*/*/*/*.jsonl"))
	var newest string
	var mod time.Time
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && st.ModTime().After(mod) {
			newest, mod = f, st.ModTime()
		}
	}
	if newest == "" {
		return nil, fmt.Errorf("codex quota: no rollout logs in %s", codexSessions())
	}
	fh, err := os.Open(newest)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	if st, _ := fh.Stat(); st.Size() > 1<<20 { // ponytail: the last MiB has many token_counts
		fh.Seek(-1<<20, io.SeekEnd)
	}
	var q *codexQuota
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if !bytes.Contains(sc.Bytes(), []byte(`"rate_limits"`)) {
			continue
		}
		var e struct {
			Timestamp string
			Payload   struct {
				Type       string
				RateLimits map[string]any `json:"rate_limits"`
			}
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Payload.Type == "token_count" && e.Payload.RateLimits != nil {
			q = &codexQuota{At: e.Timestamp, Limits: e.Payload.RateLimits}
		}
	}
	if q == nil {
		return nil, fmt.Errorf("codex quota: no rate_limits in %s", newest)
	}
	for _, w := range []string{"primary", "secondary"} {
		win, _ := q.Limits[w].(map[string]any)
		used, _ := win["used_percent"].(float64)
		resets, _ := win["resets_at"].(float64)
		if resets == 0 || int64(resets) > time.Now().Unix() {
			q.Used = max(q.Used, used)
		}
	}
	q.Reached = q.Limits["rate_limit_reached_type"] != nil
	return q, nil
}

// noteCodexQuota reads the quota, records it in codex-quota.json and returns it (nil when unreadable, logged).
func (r *Runner) noteCodexQuota() *codexQuota {
	q, err := readCodexQuota()
	if err != nil {
		r.logf("%v", err)
		return nil
	}
	f := filepath.Join(r.dir, "codex-quota.json")
	rec := map[string]*codexQuota{}
	if b, err := os.ReadFile(f); err == nil {
		json.Unmarshal(b, &rec)
	}
	if rec["start"] == nil {
		rec["start"] = q
	}
	rec["end"] = q
	b, _ := json.MarshalIndent(rec, "", "  ")
	os.WriteFile(f, append(b, '\n'), 0o644)
	return q
}

// codexQuotaAlert: what the quota calls for: a pause at 97% or a reached limit, else an alert the first time it
// passes 80% or 90% (alerted: the highest threshold alerted so far). "" for nothing.
func codexQuotaAlert(q *codexQuota, alerted *float64) (msg string, pause bool) {
	if q.Reached || q.Used >= codexQuotaPause {
		return fmt.Sprintf("codex quota at %.0f%% (limit reached: %v)", q.Used, q.Reached), true
	}
	for _, at := range []float64{90, 80} {
		if q.Used >= at {
			if *alerted < at {
				*alerted = at
				return fmt.Sprintf("codex quota at %.0f%%", q.Used), false
			}
			break
		}
	}
	return "", false
}

// watchCodexQuota checks the quota every codexQuotaEvery until the run ends (alerted: from the preflight).
func (r *Runner) watchCodexQuota(alerted float64) {
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(codexQuotaEvery):
		}
		q := r.noteCodexQuota()
		if q == nil {
			continue
		}
		if msg, pause := codexQuotaAlert(q, &alerted); pause {
			r.alert(nil, msg+": pausing the run")
			r.pause()
			return
		} else if msg != "" {
			r.alert(nil, msg)
		}
	}
}

// usesCodex: a player runs on Codex.
func (r *Runner) usesCodex() bool {
	for _, p := range r.players {
		if p.a.Runtime == "codex" {
			return true
		}
	}
	return false
}
