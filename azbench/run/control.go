package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/pthm/leeroy/azbench/diagnostics"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	return map[string]any{"run": r.dir, "scenario": r.s.Name, "goal": r.s.Goal, "minutes": r.s.Minutes,
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
	if st != "playing" {
		return fmt.Errorf("%s is %s", name, st)
	}
	go func() {
		p.logf("restarting")
		p.runtimeState("supervisor", "restart")
		p.stopAgent()
		p.stopSession()
		if !waitOffline(p.a.Char, 2*time.Minute) {
			r.alert(p, "restart: still online")
			return
		}
		if err := p.login(r.ctx); err != nil {
			r.alert(p, "restart: "+err.Error())
			return
		}
		left := time.Duration(r.minutesLeft() * float64(time.Minute))
		msg := replaceLeft(message, left)
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
	var c struct{ Addr string }
	json.Unmarshal(b, &c)
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
