package run

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pthm/leeroy/azbench/config"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// Opt-in: uses installed runtimes and paid model access. Logs stay in the named
// artifact directory; normal unit tests never start model processes.
func TestRuntimePermissionsLive(t *testing.T) {
	selected := os.Getenv("WOWBENCH_LIVE_RUNTIMES")
	if selected == "" {
		t.Skip("set WOWBENCH_LIVE_RUNTIMES=claude,codex,opencode and WOWBENCH_LIVE_ARTIFACTS")
	}
	root := os.Getenv("WOWBENCH_LIVE_ARTIFACTS")
	if !filepath.IsAbs(root) {
		t.Fatal("WOWBENCH_LIVE_ARTIFACTS must be an absolute fresh artifact directory")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	commands := []string{"wow help", "azworld help", "standings --help", "sleep 0.1", "tail -n 1 fixture.txt", "cat fixture.txt", "ls .", "grep alpha fixture.txt", "mkdir -p smoke-dir", "head -n 1 fixture.txt", "wc -l fixture.txt", "sort fixture.txt", "uniq fixture.txt", "cut -c 1-2 fixture.txt", "rg alpha fixture.txt"}
	for _, runtime := range strings.Split(selected, ",") {
		t.Run(runtime, func(t *testing.T) {
			if runtime != "claude" && runtime != "codex" && runtime != "opencode" {
				t.Fatal("unsupported runtime")
			}
			dir := filepath.Join(root, runtime)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			ws := filepath.Join(dir, "workspace")
			if err := os.Mkdir(ws, 0755); err != nil {
				t.Fatal("fresh workspace required:", err)
			}
			for name, body := range map[string]string{"fixture.txt": "alpha\nbeta\n", "AGENTS.md": shellContract(runtime), "CLAUDE.md": "@AGENTS.md\n"} {
				if err := os.WriteFile(filepath.Join(ws, name), []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			a := config.Agent{Runtime: runtime, Name: "smoke", Char: "Devgm", Model: os.Getenv("WOWBENCH_SMOKE_MODEL_" + strings.ToUpper(runtime))}
			if runtime == "opencode" && a.Model == "" {
				t.Fatal("set WOWBENCH_SMOKE_MODEL_OPENCODE to an available provider/model")
			}
			p := &player{r: &Runner{repo: repo, dir: dir}, ws: ws, dir: dir, port: 8112, a: a}
			prompt := "Run these permission smoke commands exactly, one separate shell tool call each. Do not change files or call other commands. Finish only after all calls return; if a tool yields a session/task handle, poll it with the runtime polling tool. Report failures accurately. These are local tool tests, not gameplay.\n" + strings.Join(commands, "\n")
			cmd := p.agentCmd(prompt, "")
			out, err := os.Create(filepath.Join(dir, "agent.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stdout, cmd.Stderr = out, out
			if err := cmd.Start(); err != nil {
				out.Close()
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err = <-done:
			case <-time.After(4 * time.Minute):
				syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
				err = <-done
				t.Errorf("runtime smoke deadline expired")
			}
			out.Close()
			if err != nil {
				t.Errorf("runtime process: %v (see %s)", err, dir)
			}
			seen := map[string]bool{}
			diag := newToolDiagnostics(runtime, func(d types.Diagnostic) {
				if d.Kind == "tool_call" && d.Outcome == "succeeded" {
					seen[d.Details.CommandFamily] = true
				}
			})
			f, err := os.Open(filepath.Join(dir, "agent.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			scan := bufio.NewScanner(f)
			scan.Buffer(make([]byte, 64<<10), 8<<20)
			for scan.Scan() {
				decodeToolLine(scan.Bytes(), diag.observe)
			}
			if err := scan.Err(); err != nil {
				t.Fatal(err)
			}
			for _, name := range agentCommands {
				if !seen[name] {
					t.Errorf("no successful %s tool evidence; inspect %s", name, dir)
				}
			}
			summary, _ := json.MarshalIndent(seen, "", "  ")
			os.WriteFile(filepath.Join(dir, "commands.json"), summary, 0644)
			fmt.Fprintf(os.Stderr, "%s permission smoke: %d command families observed; artifacts %s\n", runtime, len(seen), dir)
		})
	}
}
