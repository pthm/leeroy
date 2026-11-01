package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/pthm/leeroy/azbench/diagnostics"
	"github.com/pthm/leeroy/azeroth-sdk/types"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// serveControl serves the run's control API on a free localhost port, written to control.json. It returns a stop
// function.
func (r *Runner) serveControl() func() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) { json.NewEncoder(w).Encode(r.status()) })
	mux.HandleFunc("POST /pause", func(w http.ResponseWriter, _ *http.Request) {
		r.logf("pausing (azbench pause)")
		go r.pause()
		fmt.Fprintln(w, `{"ok":true}`)
	})
	mux.HandleFunc("POST /restart", func(w http.ResponseWriter, req *http.Request) {
		var q struct{ Agent, Message string }
		json.NewDecoder(req.Body).Decode(&q)
		if err := r.restart(q.Agent, q.Message); err != nil {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		fmt.Fprintln(w, `{"ok":true}`)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		r.logf("no control API: %v", err)
		return func() {}
	}
	b, _ := json.Marshal(map[string]any{"addr": ln.Addr().String(), "pid": os.Getpid()})
	os.WriteFile(filepath.Join(r.dir, "control.json"), b, 0o644)
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	return func() { srv.Close(); os.Remove(filepath.Join(r.dir, "control.json")) }
}

func (r *Runner) status() map[string]any {
	var ps []map[string]any
	for _, p := range r.players {
		p.mu.Lock()
		st := map[string]any{"agent": p.a.Name, "char": p.a.Char, "runtime": p.a.Runtime, "port": p.port, "state": p.state,
			"agent_running": alive(p.agent), "session_running": alive(p.session)}
		p.mu.Unlock()
		diag := diagnostics.Read(p.a.Name, diagnostics.Paths(r.dir, p.a.Name)...)
		st["active_failures"] = diag.TopActive(time.Now().UnixMilli(), 3)
		if b, err := os.ReadFile(filepath.Join(r.dir, "board", p.a.Char+".json")); err == nil {
			var board map[string]any
			if json.Unmarshal(b, &board) == nil {
				st["score"], st["level"] = board["score"], board["level"]
			}
		}
		ps = append(ps, st)
	}
	return map[string]any{"run": r.dir, "state": "running", "scenario": r.s.Name, "goal": r.s.Goal, "minutes": r.s.Minutes,
		"minutes_left": int(r.minutesLeft() + 0.5), "players": ps}
}

// restart brings one agent back mid-run: its session restarts (character calmed, logged out and in again with the
// current SDK) and its conversation resumes with the message ($LEFT: minutes left). Its judge keeps running.
func (r *Runner) restart(name, message string) error {
	var p *player
	for _, x := range r.players {
		if x.a.Name == name {
			p = x
		}
	}
	if p == nil {
		return fmt.Errorf("no agent %q", name)
	}
	p.mu.Lock()
	st := p.state
	p.mu.Unlock()
	if st != types.JudgePlaying {
		return fmt.Errorf("%s is %s", name, st)
	}
	go func() {
		p.logf("restarting")
		p.runtimeState("supervisor", types.StateRestart)
		p.stopAgent()
		p.stopSession()
		if err := p.workspace(); err != nil { // the docs as of this build
			r.alert(p, "restart: "+err.Error())
			return
		}
		if !waitOffline(p.a.Char, 2*time.Minute) {
			r.alert(p, "restart: still online")
			return
		}
		if err := p.login(r.ctx); err != nil {
			r.alert(p, "restart: "+err.Error())
			return
		}
		left := time.Duration(r.minutesLeft() * float64(time.Minute))
		msg := replaceLeft(message, left) + p.changes()
		if err := p.startAgent(msg, p.conversation(), time.Now().Add(left)); err != nil {
			r.alert(p, "restart: "+err.Error())
			return
		}
		p.logf("back, %.0f min left", left.Minutes())
	}()
	return nil
}

func replaceLeft(msg string, left time.Duration) string {
	return string(bytes.ReplaceAll([]byte(msg), []byte("$LEFT"), []byte(fmt.Sprintf("%.0f", left.Minutes()))))
}

// Control sends a command to a running run's supervisor (control.json in its dir) and returns the reply.
func Control(dir, method, path string, body any) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, "control.json"))
	if err != nil {
		return nil, fmt.Errorf("%s isn't running (no control.json)", dir)
	}
	var c struct {
		Addr string
		Pid  int
	}
	json.Unmarshal(b, &c)
	if !pidAlive(c.Pid) {
		return nil, fmt.Errorf("%s: supervisor crashed (pid %d is gone); %d of its processes still run; azbench resume stops them", dir, c.Pid, len(leftovers(dir)))
	}
	j, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, "http://"+c.Addr+path, bytes.NewReader(j))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: supervisor not answering: %v", dir, err)
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	out.ReadFrom(resp.Body)
	if resp.StatusCode != 200 {
		return out.Bytes(), fmt.Errorf("%s", bytes.TrimSpace(out.Bytes()))
	}
	return out.Bytes(), nil
}

