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

	"github.com/pthm/leeroy/azbench/config"
)

// player is one agent and its character in a run: workspace, session (an azsdk daemon), judge and agent process.
type player struct {
	r           *Runner
	a           config.Agent
	race, class int
	port        int
	ws, dir     string // workspace (bench/workspaces/<name>), its files in the run (<run>/<name>)

	mu       sync.Mutex
	state    string // starting, ready, playing, done, paused, failed
	session  *exec.Cmd
	agent    *exec.Cmd
	agentEnd time.Time // when its agent must stop: the run's clock
	result   []byte    // the judge's final line
}

func (p *player) setState(s string) {
	p.mu.Lock()
	p.state = s
	p.mu.Unlock()
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
	"AGENTS.md":           "bench/agent/AGENTS.md",
	"game.md":             "azeroth-sdk/docs/game.md",
	"sdk.md":              "azeroth-sdk/docs/sdk.md",
	"moving.md":           "azeroth-sdk/docs/moving.md",
	"fighting.md":         "azeroth-sdk/docs/fighting.md",
	"npcs-and-items.md":   "azeroth-sdk/docs/npcs-and-items.md",
	"death-and-travel.md": "azeroth-sdk/docs/death-and-travel.md",
	"players.md":          "azeroth-sdk/docs/players.md",
	"troubleshooting.md":  "azeroth-sdk/docs/troubleshooting.md",
	"azworld.md":          "azeroth-sdk/docs/azworld.md",
	"api-reference.md":    "azeroth-sdk/docs/api-reference.md",
	"example.lua":         "azeroth-sdk/examples/lua/skeleton.lua",
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
		if err := os.WriteFile(filepath.Join(p.ws, name), b, 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(p.ws, "CLAUDE.md"), []byte("@AGENTS.md\n"), 0o644); err != nil {
		return err
	}
	notes := filepath.Join(p.ws, "NOTES.md")
	if _, err := os.Stat(notes); err != nil {
		os.WriteFile(notes, []byte("# Notes\n\nYour notebook, kept with your workspace (AGENTS.md, \"NOTES.md\").\n"), 0o644)
	}
	return nil
}

// daemon starts the character's azsdk session; extra flags: -duration for a short login.
func (p *player) daemon(extra ...string) (*exec.Cmd, error) {
	log, err := os.OpenFile(filepath.Join(p.ws, "session.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	args := []string{"-char", p.a.Char, "-account", p.account(), "-listen", fmt.Sprintf("127.0.0.1:%d", p.port),
		"-agent", p.a.Name,
		"-data", filepath.Join(p.r.repo, "data"), "-workspace", p.ws, "-race", fmt.Sprint(p.race), "-class", fmt.Sprint(p.class)}
	if len(extra) == 0 {
		args = append(args, "-record", filepath.Join(p.r.dir, p.a.Name+".record.jsonl"), "-events", p.r.dir)
	}
	cmd := exec.Command(filepath.Join(p.r.repo, "bin", "azsdk"), append(args, extra...)...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, err
	}
	go func() { cmd.Wait(); log.Close() }()
	return cmd, nil
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
				_, err := eval(ctx, p.port, fmt.Sprintf("LIB = %q; chat.join(%q, 1)", p.ws+"/", p.r.s.Channel))
				return err
			}
			select {
			case <-ctx.Done():
				syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
		p.logf("session login failed (try %d), see %s/session.log", try, p.ws)
		if try == 3 {
			return fmt.Errorf("session login failed")
		}
		time.Sleep(time.Duration(try*5) * time.Second)
	}
}

