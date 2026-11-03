// Package run is the benchmark's supervisor: one process per run owns every character's session, judge and agent,
// starts them on one clock, watches them, and stops them cleanly (agents first, characters out of combat, then a
// logout). It serves a small control API on localhost (control.json in the run dir) for pause, restart and status.
package run

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pthm/leeroy/internal/bench/monitor"

	"github.com/goccy/go-yaml"
	"github.com/pthm/leeroy/azsdk/types"
	"github.com/pthm/leeroy/internal/bench/config"
	"github.com/pthm/leeroy/internal/bench/judge"
	"github.com/pthm/leeroy/runfiles"
)

// Runner supervises one run.
type Runner struct {
	repo    string
	b       *config.Bench
	s       *config.Scenario
	dir     string // runs/<time>-<scenario>
	monitor time.Duration
	players []*player
	ctx     context.Context
	cancel  context.CancelFunc
	start   time.Time     // the run's clock (go)
	elapsed time.Duration // a resumed run: how much of it was played before
	paused  bool
	mu      sync.Mutex
	pidsMu  sync.Mutex // the pids file
	manMu   sync.Mutex // run.json
}

// Options for a run.
type Options struct {
	Repo     string
	BasePort int  // default 8090
	DryRun   bool // print the plan and the first prompt, start nothing
	Dir      string
	Monitor  time.Duration // sample the machine this often into server/resources.jsonl (0: don't)
}

// New sets a run up (without starting anything).
func New(b *config.Bench, s *config.Scenario, o Options) (*Runner, error) {
	if o.BasePort == 0 {
		o.BasePort = 8090
	}
	r := &Runner{repo: o.Repo, b: b, s: s, dir: o.Dir, monitor: o.Monitor}
	if r.dir == "" {
		r.dir = filepath.Join(o.Repo, "var", "runs", time.Now().Format("20060102-150405")+"-"+s.Name)
	}
	for i, a := range s.Agents {
		race, _ := config.ID(config.Races, a.Race)
		class, _ := config.ID(config.Classes, a.Class)
		r.players = append(r.players, &player{
			r: r, a: a, race: race, class: class, port: o.BasePort + i,
			ws: filepath.Join(o.Repo, "var", "workspaces", a.Name), dir: runfiles.Run(r.dir).Agent(a.Name), state: "starting",
		})
	}
	return r, nil
}

// f is the run's layout.
func (r *Runner) f() runfiles.Run { return runfiles.Run(r.dir) }

// Dir is the run's directory.
func (r *Runner) Dir() string { return r.dir }

// Paused reports whether the run ended paused (azbench pause) rather than finished.
func (r *Runner) Paused() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paused
}

// logf: a line in supervisor.log, and on stderr.
func (r *Runner) logf(f string, a ...any) {
	line := fmt.Sprintf("%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(f, a...))
	_, _ = os.Stderr.WriteString(line[11:]) // the terminal: the time of day
	if fh, err := os.OpenFile(r.f().SupervisorLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_, _ = fh.WriteString(line) // nowhere left to report a failure
		_ = fh.Close()
	}
}

// alert: something wrong or worth a look, ALERT in supervisor.log; p nil for the run as a whole.
func (r *Runner) alert(p *player, msg string) {
	if p != nil {
		r.logf("ALERT %s (%s): %s", p.a.Name, p.a.Char, msg)
		return
	}
	r.logf("ALERT run: %s", msg)
}

// manifest updates run.json under the lock: fn changes it.
func (r *Runner) manifest(fn func(*runfiles.Manifest)) {
	r.manMu.Lock()
	defer r.manMu.Unlock()
	m, err := r.f().ReadManifest()
	if err != nil {
		m = runfiles.Manifest{Task: r.s.Task, Scenario: r.s.Name, Goal: r.s.Goal, Hardcore: r.s.Hardcore, Status: "starting"}
		for _, p := range r.players {
			pl := runfiles.Player{
				Agent: p.a.Name, Char: p.a.Char, Race: p.a.Race, Class: p.a.Class, Spec: p.a.Spec,
				Runtime: p.a.Runtime, Model: p.a.Model, Effort: p.a.Effort,
			}
			if p.a.Kit != nil {
				pl.Kit = fmt.Sprintf("level %d", p.a.Kit.Level)
			}
			m.Players = append(m.Players, pl)
		}
	}
	m.Minutes = r.s.Minutes
	fn(&m)
	if err := r.f().WriteManifest(m); err != nil {
		r.logf("run.json: %v", err)
	}
}

// Build builds the tools into bin/ (the judge, viewer, agent tools and the azsdk daemon).
func Build(repo string) error {
	cmd := exec.Command("go", "build", "-o", "bin/", "./azsdk/cmd/...", "./cmd/...") // as `just build`
	cmd.Dir, cmd.Stdout, cmd.Stderr = repo, os.Stderr, os.Stderr
	return cmd.Run()
}

// minutesLeft on the run's clock (pauses don't count).
func (r *Runner) minutesLeft() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.start.IsZero() {
		return float64(r.s.Minutes)
	}
	return max(0, float64(r.s.Minutes)-(r.elapsed+time.Since(r.start)).Minutes())
}

