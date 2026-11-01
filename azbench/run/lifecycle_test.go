package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pthm/leeroy/azbench/config"
)

// A session that exits by itself pauses the run; one the supervisor stopped (no longer p.session) doesn't.
func TestSessionExitPausesRun(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{repo: dir, dir: dir}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	p := &player{r: r, a: config.Agent{Name: "a1", Char: "Aone"}, dir: dir, ws: dir}
	r.players = []*player{p}
	exit := func() *exec.Cmd {
		cmd := exec.Command("sh", "-c", "exit 3")
		cmd.Run()
		return cmd
	}

	p.sessionExited(exit()) // not p.session: stopped by the supervisor
	if r.paused || r.ctx.Err() != nil {
		t.Fatal("a stopped session paused the run")
	}
	p.session = exit()
	p.sessionExited(p.session)
	if !r.paused || r.ctx.Err() == nil {
		t.Fatal("an exited session didn't pause the run")
	}
	alerts, _ := os.ReadFile(filepath.Join(dir, "alerts.log"))
	if !strings.Contains(string(alerts), "session exited (code 3") {
		t.Fatalf("alerts.log: %s", alerts)
	}
	ev := harnessEvents(t, filepath.Join(dir, "harness.jsonl"))
	if len(ev) != 2 || ev[0].Details.Component != "session" || *ev[0].Details.ExitCode != 3 ||
		ev[1].Details.Component != "supervisor" || ev[1].Details.State != "pause" {
		t.Fatalf("harness events: %+v", ev)
	}
}

// control.json with a dead pid: status says crashed and counts what the supervisor left running; resume (Running)
// refuses while the pid lives; stopLeftovers stops the recorded process groups that still run that program.
func TestCrashedSupervisor(t *testing.T) {
	dir := t.TempDir()
	dead := exec.Command("true")
	dead.Run()
	left := exec.Command("sleep", "60")
	left.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := left.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { left.Wait(); close(exited) }()
	defer left.Process.Kill()
	// a reused pid (this test, not "azsdk") is not a leftover
	pids := fmt.Sprintf("%d sleep\n%d azsdk\n", left.Process.Pid, os.Getpid())
	os.WriteFile(filepath.Join(dir, "pids"), []byte(pids), 0o644)
	control := func(pid int) {
		os.WriteFile(filepath.Join(dir, "control.json"), fmt.Appendf(nil, `{"addr":"127.0.0.1:1","pid":%d}`, pid), 0o644)
	}

	control(os.Getpid())
	if err := Running(dir); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("Running with a live supervisor: %v", err)
	}
	control(dead.Process.Pid)
	if err := Running(dir); err != nil {
		t.Fatal(err)
	}
	out, err := Status(dir)
	var st struct {
		State       string
		LeftRunning int `json:"left_running"`
	}
	if err != nil || json.Unmarshal(out, &st) != nil || st.State != "crashed" || st.LeftRunning != 1 {
		t.Fatalf("status: %s %v", out, err)
	}
	if _, err := Control(dir, "POST", "/pause", nil); err == nil || !strings.Contains(err.Error(), "crashed") {
		t.Fatalf("control: %v", err)
	}
	stopLeftovers(dir)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("leftover still running")
	}
}

// the pids file lists the sessions and agents a supervisor runs, and is emptied as they stop
func TestSavePids(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{dir: dir}
	p := &player{r: r, session: exec.Command("sleep", "60")}
	r.players = []*player{p}
	p.session.Start()
	defer p.session.Process.Kill()
	r.savePids()
	b, _ := os.ReadFile(filepath.Join(dir, "pids"))
	if string(b) != fmt.Sprintf("%d sleep\n", p.session.Process.Pid) {
		t.Fatalf("pids: %q", b)
	}
	p.session = nil
	r.savePids()
	if b, _ := os.ReadFile(filepath.Join(dir, "pids")); len(b) != 0 {
		t.Fatalf("pids after stop: %q", b)
	}
}
