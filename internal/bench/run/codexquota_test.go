package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Spec H10: the newest rollout's last rate_limits, recorded as start (once) and end; alerts at 80% and 90% once
// each; a pause at 97% or a reached limit.
func TestCodexQuota(t *testing.T) {
	home := t.TempDir()
	codexSessions = func() string { return home }
	day := filepath.Join(home, "2026/10/31")
	os.MkdirAll(day, 0o755)
	future := time.Now().Add(time.Hour).Unix()
	write := func(name string, mod time.Time, lines ...string) {
		f := filepath.Join(day, name)
		os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		os.Chtimes(f, mod, mod)
	}
	tc := func(ts string, used float64, resets int64, reached string) string {
		return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":%v,"resets_at":%d},"secondary":null,"rate_limit_reached_type":%s}}}`, ts, used, resets, reached)
	}
	write("rollout-old.jsonl", time.Now().Add(-time.Hour), tc("t0", 99, future, "null"))
	write("rollout-new.jsonl", time.Now(), tc("t1", 50, future, "null"), tc("t2", 42, future, "null"), `{"type":"other"}`)
	dir := t.TempDir()
	r := &Runner{dir: dir}
	if q := r.noteCodexQuota(); q == nil || q.At != "t2" || q.Used != 42 || q.Reached {
		t.Fatalf("quota: %+v", q)
	}
	write("rollout-new.jsonl", time.Now(), tc("t3", 85, future, "null"))
	r.noteCodexQuota()
	b, _ := os.ReadFile(filepath.Join(dir, "codex-quota.json"))
	if rec := string(b); !strings.Contains(rec, `"at": "t2"`) || !strings.Contains(rec, `"at": "t3"`) {
		t.Fatalf("codex-quota.json: %s", rec)
	}
	alerted := 0.0
	for _, c := range []struct {
		used         float64
		reached      bool
		alert, pause bool
	}{
		{50, false, false, false},
		{81, false, true, false},
		{85, false, false, false},
		{92, false, true, false},
		{95, false, false, false},
		{97, false, true, true},
		{10, true, true, true},
	} {
		msg, pause := codexQuotaAlert(&codexQuota{Used: c.used, Reached: c.reached}, &alerted)
		if (msg != "") != c.alert || pause != c.pause {
			t.Fatalf("%v%%, reached %v: msg %q pause %v", c.used, c.reached, msg, pause)
		}
	}
	write("rollout-new.jsonl", time.Now(), tc("t4", 99, time.Now().Add(-time.Minute).Unix(), "null")) // the window reset
	if q, _ := readCodexQuota(); q.Used != 0 {
		t.Fatalf("a reset window counted: %+v", q)
	}
}