// Run starts every player, waits for them all (up to 5 minutes), starts the clock, and supervises until every
// judge is done or the run is paused (SIGINT/SIGTERM or azbench pause). At the end: report.json and team.json.
func (r *Runner) Run(quiet bool) error {
	return r.run(false, "", quiet)
}

// Resume continues a paused run: each character logs back in, its judge continues from its samples (same
// baseline and curve; the pause doesn't count), and its agent's conversation is resumed with the message ($LEFT in
// it: the minutes left).
func (r *Runner) Resume(message string) error {
	return r.run(true, message, false)
}

func (r *Runner) run(resume bool, message string, quiet bool) error {
	if resume {
		if err := Running(r.dir); err != nil {
			return err
		}
		if n := stopLeftovers(r.dir); n > 0 { // after a crash: what its supervisor left running
			r.logf("stopped %d processes a crashed supervisor left running", n)
		}
	}
	for _, p := range r.players {
		if r.s.RequireFresh && !resume {
			if _, err := os.Stat(p.ws); err == nil {
				return fmt.Errorf("%s: workspace %s exists: this scenario is for fresh agents", p.a.Name, p.ws)
			}
		}
		if err := validateAgentCredentials(p.a); err != nil {
			return err
		}
	}
	if err := grantReader(); err != nil {
		return fmt.Errorf("database (is the server up?): %v", err)
	}
	for _, d := range []string{r.f().Board(), r.f().Live(""), filepath.Dir(r.f().Diagnostics("x"))} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	for _, p := range r.players {
		for _, d := range []string{p.dir, r.f().Game(p.a.Char), r.f().Judge(p.a.Char)} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
	}
	// the scenario as run (the notice inlined), for resume and the record; again on a resume, which may have
	// changed its length (resume --minutes)
	saved := *r.s
	if saved.Notice, _ = r.s.NoticeText(); true {
		y, _ := yaml.Marshal(saved)
		if err := os.WriteFile(r.f().Scenario(), y, 0o644); err != nil {
			return err
		}
	}
	if !resume {
		// the benchmark version (suite/versions.yaml) and the SDK revision the run starts on, for the results
		r.manifest(func(m *runfiles.Manifest) {
			if b, err := config.LoadBench(filepath.Join(r.repo, "suite")); err == nil {
				m.Version = b.Current().Version
			}
			m.SDKRev, _ = sdkRev(r.repo)
			m.Host = host()
		})
		if err := SavePreflight(r.f().Preflight(), CheckScenario(r.repo, r.s)); err != nil {
			return fmt.Errorf("save scenario preflight: %w", err)
		}
	}
	codexAlerted := 0.0
	if r.usesCodex() {
		if q := r.noteCodexQuota(); q != nil {
			if msg, pause := codexQuotaAlert(q, &codexAlerted); pause {
				return fmt.Errorf("%s: not starting", msg)
			} else if msg != "" {
				r.alert(nil, msg)
			}
		}
	}
	if resume {
		r.elapsed = r.played()
		r.logf("resuming %s: %.0f of %d minutes played", r.dir, r.elapsed.Minutes(), r.s.Minutes)
		if r.elapsed >= time.Duration(r.s.Minutes)*time.Minute {
			return fmt.Errorf("nothing left to play")
		}
	}
	for _, p := range r.players {
		state := types.StateStart
		if resume {
			state = types.StateResume
		}
		p.runtimeState("supervisor", state)
	}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	defer r.cancel()
	if r.monitor > 0 { // for the whole of this supervisor's life: setup, play and the wrap-up
		mctx, stop := context.WithCancel(context.Background())
		defer stop()
		go func() {
			if err := monitor.Run(mctx, r.f().Resources(), r.monitor); err != nil {
				r.logf("monitor: %v", err)
			}
		}()
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		if _, ok := <-sig; ok {
			r.logf("pausing (signal)")
			r.pause()
		}
	}()
	stopControl := r.serveControl()
	defer stopControl()
	if r.usesCodex() {
		go r.watchCodexQuota(codexAlerted)
	}
	r.logf("run %s: %d players, %s, %d minutes", r.dir, len(r.players), r.s.Goal, r.s.Minutes)

	// every session up first (logins staggered: concurrent ones time out on the auth server), then one clock
	var wg sync.WaitGroup
	for i, p := range r.players {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if resume {
				if err = p.workspace(); err == nil { // the docs as of this build
					if !waitOffline(p.a.Char) {
						err = fmt.Errorf("still online")
					} else {
						err = p.login(r.ctx)
					}
				}
			} else {
				err = p.prepare(r.ctx)
			}
			if err != nil {
				p.logf("not started: %v", err)
				p.setState(types.JudgeFailed)
				return
			}
			p.setState(types.JudgeReady)
			p.logf("session up on port %d", p.port)
		}()
		if i < len(r.players)-1 {
			select {
			case <-time.After(10 * time.Second):
			case <-r.ctx.Done():
			}
		}
	}
	wg.Wait()
	if r.ctx.Err() != nil {
		r.stopAll()
		return fmt.Errorf("paused before the start")
	}
	r.mu.Lock()
	r.start = time.Now()
	r.mu.Unlock()
	r.manifest(func(m *runfiles.Manifest) {
		m.Status = "running"
		if m.Started == 0 {
			m.Started = r.start.UnixMilli()
		}
	})
	r.noteOpenrouter(false)
	left := time.Duration(r.minutesLeft() * float64(time.Minute))
	r.logf("go: %d/%d sessions up, %.0f minutes", r.count(types.JudgeReady), len(r.players), left.Minutes())

	var judges sync.WaitGroup
	for _, p := range r.players {
		if p.state != types.JudgeReady {
			continue
		}
		judges.Add(1)
		go func() {
			defer judges.Done()
			r.play(p, resume, message, left)
		}()
	}
	judges.Wait()
	if r.usesCodex() {
		r.noteCodexQuota() // end
	}
	r.mu.Lock()
	paused := r.paused
	r.mu.Unlock()
	if paused {
		r.manifest(func(m *runfiles.Manifest) {
			m.Status = "paused"
		})
		r.logf("paused: azbench resume %s --message <file>", r.dir)
		return nil
	}
	r.manifest(func(m *runfiles.Manifest) { m.Status, m.Ended = "finished", time.Now().UnixMilli() })
	if err := os.RemoveAll(r.f().Live("")); err != nil {
		r.logf("live/: %v", err)
	}
	if !quiet {
		r.finish()
	}
	return nil
}

