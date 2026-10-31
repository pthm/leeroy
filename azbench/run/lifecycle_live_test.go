package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pthm/leeroy/azbench/config"
	"github.com/pthm/leeroy/azbench/diagnostics"
	"github.com/pthm/leeroy/azbench/judge"
)

// Opt-in server integration: a fresh non-GM character, no paid model process.
// Exercises a skill goal, pause, saved report, resume and session restart.
func TestLifecycleAndSkillGoalLive(t *testing.T) {
	dir := os.Getenv("WOWBENCH_LIVE_HARNESS")
	if dir == "" {
		t.Skip("set WOWBENCH_LIVE_HARNESS to a fresh absolute artifact directory; build binaries first")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("absolute artifact directory required")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("fresh artifact directory required")
	}
	repo, _ := filepath.Abs("../..")
	b, err := config.LoadBench(filepath.Join(repo, "bench"))
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%08x", uint32(time.Now().UnixNano()))
	suffix = strings.Map(func(r rune) rune { return rune('a' + strings.IndexRune("0123456789abcdef", r)) }, suffix)
	name, char := "rl-"+suffix, "Life"+suffix
	s := &config.Scenario{Name: "reliability-lifecycle", Goal: "skill:185:75", Minutes: 2, Level: 5, Money: 2000, RequireFresh: true,
		Start: &config.Place{X: -9467, Y: -5, Z: 57, Map: 0, Zone: 12}, Agents: []config.Agent{{Name: name, Char: char, Race: "human", Class: "warrior", Runtime: "none"}}}
	makeRunner := func() *Runner {
		r, err := New(b, s, Options{Repo: repo, BasePort: 8210, Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	waitFor := func(label string, limit time.Duration, condition func() bool) {
		t.Helper()
		end := time.Now().Add(limit)
		for time.Now().Before(end) {
			if condition() {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatal("deadline:", label)
	}
	r := makeRunner()
	done := make(chan error, 1)
	go func() { done <- r.Run(true) }()
	playing := func(r *Runner) bool {
		r.players[0].mu.Lock()
		defer r.players[0].mu.Unlock()
		return r.players[0].state == "playing"
	}
	waitFor("initial session", 90*time.Second, func() bool { return playing(r) })
	// End active processes even when an assertion fails.
	active := r
	t.Cleanup(func() {
		if active != nil && active.cancel != nil {
			active.pause()
			active.stopAll()
		}
	})
	preflight, err := os.ReadFile(filepath.Join(dir, "preflight.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = eval(context.Background(), 8210, `life=nil;task.spawn("skill-fixture",function()
  local r=act.train{npc_entry=1430,spells={2551}};life={action=r,skill=skills.get(185)}
 end)`)
	if err != nil {
		t.Fatal(err)
	}
	var skill struct {
		Action struct {
			OK    bool `json:"ok"`
			Error any  `json:"error"`
		} `json:"action"`
		Skill struct {
			Value int `json:"value"`
		} `json:"skill"`
	}
	waitFor("learned skill", 30*time.Second, func() bool {
		raw, err := eval(context.Background(), 8210, "return life")
		return err == nil && string(raw) != "null" && len(raw) > 0 && json.Unmarshal(raw, &skill) == nil
	})
	if !skill.Action.OK || skill.Skill.Value < 1 {
		t.Fatalf("training: %+v", skill)
	}
	r.pause()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("pause did not stop run")
	}
	active = nil
	judge.Report(dir)
	report := func(state string, provisional bool) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, "report.json"))
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Agents []struct {
				Provisional bool   `json:"score_provisional"`
				JudgeState  string `json:"judge_state"`
			} `json:"agents"`
			Team struct {
				State string `json:"state"`
			} `json:"team"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		if len(v.Agents) != 1 || v.Agents[0].Provisional != provisional || v.Team.State != state {
			t.Fatalf("report state: %+v", v)
		}
	}
	report("paused", true)
	raw, _ := os.ReadFile(filepath.Join(dir, "report.json"))
	os.WriteFile(filepath.Join(dir, "paused-report.json"), raw, 0644)
	var savedRank int
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = chars().QueryRowContext(ctx, `SELECT s.value FROM character_skills s JOIN characters c ON c.guid=s.guid WHERE c.name=? AND s.skill=185`, char).Scan(&savedRank)
	if err != nil || savedRank != skill.Skill.Value {
		t.Fatalf("SDK rank=%d, saved rank=%d: %v", skill.Skill.Value, savedRank, err)
	}
	r = makeRunner()
	active = r
	done = make(chan error, 1)
	go func() { done <- r.run(true, "lifecycle acceptance", true) }()
	waitFor("resumed session", 90*time.Second, func() bool { return playing(r) })
	p := r.players[0]
	p.mu.Lock()
	first := p.session.Process.Pid
	p.mu.Unlock()
	if err := r.restart(name, "lifecycle acceptance restart"); err != nil {
		t.Fatal(err)
	}
	waitFor("restarted session", 60*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.session != nil && p.session.Process.Pid != first && alive(p.session)
	})
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Minute):
		t.Fatal("resumed run did not complete")
	}
	active = nil
	after, err := os.ReadFile(filepath.Join(dir, "preflight.json"))
	if err != nil || !bytes.Equal(preflight, after) {
		t.Fatal("resume overwrote initial preflight")
	}
	judge.Report(dir)
	report("completed", false)
	d := diagnostics.Read(name, diagnostics.Paths(dir, name)...)
	if len(d.Coverage.Sessions) < 3 || d.Reliability.DeadMS == nil || len(d.Coverage.Gaps) > 0 {
		t.Fatalf("lifecycle evidence: %+v", d.Coverage)
	}
	t.Logf("saved rank=%d; %d session streams; artifacts %s", savedRank, len(d.Coverage.Sessions), dir)
}
