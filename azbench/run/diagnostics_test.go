package run

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pthm/leeroy/azbench/config"
	"github.com/pthm/leeroy/azbench/diagnostics"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func harnessEvents(t *testing.T, path string) []types.Diagnostic {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []types.Diagnostic
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e types.Diagnostic
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHarnessResumeAndTornRecord(t *testing.T) {
	dir := t.TempDir()
	p := &player{a: config.Agent{Name: "a1", Runtime: "codex"}, dir: dir}
	p.setState("playing")
	p.setState("playing")
	p.setState("paused")
	path := filepath.Join(dir, "harness.jsonl")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(`{"schema":1,"seq":3`)
	f.Close()
	resumed := &player{a: p.a, dir: dir}
	resumed.runtimeState("supervisor", "resume")
	resumed.setState("playing")
	events := harnessEvents(t, path)
	if len(events) != 4 {
		t.Fatalf("events: %+v", events)
	}
	for i, e := range events {
		if e.Seq != uint64(i+1) || e.Agent != "a1" || e.Session != "" {
			t.Fatalf("identity: %+v", e)
		}
	}
	s := diagnostics.Read("a1", path)
	if s.Coverage.MalformedLines != 1 || s.Coverage.Status != "partial" {
		t.Fatalf("torn coverage: %+v", s.Coverage)
	}
}

func TestAgentExitDrainsTranscriptBeforeSnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"test-123\"}' '{\"type\":\"turn.completed\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	p := &player{a: config.Agent{Name: "a1", Runtime: "codex"}, dir: dir, ws: dir, r: &Runner{repo: dir, dir: dir}}
	if err := p.startAgent("test prompt", "", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.agentDone:
	case <-time.After(5 * time.Second):
		t.Fatal("agent completion not signaled")
	}
	b, err := os.ReadFile(filepath.Join(dir, "agent.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var v map[string]any
		if json.Unmarshal(sc.Bytes(), &v) != nil || v["timestamp"] == nil {
			t.Fatalf("bad transcript line: %s", sc.Bytes())
		}
		lines++
	}
	if lines != 2 {
		t.Fatalf("incomplete transcript: %s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "usage.json")); err != nil {
		t.Fatal("usage snapshot missing:", err)
	}
	events := harnessEvents(t, filepath.Join(dir, "harness.jsonl"))
	if len(events) != 2 || events[1].Details.State != "stop" || events[1].Details.ExitCode == nil || *events[1].Details.ExitCode != 0 {
		t.Fatalf("lifecycle: %+v", events)
	}
}
