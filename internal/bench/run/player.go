package run

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	acct "github.com/pthm/leeroy/internal/bench/account"

	"github.com/pthm/leeroy/azsdk/types"
	"github.com/pthm/leeroy/internal/bench/config"
	"github.com/pthm/leeroy/internal/bench/judge"
	"github.com/pthm/leeroy/runfiles"
	"github.com/pthm/leeroy/trajectory"
)

// player is one agent and its character in a run: workspace, session (an azsdk daemon), judge and agent process.
type player struct {
	r           *Runner
	a           config.Agent
	race, class int
	port        int
	ws, dir     string // workspace (var/workspaces/<name>), its files in the run (<run>/agents/<name>)
	version     string // the runtime's (cliVersion), once meta has read it

	mu        sync.Mutex
	state     string // starting, ready, playing, done, paused, failed
	session   *exec.Cmd
	agent     *exec.Cmd
	agentEnd  time.Time     // when its agent must stop: the run's clock
	continues int           // times its agent was told to carry on after stopping early (exited)
	hold      bool          // a provider outage: logged out, its judge's clock held (outage.go)
	result    []byte        // the judge's final line
	agentDone chan struct{} // closed after transcript drain, usage snapshot and exit diagnostics
	agentStop *time.Timer   // stops the agent at agentEnd
	tools     *toolDiagnostics
	diagMu    sync.Mutex
	metaMu    sync.Mutex // meta.json
	diagSeq   uint64
	diagInit  bool
}

func (p *player) setState(s string) {
	p.mu.Lock()
	changed := p.state != s
	p.state = s
	p.mu.Unlock()
	if changed {
		p.runtimeState("judge", s)
	}
}

func (p *player) logf(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s %s (%s): %s\n", time.Now().Format("15:04:05"), p.a.Name, p.a.Char, fmt.Sprintf(f, a...))
}

func (p *player) account() string {
	s := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(p.a.Name, "")
	return s[:min(16, len(s))]
}

// workspaceFiles: what each run refreshes in the workspace, from the repo. AGENTS.md is where the agent starts (Codex
// loads it by itself; CLAUDE.md imports it for Claude Code, so both start from the same text) and points to the rest.
var workspaceFiles = map[string]string{
	"AGENTS.md":           "suite/agent/AGENTS.md",
	"game.md":             "azsdk/docs/game.md",
	"sdk.md":              "azsdk/docs/sdk.md",
	"moving.md":           "azsdk/docs/moving.md",
	"fighting.md":         "azsdk/docs/fighting.md",
	"npcs-and-items.md":   "azsdk/docs/npcs-and-items.md",
	"death-and-travel.md": "azsdk/docs/death-and-travel.md",
	"players.md":          "azsdk/docs/players.md",
	"troubleshooting.md":  "azsdk/docs/troubleshooting.md",
	"azworld.md":          "azsdk/docs/azworld.md",
	"api-reference.md":    "azsdk/docs/api-reference.md",
	"example.lua":         "azsdk/examples/lua/skeleton.lua",
}