// prepare: workspace, account, starting conditions (on the offline character, creating it with a short login
// first), then the session. The player is ready to start when it returns nil.
func (p *player) prepare(ctx context.Context) error {
	if err := p.workspace(); err != nil {
		return err
	}
	if out, err := exec.Command(filepath.Join(p.r.repo, "bin", "account"), p.account(), p.account()).CombinedOutput(); err != nil {
		return fmt.Errorf("account: %v: %s", err, out)
	}
	os.WriteFile(filepath.Join(p.ws, "session.log"), nil, 0o644) // the workspace (and old log) persists
	s := p.r.s
	if s.Level > 0 || s.Money > 0 || s.Start != nil || len(s.Setup) > 0 {
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
			return fmt.Errorf("character not created, see %s/session.log", p.ws)
		}
		if !waitOffline(p.a.Char, 2*time.Minute) {
			return fmt.Errorf("still online")
		}
		if err := applyStart(s, p.a.Char); err != nil {
			return fmt.Errorf("starting conditions: %v", err)
		}
	} else if !waitOffline(p.a.Char, 2*time.Minute) {
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
		common := []string{"--json", "--skip-git-repo-check", "-c", "sandbox_workspace_write.network_access=true",
			"-c", `approval_policy="never"`, "-c", "shell_environment_policy.inherit=all"}
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
		args = append(args, "--add-dir", repo+"/azeroth-sdk/client", repo+"/azeroth-sdk/session", repo+"/azeroth-sdk/script",
			repo+"/server/azerothcore/src/server/game", repo+"/server/azerothcore/src/server/shared",
			"--allowedTools")
		for _, c := range agentCommands {
			args = append(args, "Bash("+c+":*)")
		}
		args = append(args, "Edit(./**)", "--disallowedTools", "Edit(~/.claude/**)", "Read(~/.claude/**)")
		cmd = exec.Command("claude", args...)
	case "opencode":
		args := []string{"run", "--pure", "--format", "json", "-m", a.Model, "--dir", p.ws}
		if resume != "" {
			args = append(args, "--session", resume)
		}
		cmd = exec.Command("opencode", append(args, text)...)
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
		// its own empty config dir, so the user's opencode config (plugins, providers, AGENTS.md) stays out of the run
		cfg := filepath.Join(p.dir, "opencode")
		os.MkdirAll(cfg, 0o755)
		env = append(env, "XDG_CONFIG_HOME="+cfg, "OPENCODE_CONFIG_CONTENT="+opencodeConfig(repo, p.ws))
	}
	cmd.Env = append(env, "PATH="+filepath.Join(repo, "bench/agent/bin")+":"+os.Getenv("PATH"),
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "WOWBENCH_BOARD="+filepath.Join(p.r.dir, "board"),
		"WOWBENCH_CHAR="+a.Char, fmt.Sprintf("WOW_PORT=%d", p.port))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // so stopping it stops its tools too
	return cmd
}

// agentCommands: the shell commands an agent may run (bench/agent/AGENTS.md lists them): its three tools and
// read-only file and text commands, so output can be piped through head or grep
var agentCommands = []string{"wow", "azworld", "standings", "sleep", "tail", "cat", "ls", "grep", "mkdir", "head", "wc",
	"sort", "uniq", "cut"}

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
	for _, d := range []string{"azeroth-sdk/client", "azeroth-sdk/session", "azeroth-sdk/script",
		"server/azerothcore/src/server/game", "server/azerothcore/src/server/shared"} {
		ext[filepath.Join(repo, d)+"/**"] = "allow"
	}
	// edit rules match paths relative to the git root (the repo), not absolute ones
	rel, _ := filepath.Rel(repo, ws)
	b, _ := json.Marshal(map[string]any{"autoupdate": false, "share": "disabled", "snapshot": false, "permission": map[string]any{
		"bash": bash, "external_directory": ext, "edit": map[string]string{"*": "deny", rel + "/**": "allow"},
		"webfetch": "deny", "websearch": "deny", "codesearch": "deny", "doom_loop": "allow"}})
	return string(b)
}

// startAgent runs the agent until end, appending its output to agent.jsonl. Codex's JSONL events have no
// timestamps, so each line is stamped as it arrives (the viewer and the report place them on the timeline);
// opencode's have their own, in unix ms, which become the same ISO time.
func (p *player) startAgent(text, resume string, end time.Time) error {
	p.mu.Lock()
	p.agentEnd = end
	p.mu.Unlock()
	if p.a.Runtime == "none" { // no agent: an empty transcript, so the report card still lists the player
		f, err := os.OpenFile(filepath.Join(p.dir, "agent.jsonl"), os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
		}
		return err
	}
	out, err := os.OpenFile(filepath.Join(p.dir, "agent.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	cmd := p.agentCmd(text, resume)
	cmd.Stdin = nil
	var stamp *io.PipeWriter
	if p.a.Runtime != "claude" {
		pr, pw := io.Pipe()
		stamp = pw
		cmd.Stdout, cmd.Stderr = pw, pw
		go stampLines(pr, out)
	} else {
		cmd.Stdout, cmd.Stderr = out, out
	}
	if err := cmd.Start(); err != nil {
		out.Close()
		return err
	}
	p.mu.Lock()
	p.agent = cmd
	p.mu.Unlock()
	stop := time.AfterFunc(time.Until(end), func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) })
	go func() {
		cmd.Wait()
		stop.Stop()
		if stamp != nil {
			stamp.Close()
		} else {
			out.Close()
		}
		p.exited(cmd)
	}()
	return nil
}