// play runs one player from the start of the clock: its judge, its agent, and the clean stop when the judge is done.
func (r *Runner) play(p *player, resume bool, message string, left time.Duration) {
	spec := r.b.Specs[p.a.Spec]
	o := judge.Options{
		Char: p.a.Char, Goal: r.s.Goal, Timeout: time.Duration(r.s.Minutes) * time.Minute,
		Every: 15 * time.Second, Samples: r.f().Samples(p.a.Char),
		Board:  r.f().Board(),
		Events: r.f().Events(p.a.Char), Role: spec.Role, Runtime: p.a.Runtime,
		Hardcore: r.s.Hardcore, Checkpoints: r.s.Checkpoints, Hold: p.held,
	}
	if resume {
		o.Resume = o.Samples
	}
	type res struct {
		line []byte
		met  bool
		err  error
	}
	done := make(chan res, 1)
	p.setState(types.JudgePlaying)
	go func() {
		line, met, err := judge.Run(r.ctx, o)
		done <- res{line, met, err}
	}()
	end := time.Now().Add(left)
	var err error
	if resume {
		msg := replaceLeft(message, left) + p.restored() + p.changes()
		if conv := p.conversation(); conv != "" || p.a.Runtime == "none" {
			err = p.startAgent(msg, conv, end)
		} else {
			err = fmt.Errorf("no conversation to resume in %s/transcript.jsonl", p.dir)
		}
	} else {
		p.changes() // the revision it starts on
		p.autorun()
		var text string
		if text, err = prompt(r.s, r.b, p.a, r.repo); err == nil {
			if err := os.WriteFile(filepath.Join(p.dir, "prompt.md"), []byte(text), 0o644); err != nil {
				p.logf("prompt.md: %v", err)
			}
			err = p.startAgent(text, "", end)
		}
		go p.meta(spec)
	}
	if err != nil {
		r.alert(p, "agent not started: "+err.Error())
	}
	j := <-done
	if j.err != nil {
		if r.ctx.Err() != nil {
			p.setState(types.JudgePaused)
		} else {
			p.setState(types.JudgeFailed)
		}
		p.stop()
		return
	}
	p.mu.Lock()
	p.result = j.line
	p.mu.Unlock()
	if err := os.WriteFile(r.f().Result(p.a.Char), append(j.line, '\n'), 0o644); err != nil {
		r.alert(p, "result not written: "+err.Error())
	}
	p.setState(types.JudgeDone)
	p.logf("done: %s", j.line)
	var out struct {
		GaveUp string `json:"gave_up"`
	}
	if json.Unmarshal(j.line, &out) == nil && out.GaveUp != "" {
		r.alert(p, "gave up: "+out.GaveUp)
	}
	if j.met {
		r.wrapUp(p, time.Duration(r.s.WrapUpMinutes)*time.Minute)
	}
	p.stop()
	p.saveKit() // after the logout saved the character
}