// workspace: refreshed each run: the docs, the example script, CLAUDE.md, and NOTES.md if it has none yet.
func (p *player) workspace() error {
	if err := os.MkdirAll(p.ws, 0o755); err != nil {
		return err
	}
	for name, src := range workspaceFiles {
		b, err := os.ReadFile(filepath.Join(p.r.repo, src))
		if err != nil {
			return err
		}
		if name == "AGENTS.md" {
			b = append(b, []byte(shellContract(p.a.Runtime))...)
		}
		if err := os.WriteFile(filepath.Join(p.ws, name), b, 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(p.ws, "CLAUDE.md"), []byte("@AGENTS.md\n"), 0o644); err != nil {
		return err
	}
	notes := filepath.Join(p.ws, "NOTES.md")
	if _, err := os.Stat(notes); err != nil {
		if err := os.WriteFile(notes, []byte("# Notes\n\nYour notebook, kept with your workspace (AGENTS.md, \"NOTES.md\").\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// daemon starts the character's azsdk session; extra flags: -duration for a short login.
func (p *player) daemon(extra ...string) (*exec.Cmd, error) {
	// the session's log: the run's (game/<char>/session.log), and the workspace's, where the agent reads its log() lines
	log, err := os.OpenFile(filepath.Join(p.ws, "session.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	runLog, err := os.OpenFile(p.r.f().SessionLog(p.a.Char), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Close()
		return nil, err
	}
	args := []string{
		"-char", p.a.Char, "-account", p.account(), "-listen", fmt.Sprintf("127.0.0.1:%d", p.port),
		"-agent", p.a.Name,
		"-data", filepath.Join(p.r.repo, "data"), "-workspace", p.ws, "-race", fmt.Sprint(p.race), "-class", fmt.Sprint(p.class),
	}
	if len(extra) == 0 {
		args = append(args, "-events", p.r.f().Events(p.a.Char),
			"-diagnostics", p.r.f().Diagnostics(p.a.Name))
	}
	cmd := exec.Command(filepath.Join(p.r.repo, "bin", "azsdk"), append(args, extra...)...)
	both := io.MultiWriter(log, runLog)
	cmd.Stdout, cmd.Stderr = both, both
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		log.Close()
		runLog.Close()
		return nil, err
	}
	go func() {
		_ = cmd.Wait() // sessionExited looks at how it ended
		_ = log.Close()
		_ = runLog.Close()
		p.sessionExited(cmd)
	}()
	return cmd, nil
}

// sessionExited: a session that ends by itself while the run plays (its world connection lost, the daemon crashed).
// The judge would run the clock on a character nobody controls, so the run pauses: the judges stop at their last
// sample, and an operator resumes it. A session the supervisor stops is no longer p.session when it exits.
func (p *player) sessionExited(cmd *exec.Cmd) {
	p.mu.Lock()
	current, done := p.session == cmd, p.state == types.JudgeDone
	p.mu.Unlock()
	if !current || done || p.r.ctx.Err() != nil { // done: in the goal's wrap-up, its score already final
		return
	}
	code := cmd.ProcessState.ExitCode()
	p.harness(types.DiagnosticDetails{Component: "session", State: types.StateStop, Runtime: p.a.Runtime, ExitCode: &code})
	p.r.alert(p, fmt.Sprintf("session exited (code %d, see %s): pausing the run", code, p.r.f().SessionLog(p.a.Char)))
	p.r.pause()
}

func alive(c *exec.Cmd) bool {
	return c != nil && c.ProcessState == nil && c.Process.Signal(syscall.Signal(0)) == nil
}

// login starts the session and waits for its API, three tries (the auth server sometimes times out the logon).
func (p *player) login(ctx context.Context) error {
	for try := 1; ; try++ {
		cmd, err := p.daemon()
		if err != nil {
			return err
		}
		for alive(cmd) {
			if _, err := eval(ctx, p.port, "return 1"); err == nil {
				p.mu.Lock()
				p.session = cmd
				p.mu.Unlock()
				p.r.savePids()
				_, err := eval(ctx, p.port, fmt.Sprintf("LIB = %q; chat.join(%q, 1)", p.ws+"/", p.r.s.Channel))
				return err
			}
			select {
			case <-ctx.Done():
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) // gone already is fine
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
		p.logf("session login failed (try %d), see %s", try, p.r.f().SessionLog(p.a.Char))
		if try == 3 {
			return fmt.Errorf("session login failed")
		}
		time.Sleep(time.Duration(try*5) * time.Second)
	}
}

// prepare: workspace, account, starting conditions and kit (on the offline character, creating it with a short login
// first), then the session. The player is ready to start when it returns nil.
func (p *player) prepare(ctx context.Context) error {
	if err := p.workspace(); err != nil {
		return err
	}
	if err := acct.Ensure(p.account(), p.account(), 0); err != nil {
		return fmt.Errorf("account: %v", err)
	}
	if err := os.WriteFile(filepath.Join(p.ws, "session.log"), nil, 0o644); err != nil { // the workspace (and old log) persists
		return err
	}
	s := p.r.s
	if s.Level > 0 || s.Money > 0 || s.Start != nil || p.a.Start != nil || len(s.Setup) > 0 || p.a.Kit != nil {
		for try := 1; !charExists(p.a.Char) && try <= 3; try++ { // concurrent logins can time out on the auth server
			if cmd, err := p.daemon("-duration", "8s"); err == nil {
				for alive(cmd) {
					time.Sleep(500 * time.Millisecond)
				}
			}
			if !charExists(p.a.Char) {
				time.Sleep(time.Duration(try*5) * time.Second)
			}
		}
		if !charExists(p.a.Char) {
			return fmt.Errorf("character not created, see %s", p.r.f().SessionLog(p.a.Char))
		}
		if !waitOffline(p.a.Char) {
			return fmt.Errorf("still online")
		}
		if err := applyStart(s, p.a.Char); err != nil {
			return fmt.Errorf("starting conditions: %v", err)
		}
		if p.a.Kit != nil {
			if err := applyKit(p.a.Kit, p.a.Char, p.class); err != nil {
				return err
			}
		}
	} else if !waitOffline(p.a.Char) {
		return fmt.Errorf("still online from an earlier session")
	}
	return p.login(ctx)
}

// agentCmd builds the agent's process: a new conversation with prompt, or (resume != "") its earlier one with a
// message.
func (p *player) agentCmd(text, resume string) *exec.Cmd {
	repo, a := p.r.repo, p.a
	var cmd *exec.Cmd
	switch a.Runtime {
	case "codex":
		// sandboxed to its workspace (writes) with network on (the session API is on localhost)
		common := []string{
			"--json", "--skip-git-repo-check", "-c", "sandbox_workspace_write.network_access=true",
			"-c", `approval_policy="never"`, "-c", "shell_environment_policy.inherit=all",
		}
		if a.Effort != "" {
			common = append(common, "-c", fmt.Sprintf("model_reasoning_effort=%q", a.Effort))
		}
		if resume != "" {
			cmd = exec.Command("codex", append(append([]string{"exec", "resume", resume}, common...),
				"-c", `sandbox_mode="workspace-write"`, text)...)
		} else {
			args := append([]string{"exec"}, common...)
			args = append(args, "-s", "workspace-write")
			if a.Model != "" {
				args = append(args, "-m", a.Model)
			}
			cmd = exec.Command("codex", append(args, "-C", p.ws, text)...)
		}
	case "opencode":
		args := []string{"run", "--pure", "--format", "json", "--print-logs", "--log-level", "ERROR", "-m", a.Model, "--dir", p.ws}
		if resume != "" {
			args = append(args, "--session", resume)
		}
		if a.Effort != "" {
			args = append(args, "--variant", a.Effort)
		}
		cmd = exec.Command("opencode", append(args, text)...)
	default:
		// no auto-memory: it's keyed by the git root, so agents would share notes with each other (and with us). The
		// env var turns it off, but Claude Code still lets sessions write the memory folder, so ~/.claude is denied
		args := []string{"-p", text}
		if resume != "" {
			args = append(args, "--resume", resume)
		}
		args = append(args, "--setting-sources", "project", "--strict-mcp-config", "--output-format", "stream-json", "--verbose")
		if a.Model != "" {
			args = append(args, "--model", a.Model)
		}
		if a.Effort != "" {
			args = append(args, "--effort", a.Effort)
		}
		args = append(args, "--add-dir", repo+"/azsdk", repo+"/server/azerothcore/src/server/game", repo+"/server/azerothcore/src/server/shared",
			"--allowedTools")
		for _, c := range agentCommands {
			args = append(args, "Bash("+c+":*)")
		}
		args = append(args, "Edit(./**)", "--disallowedTools", "Edit(~/.claude/**)", "Read(~/.claude/**)")
		cmd = exec.Command("claude", args...)
	}
	cmd.Dir = p.ws
	env := os.Environ()
	if a.Provider == "openrouter" && a.Runtime == "claude" {
		// OpenRouter's Anthropic-compatible endpoint; the model is its slug (anthropic/claude-opus-5.5), also for
		// Claude Code's background calls, which would otherwise ask for a Haiku id OpenRouter may not route
		env = slices.DeleteFunc(env, func(kv string) bool {
			return strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") || strings.HasPrefix(kv, "CLAUDE_CODE_OAUTH_TOKEN=")
		})
		env = append(env, "ANTHROPIC_BASE_URL=https://openrouter.ai/api", "ANTHROPIC_API_KEY="+os.Getenv("OPENROUTER_API_KEY"),
			"ANTHROPIC_DEFAULT_HAIKU_MODEL="+a.Model)
	}
	if a.Runtime == "opencode" {
		// its own empty config dir, so the user's opencode config (plugins, providers, AGENTS.md) stays out of the run,
		// and its own data, state and cache: opencode keeps its sessions in one SQLite database there, and agents
		// sharing it fail at start with "database is locked" when they start together
		cfg := filepath.Join(p.dir, "opencode")
		if err := os.MkdirAll(cfg, 0o755); err != nil {
			p.logf("opencode config: %v", err)
		}
		// mise keeps its tools and trusted configs in the XDG dirs too: pin it to its own first, or opencode run through
		// a mise shim (the run servers) fails with "config files … are not trusted"
		home, _ := os.UserHomeDir()
		for _, d := range [][3]string{{"MISE_CONFIG_DIR", "XDG_CONFIG_HOME", ".config"}, {"MISE_DATA_DIR", "XDG_DATA_HOME", ".local/share"},
			{"MISE_STATE_DIR", "XDG_STATE_HOME", ".local/state"}} {
			if os.Getenv(d[0]) == "" {
				env = append(env, d[0]+"="+filepath.Join(cmp.Or(os.Getenv(d[1]), filepath.Join(home, d[2])), "mise"))
			}
		}
		env = append(env, "XDG_CONFIG_HOME="+cfg, "XDG_DATA_HOME="+filepath.Join(cfg, "data"), "XDG_STATE_HOME="+filepath.Join(cfg, "state"),
			"XDG_CACHE_HOME="+filepath.Join(cfg, "cache"), "OPENCODE_CONFIG_CONTENT="+opencodeConfig(repo, p.ws))
	}
	env = append(env, "PATH="+filepath.Join(repo, "suite", "agent", "bin")+":"+os.Getenv("PATH"),
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "WOWBENCH_BOARD="+p.r.f().Board(),
		"WOWBENCH_CHAR="+a.Char, fmt.Sprintf("WOW_MAX_WAIT=%d", agentMaxWaitSeconds), fmt.Sprintf("WOW_PORT=%d", p.port))
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // so stopping it stops its tools too
	return cmd
}

// agentCommands: the shell commands an agent may run (suite/agent/AGENTS.md lists them): its three tools and
// read-only file and text commands, so output can be piped through head or grep
var agentCommands = []string{
	"wow", "azworld", "standings", "sleep", "tail", "cat", "ls", "grep", "mkdir", "head", "wc",
	"sort", "uniq", "cut", "rg",
}

// opencodeConfig is the same sandbox as Claude's allowed tools: write only in the workspace, read the SDK and server
// source, and run only the agent's tools and a few read-only commands. "ask" can't be answered in opencode run, so
// everything is allow or deny; doom_loop (the same call three times in a row) is allowed, since wow wait is that.
// No snapshots: they're for undo, and copy the repo's state at every step.
func opencodeConfig(repo, ws string) string {
	bash := map[string]string{"*": "deny"}
	for _, c := range agentCommands {
		bash[c], bash[c+" *"] = "allow", "allow"
	}
	ext := map[string]string{"*": "deny"}
	for _, d := range []string{"azsdk", "server/azerothcore/src/server/game", "server/azerothcore/src/server/shared"} {
		ext[filepath.Join(repo, d)+"/**"] = "allow"
	}
	// edit rules match paths relative to the git root (the repo), not absolute ones
	rel, _ := filepath.Rel(repo, ws)
	b, _ := json.Marshal(map[string]any{"autoupdate": false, "share": "disabled", "snapshot": false, "permission": map[string]any{
		"bash": bash, "external_directory": ext, "edit": map[string]string{"*": "deny", rel + "/**": "allow"},
		"webfetch": "deny", "websearch": "deny", "codesearch": "deny", "doom_loop": "allow",
	}})
	return string(b)
}

// OpenCode also encodes its provider in the model path. Check that form before
// creating accounts or starting sessions, just as for the explicit provider field.
func validateAgentCredentials(a config.Agent) error {
	if (a.Provider == "openrouter" || (a.Runtime == "opencode" && strings.HasPrefix(a.Model, "openrouter/"))) && os.Getenv("OPENROUTER_API_KEY") == "" {
		return fmt.Errorf("%s: provider openrouter needs OPENROUTER_API_KEY; configure it or select an authenticated provider/model", a.Name)
	}
	return nil
}

// startAgent runs the agent until end, appending its output to transcript.jsonl. Codex's JSONL events have no
// timestamps, so each line is stamped as it arrives (the viewer and the report place them on the timeline);
// opencode's have their own, in unix ms, which become the same ISO time.
func (p *player) startAgent(text, resume string, end time.Time) error {
	p.mu.Lock()
	p.agentEnd = end
	p.mu.Unlock()
	if p.a.Runtime == "none" { // no agent: an empty transcript, so the report card still lists the player
		f, err := os.OpenFile(filepath.Join(p.dir, "transcript.jsonl"), os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
		}
		return err
	}
	out, err := os.OpenFile(filepath.Join(p.dir, "transcript.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	cmd := agentCommand(p, text, resume)
	cmd.Stdin = nil
	pr, stamp := io.Pipe()
	cmd.Stdout, cmd.Stderr = stamp, stamp
	drained := make(chan struct{})
	observer := newToolDiagnostics(p.a.Runtime, p.harnessEvent)
	go func() {
		defer close(drained)
		defer func() { _ = pr.Close() }() // a pipe's Close returns nil
		if err := stampLinesObserved(pr, out, observer.observe); err != nil {
			p.harnessEvent(types.Diagnostic{Kind: "runtime_state", Code: "transcript_io", Details: types.DiagnosticDetails{Component: "agent", State: "coverage_gap", Runtime: p.a.Runtime}})
			p.logf("recording agent transcript: %v", err)
		}
	}()
	if err := cmd.Start(); err != nil {
		_ = stamp.Close() // a pipe's Close returns nil
		<-drained
		p.runtimeState("agent", "failed")
		return err
	}
	done := make(chan struct{})
	p.mu.Lock()
	p.agent = cmd
	p.agentDone = done
	p.tools = observer
	p.mu.Unlock()
	p.r.savePids()
	state := types.StateStart
	if resume != "" {
		state = types.StateResume
	}
	p.harness(types.DiagnosticDetails{Component: "agent", State: state, Runtime: p.a.Runtime, ToolCoverage: true})
	stop := time.AfterFunc(time.Until(end), func() {
		p.mu.Lock()
		current := p.agent == cmd
		p.mu.Unlock()
		if current {
			p.stopAgent()
		}
	})
	p.mu.Lock()
	p.agentStop = stop
	p.mu.Unlock()
	go func() {
		defer close(done)
		_ = cmd.Wait() // the exit code is read below
		stop.Stop()
		_ = stamp.Close()
		<-drained
		observer.finish(time.Now().UnixMilli())
		if err := judge.SaveUsage(p.dir); err != nil {
			p.logf("saving usage: %v", err)
		}
		prompt, _ := os.ReadFile(filepath.Join(p.dir, "prompt.md"))
		if err := trajectory.Write(filepath.Join(p.dir, "transcript.jsonl"), filepath.Join(p.dir, "trajectory.json"), trajectory.Info{
			Agent: p.a.Name, Char: p.a.Char, Runtime: p.a.Runtime, Version: p.cliVersionNow(), Model: p.a.Model, Provider: p.a.Provider, Effort: p.a.Effort,
			Prompt: string(prompt), Usage: filepath.Join(p.dir, "usage.json"),
		}); err != nil {
			p.logf("trajectory: %v", err)
		}
		code := cmd.ProcessState.ExitCode()
		p.harness(types.DiagnosticDetails{Component: "agent", State: types.StateStop, Runtime: p.a.Runtime, ExitCode: &code})
		p.exited(cmd)
	}()
	return nil
}

func stampLinesObserved(r io.Reader, w io.WriteCloser, observe func(map[string]any)) (err error) {
	defer func() {
		if cerr := w.Close(); err == nil {
			err = cerr
		}
	}()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		ts := `"timestamp":"` + time.Now().UTC().Format(time.RFC3339Nano) + `"`
		var obj map[string]json.RawMessage
		if len(line) > 1 && line[0] == '{' && json.Unmarshal(line, &obj) == nil {
			if ms, err := strconv.ParseInt(string(obj["timestamp"]), 10, 64); err == nil {
				obj["timestamp"], _ = json.Marshal(time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z"))
				line, _ = json.Marshal(obj)
			} else {
				var stamp string
				_ = json.Unmarshal(obj["timestamp"], &stamp) // not a string: replaced below
				if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
					obj["timestamp"], _ = json.Marshal(time.Now().UTC().Format(time.RFC3339Nano))
				}
				line, _ = json.Marshal(obj)
			}
		} else {
			t, _ := json.Marshal(sc.Text())
			line = []byte(`{"type":"stderr","text":` + string(t) + "," + ts + "}")
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			return err
		}
		decodeToolLine(line, observe)
	}
	return sc.Err()
}

// exited: an agent that stops with 2+ minutes left is reported (an ALERT in supervisor.log), with the runtime's last error (a
// Codex usage limit, say). Without an error, its conversation is resumed with a short message (the minutes left;
// carry on, or give up), up to maxContinues times: Codex ends its job when it ends a reply ("the automation is
// running"), and a script left alone can't adapt, so the agent stays the one playing. Giving up (standings give-up)
// ends the player, so it isn't resumed.
func (p *player) exited(cmd *exec.Cmd) {
	p.mu.Lock()
	left, current, done := time.Until(p.agentEnd), p.agent == cmd, p.state == types.JudgeDone
	p.mu.Unlock()
	if !current || done || left < 2*time.Minute || p.r.ctx.Err() != nil {
		return
	}
	why := ""
	f, _ := os.Open(filepath.Join(p.dir, "transcript.jsonl"))
	if f != nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			var e struct {
				Type    string
				IsError bool `json:"is_error"`
				Message string
				Result  string
				Error   struct {
					Message string
					Data    struct{ Message string } // opencode
				}
			}
			if json.Unmarshal(sc.Bytes(), &e) != nil {
				continue
			}
			switch {
			case e.Type == "turn.failed":
				why = e.Error.Message
			case e.Type == "error":
				why = cmp.Or(e.Message, e.Error.Data.Message)
			case e.Type == "result" && e.IsError:
				why = e.Result
			}
		}
		f.Close()
	}
	msg := fmt.Sprintf("agent exited with %.0f min left", left.Minutes())
	if why != "" {
		msg += ": " + why
	}
	if transientError(why) && p.conversation() != "" { // a provider outage: wait it out with the clock held
		go p.outage(why)
		return
	}
	p.mu.Lock()
	n := p.continues
	resume := why == "" && n < maxContinues
	if resume {
		p.continues++
	}
	end := p.agentEnd
	p.mu.Unlock()
	conv := p.conversation()
	if !resume || conv == "" {
		p.r.alert(p, msg)
		return
	}
	p.r.alert(p, fmt.Sprintf("%s: told to carry on (%d of %d)", msg, n+1, maxContinues))
	go func() { // not in the old agent's goroutine: whatever waits for it to finish needn't wait for this
		time.Sleep(5 * time.Second)
		p.mu.Lock()
		done := p.state == types.JudgeDone
		p.mu.Unlock()
		if done || p.r.ctx.Err() != nil {
			return
		}
		if err := p.startAgent(replaceLeft(continueText, time.Until(end)), conv, end); err != nil {
			p.r.alert(p, "carry on: "+err.Error())
		}
	}()
}

// maxContinues: how many times an agent that stops early is told to carry on (exited)
const maxContinues = 5

// continueText resumes an agent that stopped early ($LEFT: minutes left).
const continueText = `The run isn't over: $LEFT minutes are left. Your character and session are where you left them. Carry on;
if you've concluded you can't get further, standings give-up "<why>" ends your run.`

// meta writes meta.json: who plays this character, for the report, the board and the viewer. The model is what
// the runtime reports once it starts (Claude: its init event; Codex: its session file; opencode: as configured).
func (p *player) meta(spec config.Spec) {
	model := ""
	for i := 0; i < 60 && model == "" && p.a.Runtime != "none" && p.a.Runtime != "opencode"; i++ {
		model = detectModel(p.a.Runtime, filepath.Join(p.dir, "transcript.jsonl"))
		if model == "" {
			time.Sleep(2 * time.Second)
		}
	}
	if model == "" {
		model = p.a.Model
	}
	if model == "" {
		model = "default"
	}
	effort := p.a.Effort
	for i := 0; effort == "" || effort == "default" && p.a.Runtime == "codex" && i < 15; i++ { // Codex writes it just after the model
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		effort = DetectEffort(p.a.Runtime, filepath.Join(p.dir, "transcript.jsonl"))
	}
	version := cliVersion(p.a.Runtime)
	p.mu.Lock()
	p.version = version
	p.mu.Unlock()
	p.updateMeta(func(m map[string]any) { // keeps sdk_rev (changes)
		for k, v := range map[string]any{
			"agent": p.a.Name, "char": p.a.Char, "runtime": p.a.Runtime, "version": version,
			"model": model, "effort": effort, "provider": p.a.Provider, "spec": spec.Title, "role": spec.Role, "goal": p.r.s.Goal, "port": p.port,
		} {
			m[k] = v
		}
	})
	p.r.manifest(func(m *runfiles.Manifest) {
		for i := range m.Players {
			if m.Players[i].Agent == p.a.Name {
				m.Players[i].Version = version
			}
		}
	})
}

var versionRe = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// cliVersionNow: the version meta read, else read now (a stop before meta finished)
func (p *player) cliVersionNow() string {
	p.mu.Lock()
	v := p.version
	p.mu.Unlock()
	if v == "" {
		v = cliVersion(p.a.Runtime)
	}
	return v
}

// cliVersion: the runtime's version, from its --version ("2.1.292 (Claude Code)", "codex-cli 0.165.0", "1.18.22");
// "" for none or unreadable.
func cliVersion(runtime string) string {
	if runtime == "none" || runtime == "" {
		return ""
	}
	out, err := exec.Command(runtime, "--version").Output()
	if err != nil {
		return ""
	}
	return versionRe.FindString(string(out))
}

// DetectEffort is the reasoning effort a runtime used when none was set: Codex writes it in its session file
// ("effort"); Claude Code and opencode don't say, so "default".
func DetectEffort(runtime, transcript string) string {
	if f := codexSession(runtime, transcript); f != "" {
		b, _ := os.ReadFile(f)
		if x := regexp.MustCompile(`"effort":"([^"]*)"`).FindSubmatch(b); x != nil {
			return string(x[1])
		}
	}
	return "default"
}

// codexSession is the session file of a Codex transcript (its thread.started id), or "".
func codexSession(runtime, transcript string) string {
	if runtime != "codex" {
		return ""
	}
	f, err := os.Open(transcript)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for i := 0; i < 5 && sc.Scan(); i++ {
		var e struct {
			Type     string
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Type == "thread.started" && e.ThreadID != "" {
			home, _ := os.UserHomeDir()
			m, _ := filepath.Glob(filepath.Join(home, ".codex/sessions/*/*/*/*"+e.ThreadID+".jsonl"))
			if len(m) > 0 {
				return m[0]
			}
		}
	}
	return ""
}

func detectModel(runtime, transcript string) string {
	if f := codexSession(runtime, transcript); f != "" {
		b, _ := os.ReadFile(f)
		if x := regexp.MustCompile(`"model":"([^"]*)"`).FindSubmatch(b); x != nil {
			return string(x[1])
		}
		return ""
	}
	f, err := os.Open(transcript)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for i := 0; i < 5 && sc.Scan(); i++ {
		var e struct{ Subtype, Model string }
		if json.Unmarshal(sc.Bytes(), &e) == nil && runtime == "claude" && e.Subtype == "init" {
			return e.Model
		}
	}
	return ""
}

// conversation is the agent's earlier conversation to resume: Claude's or opencode's session id or Codex's thread id.
func (p *player) conversation() string {
	f, err := os.Open(filepath.Join(p.dir, "transcript.jsonl"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var e struct {
			Type      string
			SessionID string `json:"session_id"`
			ThreadID  string `json:"thread_id"`
			OpenCode  string `json:"sessionID"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if p.a.Runtime == "opencode" && e.OpenCode != "" {
			return e.OpenCode
		}
		if p.a.Runtime == "claude" && e.SessionID != "" {
			return e.SessionID
		}
		if p.a.Runtime == "codex" && e.Type == "thread.started" {
			return e.ThreadID
		}
	}
	return ""
}

// extendAgent keeps the agent past the run's clock until end (the goal's wrap-up).
func (p *player) extendAgent(end time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if end.After(p.agentEnd) && p.agentStop != nil {
		p.agentEnd = end
		p.agentStop.Reset(time.Until(end))
	}
}

// stop is how a player stops, whatever the reason (the end, a pause, a restart, the goal's wrap-up): its agent
// (stopAgent), its notes copied into the run, its running tasks recorded, then its session.
func (p *player) stop() {
	p.stopAgent()
	p.copyNotes()
	p.saveTasks()
	p.stopSession()
}

// copyNotes copies the notes the agent keeps in its workspace (NOTES.md and the other .md files it wrote) into
// <run>/<agent>/, so the run has them as they were at each stop. prompt.md there is the run's, not the agent's.
func (p *player) copyNotes() {
	files, _ := filepath.Glob(filepath.Join(p.ws, "*.md"))
	for _, f := range files {
		name := filepath.Base(f)
		if _, docs := workspaceFiles[name]; docs || name == "CLAUDE.md" || name == "prompt.md" {
			continue
		}
		if b, err := os.ReadFile(f); err == nil {
			if err := os.WriteFile(filepath.Join(p.dir, name), b, 0o644); err != nil {
				p.logf("%s: %v", name, err)
			}
		}
	}
}

// stopAgent is the one way an agent stops (pause, restart, the goal's wrap-up, the end of the run). Killing the
// runtime mid tool call loses the call's output (Codex then logs "custom tool call output is missing" on every later
// request), so its tool processes stop first and the runtime gets up to 3 s to record their output; then the runtime
// gets SIGINT, which Codex, Claude Code and opencode take as an interrupt, and SIGTERM and SIGKILL after 5 s each.
func (p *player) stopAgent() {
	p.mu.Lock()
	cmd := p.agent
	done := p.agentDone
	tools := p.tools
	p.agent = nil
	p.mu.Unlock()
	defer p.r.savePids()
	if alive(cmd) {
		if procs := toolProcs(cmd.Process.Pid); len(procs) > 0 {
			for _, pid := range procs {
				_ = syscall.Kill(pid, syscall.SIGTERM) // gone already is fine
			}
			for end := time.Now().Add(toolGrace); alive(cmd) && tools.busy() && time.Now().Before(end); {
				time.Sleep(50 * time.Millisecond)
			}
		}
		_ = cmd.Process.Signal(os.Interrupt) // the leader only: Codex's launcher passes it on to its binary
		for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
			for i := 0; i < 50 && alive(cmd); i++ {
				time.Sleep(100 * time.Millisecond)
			}
			if !alive(cmd) {
				break
			}
			_ = syscall.Kill(-cmd.Process.Pid, sig)
		}
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			p.logf("agent transcript/usage snapshot did not finish within 10s")
		}
	}
}

// how long stopAgent waits for the runtime to record its stopped tools' output
var toolGrace = 3 * time.Second

// agentCommand builds the agent's process (a variable for the test)
var agentCommand = (*player).agentCmd

// runtimeProcs: the runtimes' own programs (Codex is a node launcher, its binary and a helper)
var runtimeProcs = []string{"node", "codex", "codex-code-mode-host", "claude", "opencode"}

// toolProcs: the agent's tool processes: every process under the runtime (root) that isn't one of the runtime's own,
// with what runs under it. A tool call's shell may have exec'd its command, so they're told apart by the runtime's
// names, not by being shells.
func toolProcs(root int) []int {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil
	}
	kids, names := map[int][]int{}, map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		var pid, ppid int
		var comm string
		if n, _ := fmt.Sscan(line, &pid, &ppid, &comm); n == 3 {
			kids[ppid] = append(kids[ppid], pid)
			names[pid] = filepath.Base(comm)
		}
	}
	var tools []int
	var walk func(pid int, tool bool)
	walk = func(pid int, tool bool) {
		for _, k := range kids[pid] {
			t := tool || !slices.Contains(runtimeProcs, names[k])
			if t {
				tools = append(tools, k)
			}
			walk(k, t)
		}
	}
	walk(root, false)
	return tools
}

// stopSession: calm the character (out of combat), then stop the daemon, which logs out. The log is copied also
// when the session had already exited.
func (p *player) stopSession() {
	p.mu.Lock()
	cmd := p.session
	p.session = nil
	p.mu.Unlock()
	if cmd == nil {
		return
	}
	defer p.r.savePids()
	if alive(cmd) {
		calm(p.port)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) // gone already is fine
		for i := 0; i < 400 && alive(cmd); i++ {            // the logout takes ~20 s out of combat
			time.Sleep(100 * time.Millisecond)
		}
		if alive(cmd) {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
}
