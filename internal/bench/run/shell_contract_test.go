package run

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pthm/leeroy/internal/bench/config"
)

func TestRuntimeCommandsAndGeneratedContract(t *testing.T) {
	for _, runtime := range []string{"claude", "codex", "opencode"} {
		t.Run(runtime, func(t *testing.T) {
			dir := t.TempDir()
			p := &player{r: &Runner{repo: dir, dir: dir}, ws: filepath.Join(dir, "workspace"), dir: dir, a: config.Agent{Runtime: runtime, Char: "Test", Name: "test"}}
			command := p.agentCmd("smoke", "")
			args := strings.Join(command.Args, " ")
			env := strings.Join(command.Env, "\n")
			if !strings.Contains(env, "WOW_MAX_WAIT=110") {
				t.Fatal("missing wait limit")
			}
			contract := shellContract(runtime)
			for _, name := range agentCommands {
				if !strings.Contains(contract, "`"+name+"`") {
					t.Fatal("help missing", name)
				}
				if runtime == "claude" && !strings.Contains(args, "Bash("+name+":*)") {
					t.Fatal("permission missing", name)
				}
			}
			if runtime == "opencode" {
				var v struct {
					Permission struct {
						Bash map[string]string `json:"bash"`
					} `json:"permission"`
				}
				if err := json.Unmarshal([]byte(opencodeConfig(dir, p.ws)), &v); err != nil {
					t.Fatal(err)
				}
				for _, name := range agentCommands {
					if v.Permission.Bash[name] != "allow" || v.Permission.Bash[name+" *"] != "allow" {
						t.Fatal("permission missing", name)
					}
				}
				if v.Permission.Bash["*"] != "deny" {
					t.Fatal("unknown commands not denied")
				}
			}
		})
	}
}

func TestProviderCredentialCheckIncludesOpenCodeModelPrefix(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	for _, a := range []config.Agent{
		{Runtime: "claude", Provider: "openrouter"},
		{Runtime: "opencode", Model: "openrouter/openai/gpt-6.1-sol"},
	} {
		if validateAgentCredentials(a) == nil {
			t.Fatal("missing key accepted", a)
		}
	}
	if err := validateAgentCredentials(config.Agent{Runtime: "opencode", Model: "openai/gpt-6.1-sol"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENROUTER_API_KEY", "test-placeholder")
	if err := validateAgentCredentials(config.Agent{Runtime: "opencode", Model: "openrouter/openai/gpt-6.1-sol"}); err != nil {
		t.Fatal(err)
	}
}

// Each runtime gets the agent's reasoning effort in its own flag.
func TestEffortFlags(t *testing.T) {
	want := map[string]string{"claude": "--effort high", "codex": `model_reasoning_effort="high"`, "opencode": "--variant high"}
	for runtime, flag := range want {
		dir := t.TempDir()
		p := &player{
			r: &Runner{repo: dir, dir: dir}, ws: filepath.Join(dir, "workspace"), dir: dir,
			a: config.Agent{Runtime: runtime, Char: "Test", Name: "test", Model: "x/y", Effort: "high"},
		}
		if args := strings.Join(p.agentCmd("smoke", "").Args, " "); !strings.Contains(args, flag) {
			t.Errorf("%s: no %q in %s", runtime, flag, args)
		}
		p.a.Effort = ""
		if args := strings.Join(p.agentCmd("smoke", "").Args, " "); strings.Contains(args, "effort") || strings.Contains(args, "--variant") {
			t.Errorf("%s: an effort flag without an effort: %s", runtime, args)
		}
	}
}
