package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pthm/leeroy/azsdk/types"
	"github.com/pthm/leeroy/internal/bench/config"
	"github.com/pthm/leeroy/runfiles"
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
	alerts, _ := os.ReadFile(runfiles.Run(dir).SupervisorLog())
	if !strings.Contains(string(alerts), "ALERT") || !strings.Contains(string(alerts), "session exited (code 3") {
		t.Fatalf("supervisor.log: %s", alerts)
	}
	ev := harnessEvents(t, runfiles.Run(dir).Diagnostics("a1"))
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
	os.MkdirAll(runfiles.Run(dir).Live(""), 0o755)
	os.WriteFile(runfiles.Run(dir).Live("pids"), []byte(pids), 0o644)
	control := func(pid int) {
		os.WriteFile(runfiles.Run(dir).Live("control.json"), fmt.Appendf(nil, `{"addr":"127.0.0.1:1","pid":%d}`, pid), 0o644)
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
	b, _ := os.ReadFile(runfiles.Run(dir).Live("pids"))
	if string(b) != fmt.Sprintf("%d sleep\n", p.session.Process.Pid) {
		t.Fatalf("pids: %q", b)
	}
	p.session = nil
	r.savePids()
	if b, _ := os.ReadFile(runfiles.Run(dir).Live("pids")); len(b) != 0 {
		t.Fatalf("pids after stop: %q", b)
	}
}

// stopAgent during a tool call (sleep 60): the tool stops first and the runtime records its output before it's
// interrupted, well inside the 5 s it would get before a SIGTERM.
func TestStopAgentKeepsToolOutput(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{repo: dir, dir: dir}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	defer r.cancel()
	p := &player{r: r, a: config.Agent{Name: "a1", Char: "Aone", Runtime: "codex"}, dir: dir, ws: dir}
	r.players = []*player{p}
	agentCommand = func(*player, string, string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAgentHelper$")
		cmd.Env = append(os.Environ(), "AZBENCH_AGENT_HELPER=1")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		return cmd
	}
	defer func() { agentCommand = (*player).agentCmd }()
	if err := p.startAgent("go", "", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	cmd, tools := p.agent, p.tools
	p.mu.Unlock()
	for end := time.Now().Add(10 * time.Second); !tools.busy() || len(toolProcs(cmd.Process.Pid)) == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the tool call didn't start")
		}
	}
	start := time.Now()
	p.stopAgent()
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("stop took %v", took)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "transcript.jsonl"))
	completed, interrupted := strings.Index(string(b), `"item.completed"`), strings.Index(string(b), `"turn.interrupted"`)
	if completed < 0 || interrupted < completed {
		t.Fatalf("transcript:\n%s", b)
	}
}

// TestAgentHelper is the runtime for TestStopAgentKeepsToolOutput: one tool call (sleep 60), its output, then it
// waits for an interrupt.
func TestAgentHelper(t *testing.T) {
	if os.Getenv("AZBENCH_AGENT_HELPER") != "1" {
		return
	}
	ints := make(chan os.Signal, 1)
	signal.Notify(ints, os.Interrupt)
	fmt.Println(`{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"sleep 60"}}`)
	sleep := exec.Command("sleep", "60")
	sleep.Run()
	fmt.Printf(`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","exit_code":%d,"status":"failed"}}`+"\n", sleep.ProcessState.ExitCode())
	<-ints
	fmt.Println(`{"type":"turn.interrupted"}`)
	os.Exit(0)
}

