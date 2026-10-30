package run

import (
	"encoding/json"
	"github.com/pthm/leeroy/azbench/config"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeCommandsAndGeneratedContract(t *testing.T) {
	for _, runtime := range []string{"claude", "codex", "opencode"} {
		t.Run(runtime, func(t *testing.T) {
			dir := t.TempDir()
			p := &player{r: &Runner{repo: dir, dir: dir}, ws: filepath.Join(dir, "workspace"), dir: dir, a: config.Agent{Runtime: runtime, Char: "Test", Name: "test"}}
			command := p.agentCmd("smoke", "")
			args := strings.Join(command.Args, " ")
			env := strings.Join(command.Env, "\n")
			if !strings.Contains(env, "WOW_MAX_WAIT=25") {
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
