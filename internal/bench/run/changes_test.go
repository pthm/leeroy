package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWhatChanged(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	commit := func(file, subject string) {
		t.Helper()
		os.MkdirAll(filepath.Join(repo, filepath.Dir(file)), 0o755)
		os.WriteFile(filepath.Join(repo, file), []byte(subject), 0o644)
		run("add", ".")
		run("commit", "-q", "-m", subject+"\n\nbody")
	}
	rev := func() string {
		t.Helper()
		r, err := sdkRev(repo)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	run("init", "-q")
	commit("azsdk/a.go", "azsdk: first")
	first := rev()
	commit("azsdk/a.go", "azsdk: spells.stop_casting")
	commit("cmd/azbench/x.go", "azbench: unrelated")
	commit("suite/agent/AGENTS.md", "agent: wow wait coalesces chat")
	second := rev()
	if strings.Contains(second, "+") {
		t.Fatalf("clean tree has a dirty revision: %s", second)
	}
	if got := whatChanged(repo, second, second); got != "" {
		t.Fatalf("same revision: %q", got)
	}
	if got := whatChanged(repo, "", second); got != "" {
		t.Fatalf("no earlier revision: %q", got)
	}
	got := whatChanged(repo, first, second)
	want := "\n- azsdk: spells.stop_casting\n- agent: wow wait coalesces chat\n"
	if !strings.Contains(got, "WHAT CHANGED") || !strings.HasSuffix(got, want) || strings.Contains(got, "unrelated") {
		t.Fatalf("whatChanged:\n%s", got)
	}

	// uncommitted changes: a new revision, and only the uncommitted line (no new commits)
	os.WriteFile(filepath.Join(repo, "azsdk", "new.go"), []byte("x"), 0o644)
	dirty := rev()
	if !strings.HasPrefix(dirty, second+"+") {
		t.Fatalf("dirty revision %q", dirty)
	}
	os.WriteFile(filepath.Join(repo, "azsdk", "new.go"), []byte("y"), 0o644)
	if again := rev(); again == dirty {
		t.Fatal("an edit to an untracked file kept the revision")
	}
	got = whatChanged(repo, second, dirty)
	if strings.Count(got, "\n- ") != 1 || !strings.Contains(got, "not yet committed") {
		t.Fatalf("uncommitted:\n%s", got)
	}
	if got := whatChanged(repo, "nonesuch", second); !strings.Contains(got, "no history") {
		t.Fatalf("unknown revision:\n%s", got)
	}
}