// the goal's wrap-up: a goal_met event to the session, the agent's stop moved past the run's clock, and it returns
// after the wrap-up (or at a pause)
func TestWrapUp(t *testing.T) {
	dir := t.TempDir()
	var got struct {
		Method string
		Params struct {
			Type string
			Data types.GoalMet
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		json.NewDecoder(req.Body).Decode(&got)
		fmt.Fprintln(w, `{"jsonrpc":"2.0","id":1,"result":7}`)
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	r := &Runner{dir: dir, s: &config.Scenario{Conditions: config.Conditions{Goal: "level:2"}}}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	defer r.cancel()
	p := &player{r: r, a: config.Agent{Name: "a1", Char: "Aone"}, dir: dir, ws: dir, port: port}
	fired := make(chan time.Time, 1)
	p.agentEnd = time.Now().Add(50 * time.Millisecond)
	p.agentStop = time.AfterFunc(50*time.Millisecond, func() { fired <- time.Now() })

	start := time.Now()
	r.wrapUp(p, 300*time.Millisecond)
	if took := time.Since(start); took < 300*time.Millisecond {
		t.Fatalf("wrap-up took %v", took)
	}
	if got.Method != "events.harness" || got.Params.Type != "goal_met" || got.Params.Data.Goal != "level:2" ||
		got.Params.Data.EndsAt < start.Add(300*time.Millisecond).UnixMilli() {
		t.Fatalf("notify: %+v", got)
	}
	select {
	case at := <-fired:
		if at.Sub(start) < 250*time.Millisecond {
			t.Fatalf("the agent's stop fired %v into the wrap-up", at.Sub(start))
		}
	case <-time.After(time.Second):
		t.Fatal("the agent's stop never fired")
	}
	if ev := harnessEvents(t, runfiles.Run(dir).Diagnostics("a1")); len(ev) != 1 || ev[0].Details.State != types.StateWrapUp {
		t.Fatalf("harness events: %+v", ev)
	}
	r.cancel() // a pause ends it at once
	start = time.Now()
	r.wrapUp(p, time.Minute)
	if took := time.Since(start); took > time.Second {
		t.Fatalf("wrap-up after a pause took %v", took)
	}
}

// at a stop the agent's notes are copied into the run; the refreshed docs and the run's prompt.md aren't
func TestCopyNotes(t *testing.T) {
	ws, dir := t.TempDir(), t.TempDir()
	for _, f := range []string{"NOTES.md", "RUN_STATE.md", "AGENTS.md", "CLAUDE.md", "game.md", "prompt.md", "main.lua"} {
		os.WriteFile(filepath.Join(ws, f), []byte(f), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "prompt.md"), []byte("the run's prompt"), 0o644)
	(&player{ws: ws, dir: dir}).copyNotes()
	got, _ := filepath.Glob(filepath.Join(dir, "*"))
	for i := range got {
		got[i] = filepath.Base(got[i])
	}
	if strings.Join(got, " ") != "NOTES.md RUN_STATE.md prompt.md" {
		t.Fatalf("copied: %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "prompt.md")); string(b) != "the run's prompt" {
		t.Fatalf("prompt.md: %s", b)
	}
}

// azbench maintenance: each session that's up gets a maintenance event with when it takes effect; a message is
// required
func TestMaintenance(t *testing.T) {
	var got struct {
		Method string
		Params struct {
			Type string
			Data types.Maintenance
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		json.NewDecoder(req.Body).Decode(&got)
		fmt.Fprintln(w, `{"jsonrpc":"2.0","id":1,"result":3}`)
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	dir := t.TempDir()
	r := &Runner{dir: dir, ctx: context.Background()}
	up := &player{r: r, port: port, session: exec.Command("sleep", "60")}
	up.session.Start()
	defer up.session.Process.Kill()
	r.players = []*player{up, {r: r, port: 1}} // the second has no session

	if _, err := r.maintenance(time.Now(), " "); err == nil {
		t.Fatal("no message accepted")
	}
	at := time.Now().Add(10 * time.Minute)
	n, err := r.maintenance(at, "the run pauses for a deploy")
	if err != nil || n != 1 {
		t.Fatalf("sent %d: %v", n, err)
	}
	if got.Method != "events.harness" || got.Params.Type != "maintenance" || got.Params.Data.At != at.UnixMilli() ||
		got.Params.Data.Message != "the run pauses for a deploy" {
		t.Fatalf("notify: %+v", got)
	}
}