// wrapUp: the judge recorded p's goal, so its score is final and its run is over: a goal_met event tells the agent,
// and its session ends the run (its scripts stop and game actions fail run_over; the others play on). The agent keeps
// wrap to look back and write its notes, unless the run pauses first.
func (r *Runner) wrapUp(p *player, wrap time.Duration) {
	end := time.Now().Add(wrap)
	p.extendAgent(end)
	p.runtimeState("supervisor", types.StateWrapUp)
	if err := notify(r.ctx, p.port, "goal_met", types.GoalMet{
		Goal: r.s.Goal, End: true, EndsAt: end.UnixMilli(),
		Message: fmt.Sprintf("The judge recorded your goal; your score is final and your run is over: your scripts have stopped and game actions now fail run_over. You have %.0f minutes to look back and write your notes (NOTES.md).", wrap.Minutes()),
	}); err != nil {
		p.logf("goal_met: %v", err)
	}
	p.logf("goal met: run over for it, stopping in %.0f min", wrap.Minutes())
	select {
	case <-time.After(wrap):
	case <-r.ctx.Done():
	}
}

// played: how much of a paused run was played, from its samples (the longest).
func (r *Runner) played() time.Duration {
	var most float64
	for _, p := range r.players {
		b, _ := os.ReadFile(r.f().Samples(p.a.Char))
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		var s struct {
			ElapsedS float64 `json:"elapsed_s"`
		}
		if json.Unmarshal([]byte(lines[len(lines)-1]), &s) == nil {
			most = max(most, s.ElapsedS)
		}
	}
	return time.Duration(most * float64(time.Second))
}

func (r *Runner) count(state string) (n int) {
	for _, p := range r.players {
		p.mu.Lock()
		if p.state == state {
			n++
		}
		p.mu.Unlock()
	}
	return n
}

// pause stops the run where it is: the judges stop (their samples are the resume point), each agent is stopped,
// its character calmed out of combat and logged out.
func (r *Runner) pause() {
	r.mu.Lock()
	already := r.paused
	r.paused = true
	r.mu.Unlock()
	if already { // a signal, azbench pause and exited sessions can all ask
		return
	}
	for _, p := range r.players {
		p.runtimeState("supervisor", types.StatePause)
	}
	r.cancel()
}

func (r *Runner) stopAll() {
	var wg sync.WaitGroup
	for _, p := range r.players {
		wg.Add(1)
		go func() { defer wg.Done(); p.stop() }()
	}
	wg.Wait()
}

// finish: the report card and team.json (judge report). bin/azview live --run <dir> shows a finished run.
func (r *Runner) finish() {
	r.noteOpenrouter(true)
	judge.Report(r.dir)
}

// Plan describes the run without starting it (--dry-run): the players and the first agent's prompt.
func (r *Runner) Plan() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s, %d minutes, %d players -> %s\n", r.s.Name, r.s.Goal, r.s.Minutes, len(r.players), r.dir)
	ps := append([]*player(nil), r.players...)
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].port < ps[j].port })
	for _, p := range ps {
		fmt.Fprintf(&b, "  %-6s %-12s %-6s %-12s %-14s %-8s port %d  %s\n", p.a.Name, p.a.Char, config.RaceName(p.race),
			config.ClassName(p.class), p.a.Spec, p.a.Runtime, p.port, cmp.Or(p.a.Model, "default"))
	}
	if len(ps) > 0 {
		text, err := prompt(r.s, r.b, ps[0].a, r.repo)
		if err != nil {
			text = err.Error()
		}
		fmt.Fprintf(&b, "\n--- prompt for %s ---\n%s", ps[0].a.Name, text)
	}
	return b.String()
}

// host is the machine the run is on: cores and memory, and what the remote run says of the server (LEEROY_HOST_*).
func host() runfiles.Host {
	h := runfiles.Host{
		CPUs: runtime.NumCPU(), MemoryGB: monitor.MemoryGB(), Provider: os.Getenv("LEEROY_HOST_PROVIDER"),
		Type: os.Getenv("LEEROY_HOST_TYPE"), Location: os.Getenv("LEEROY_HOST_LOCATION"),
	}
	h.Name, _ = os.Hostname()
	h.PriceHour, _ = strconv.ParseFloat(os.Getenv("LEEROY_HOST_PRICE_HOUR"), 64)
	return h
}
