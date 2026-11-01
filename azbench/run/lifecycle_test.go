package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
