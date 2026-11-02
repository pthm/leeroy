package run

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sdkPaths: what the agents run and read: the SDK (its docs too) and their tools.
var sdkPaths = []string{"azeroth-sdk", "bench/agent"}

func git(repo string, args ...string) ([]byte, error) {
	return exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
}

// sdkRev is the SDK's revision in repo: the last commit under sdkPaths, then "+<hash>" when they have uncommitted
// changes (a hash of them, so new edits are a new revision). In a jj checkout git's HEAD is @-, so the working copy's
// changes are the uncommitted ones.
func sdkRev(repo string) (string, error) {
	head, err := git(repo, append([]string{"log", "-1", "--format=%H", "HEAD", "--"}, sdkPaths...)...)
	if err != nil {
		return "", err
	}
	diff, err := git(repo, append([]string{"diff", "HEAD", "--"}, sdkPaths...)...)
	if err != nil {
		return "", err
	}
	untracked, err := git(repo, append([]string{"ls-files", "--others", "--exclude-standard", "--"}, sdkPaths...)...)
	if err != nil {
		return "", err
	}
	rev := strings.TrimSpace(string(head))
	if len(diff)+len(untracked) > 0 {
		h := sha256.New()
		h.Write(diff)
		for _, f := range strings.Fields(string(untracked)) {
			b, _ := os.ReadFile(filepath.Join(repo, f))
			fmt.Fprintf(h, "%s\x00%s\x00", f, b)
		}
		rev += fmt.Sprintf("+%x", h.Sum(nil)[:6])
	}
	return rev, nil
}

// whatChanged is the section a resume or restart message ends with when the SDK revision moved from old to cur: the
// subjects of the commits under sdkPaths since old, and a line for uncommitted changes. "" when nothing changed or
// old is unknown (a run from before meta.json had it).
func whatChanged(repo, old, cur string) string {
	if old == "" || old == cur {
		return ""
	}
	oldCommit, _, _ := strings.Cut(old, "+")
	curCommit, _, dirty := strings.Cut(cur, "+")
	var lines []string
	if oldCommit != curCommit {
		out, err := git(repo, append([]string{"log", "--reverse", "--format=%s", oldCommit + ".." + curCommit, "--"}, sdkPaths...)...)
		if err != nil {
			lines = append(lines, fmt.Sprintf("- The SDK moved from revision %.12s to %.12s (no history between them).", oldCommit, curCommit))
		}
		subjects := strings.FieldsFunc(string(out), func(r rune) bool { return r == '\n' })
		for i, s := range subjects {
			if i == 40 { // ponytail: a cap, not a summary; a run rarely spans more
				lines = append(lines, fmt.Sprintf("- and %d more commits", len(subjects)-40))
				break
			}
			lines = append(lines, "- "+s)
		}
	}
	if dirty {
		lines = append(lines, "- The SDK also has changes not yet committed, which aren't listed here.")
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\nWHAT CHANGED in the SDK and your tools since your last session (your workspace docs are updated to match):\n" +
		strings.Join(lines, "\n") + "\n"
}

// changes records the SDK revision this session starts on (meta.json's sdk_rev) and returns WHAT CHANGED since the
// one recorded before. Per agent, not per run: a restart moves one agent to a new build while the others stay.
func (p *player) changes() string {
	cur, err := sdkRev(p.r.repo)
	if err != nil {
		p.logf("SDK revision: %v", err)
		return ""
	}
	old := ""
	p.updateMeta(func(m map[string]any) {
		old, _ = m["sdk_rev"].(string)
		m["sdk_rev"] = cur
	})
	return whatChanged(p.r.repo, old, cur)
}

// updateMeta changes meta.json (fn gets it, empty if there's none yet) under the player's meta lock.
func (p *player) updateMeta(fn func(map[string]any)) {
	p.metaMu.Lock()
	defer p.metaMu.Unlock()
	path := filepath.Join(p.dir, "meta.json")
	m := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &m)
	}
	fn(m)
	b, _ := json.Marshal(m)
	os.WriteFile(path, b, 0o644)
}