// Status is a run's status from its supervisor, or state "crashed" when control.json's pid is gone: the supervisor
// died without its cleanup, and its agents and sessions (left_running) may still be playing, unjudged.
func Status(dir string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, "control.json"))
	var c struct{ Pid int }
	if err == nil && json.Unmarshal(b, &c) == nil && !pidAlive(c.Pid) {
		out, _ := json.Marshal(map[string]any{"run": dir, "state": "crashed", "pid": c.Pid, "left_running": len(leftovers(dir))})
		return append(out, '\n'), nil
	}
	return Control(dir, "GET", "/status", nil)
}

// Running is an error while a live supervisor holds the run (control.json's pid exists).
// ponytail: a reused pid reads as alive; the message names it for the operator to check.
func Running(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "control.json"))
	var c struct{ Pid int }
	if err == nil && json.Unmarshal(b, &c) == nil && pidAlive(c.Pid) {
		return fmt.Errorf("%s: its supervisor (pid %d) is still running; azbench pause it first", dir, c.Pid)
	}
	return nil
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// savePids writes <run>/pids: the process group of each session and agent this supervisor started and hasn't
// stopped, with its program's name, so a resume after a crash can stop what it left behind.
func (r *Runner) savePids() {
	r.pidsMu.Lock()
	defer r.pidsMu.Unlock()
	var b strings.Builder
	for _, p := range r.players {
		p.mu.Lock()
		for _, c := range []*exec.Cmd{p.session, p.agent} {
			if c != nil && c.Process != nil {
				fmt.Fprintf(&b, "%d %s\n", c.Process.Pid, filepath.Base(c.Path))
			}
		}
		p.mu.Unlock()
	}
	os.WriteFile(filepath.Join(r.dir, "pids"), []byte(b.String()), 0o644)
}

// leftovers: the process groups in <run>/pids that still run, each only if its leader's command line still names
// the program recorded for it (pids are reused).
func leftovers(dir string) []int {
	b, _ := os.ReadFile(filepath.Join(dir, "pids"))
	var pids []int
	for _, line := range strings.Split(string(b), "\n") {
		var pid int
		var name string
		if n, _ := fmt.Sscan(line, &pid, &name); n != 2 {
			continue
		}
		out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
		if err == nil && strings.Contains(string(out), name) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// stopLeftovers stops the process groups a crashed supervisor left running: SIGTERM (a session logs out, ~20 s),
// then SIGKILL after 30 s. It returns how many there were.
func stopLeftovers(dir string) int {
	pids := leftovers(dir)
	for _, pid := range pids {
		syscall.Kill(-pid, syscall.SIGTERM)
	}
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if !slices.ContainsFunc(pids, pidAlive) {
			return len(pids)
		}
	}
	for _, pid := range pids {
		if pidAlive(pid) {
			syscall.Kill(-pid, syscall.SIGKILL)
		}
	}
	return len(pids)
}