func stampLines(r io.Reader, w io.WriteCloser) {
	defer w.Close()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		ts := `"timestamp":"` + time.Now().UTC().Format("2006-01-02T15:04:05Z") + `"`
		var obj map[string]json.RawMessage
		if len(line) > 1 && line[0] == '{' && json.Unmarshal(line, &obj) == nil {
			if ms, err := strconv.ParseInt(string(obj["timestamp"]), 10, 64); err == nil {
				obj["timestamp"], _ = json.Marshal(time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z"))
				line, _ = json.Marshal(obj)
			} else if len(obj) > 0 {
				line = append(append(line[:len(line)-1:len(line)-1], ','), ts+"}"...)
			} else {
				line = []byte("{" + ts + "}")
			}
		} else {
			t, _ := json.Marshal(sc.Text())
			line = []byte(`{"type":"stderr","text":` + string(t) + "," + ts + "}")
		}
		w.Write(append(line, '\n'))
	}
}

// exited: an agent that stops with 2+ minutes left is reported (alerts.log), with the runtime's last error (a
// Codex usage limit, say). Nothing else would notice: the judge keeps scoring an idle character.
func (p *player) exited(cmd *exec.Cmd) {
	p.mu.Lock()
	left, current := time.Until(p.agentEnd), p.agent == cmd
	p.mu.Unlock()
	if !current || left < 2*time.Minute || p.r.ctx.Err() != nil {
		return
	}
	why := ""
	f, _ := os.Open(filepath.Join(p.dir, "agent.jsonl"))
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
	p.r.alert(p, msg)
}

// meta writes meta.json: who plays this character, for the report, the board and the viewer. The model is what
// the runtime reports once it starts (Claude: its init event; Codex: its session file; opencode: as configured).
func (p *player) meta(spec config.Spec) {
	model := ""
	for i := 0; i < 60 && model == "" && p.a.Runtime != "none" && p.a.Runtime != "opencode"; i++ {
		model = detectModel(p.a.Runtime, filepath.Join(p.dir, "agent.jsonl"))
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
	b, _ := json.Marshal(map[string]any{"agent": p.a.Name, "char": p.a.Char, "runtime": p.a.Runtime, "model": model,
		"provider": p.a.Provider, "spec": spec.Title, "role": spec.Role, "goal": p.r.s.Goal, "port": p.port})
	os.WriteFile(filepath.Join(p.dir, "meta.json"), b, 0o644)
}

func detectModel(runtime, transcript string) string {
	f, err := os.Open(transcript)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for i := 0; i < 5 && sc.Scan(); i++ {
		var e struct {
			Type, Subtype, Model string
			ThreadID             string `json:"thread_id"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if runtime == "claude" && e.Subtype == "init" {
			return e.Model
		}
		if runtime == "codex" && e.Type == "thread.started" && e.ThreadID != "" {
			home, _ := os.UserHomeDir()
			m, _ := filepath.Glob(filepath.Join(home, ".codex/sessions/*/*/*/*"+e.ThreadID+".jsonl"))
			if len(m) > 0 {
				b, _ := os.ReadFile(m[0])
				if x := regexp.MustCompile(`"model":"([^"]*)"`).FindSubmatch(b); x != nil {
					return string(x[1])
				}
			}
		}
	}
	return ""
}

// conversation is the agent's earlier conversation to resume: Claude's or opencode's session id or Codex's thread id.
func (p *player) conversation() string {
	f, err := os.Open(filepath.Join(p.dir, "agent.jsonl"))
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

// stopAgent ends the agent's process group.
func (p *player) stopAgent() {
	p.mu.Lock()
	cmd := p.agent
	p.agent = nil
	p.mu.Unlock()
	if alive(cmd) {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		for i := 0; i < 50 && alive(cmd); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if alive(cmd) {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
}

// stopSession: calm the character (out of combat), then stop the daemon, which logs out.
func (p *player) stopSession() {
	p.mu.Lock()
	cmd := p.session
	p.session = nil
	p.mu.Unlock()
	if !alive(cmd) {
		return
	}
	calm(p.port)
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	for i := 0; i < 400 && alive(cmd); i++ { // the logout takes ~20 s out of combat
		time.Sleep(100 * time.Millisecond)
	}
	if alive(cmd) {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if b, err := os.ReadFile(filepath.Join(p.ws, "session.log")); err == nil {
		os.WriteFile(filepath.Join(p.dir, "session.log"), b, 0o644)
	}
}
