package run

// Provider outages. An agent that stops on a temporary provider error (a model at capacity, overloaded, rate limited,
// a server or network error) isn't the agent's doing, so its player waits it out without losing clock time: its
// character logs out at a safe moment (out of combat, no hostile near its aggro range, health up; the session's
// logOut fights off anything that attacks during the logout countdown), its judge's clock holds, and the provider is
// probed with a one-line prompt at growing intervals. When the probe answers, the character logs back in and the
// agent's conversation resumes, told how long it was away. The other players carry on throughout. Each outage is a
// line in <run>/<agent>/outages.jsonl and in alerts.log; one that lasts longer than outageLimit ends the player's run
// (a give-up, "provider outage"), since its clock can't hold forever.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pthm/leeroy/azbench/tools"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

var (
	// errors that end when the provider recovers
	transientRe = regexp.MustCompile(`(?i)at capacity|overloaded|rate.?limit|too many requests|\b(429|5\d\d)\b|temporarily|try again|unavailable|internal server error|bad gateway|gateway timeout|timed? ?out|connection (reset|refused|closed)|network|econn|stream (error|disconnected)`)
	// errors that don't: the account's quota, credit or credentials
	fatalRe = regexp.MustCompile(`(?i)quota|usage limit|insufficient|billing|credit|payment|unauthori[sz]ed|authentication|invalid (api )?key|forbidden|\b(401|403)\b|not found|does not exist|context length|too long`)
)

// transientError: whether an agent's exit error is a provider outage to wait out.
func transientError(why string) bool {
	return why != "" && transientRe.MatchString(why) && !fatalRe.MatchString(why)
}

// The outage's timing (variables for the test).
var (
	outageBackoff = []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}
	outageLimit   = 2 * time.Hour    // then the player's run ends
	safeWait      = 10 * time.Minute // how long to wait for a safe moment before logging out anyway
	safePoll      = 5 * time.Second
)

// outageRecord is one outage, for outages.jsonl and the website.
type outageRecord struct {
	Why       string  `json:"why"`
	Start     string  `json:"start"`                // when the agent stopped
	LoggedOut string  `json:"logged_out,omitempty"` // when the character left the world (the clock held from then)
	End       string  `json:"end,omitempty"`        // when the agent was back
	SafeWaitS float64 `json:"safe_wait_s"`          // how long it took to find a safe moment
	HeldS     float64 `json:"held_s"`               // clock time not counted
	Probes    int     `json:"probes"`               // provider checks until one answered
	Outcome   string  `json:"outcome"`              // resumed, gave_up (too long), stopped (the run paused or ended)
}

func (p *player) held() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hold
}

func (p *player) setHold(on bool) {
	p.mu.Lock()
	p.hold = on
	p.mu.Unlock()
}

// outage waits out a provider outage for p (see the top of the file). It runs in its own goroutine.
func (p *player) outage(why string) {
	r := p.r
	rec := outageRecord{Why: why, Start: time.Now().Format(time.RFC3339)}
	defer func() {
		b, _ := json.Marshal(rec)
		f, _ := os.OpenFile(filepath.Join(p.dir, "outages.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if f != nil {
			f.Write(append(b, '\n'))
			f.Close()
		}
	}()
	r.alert(p, fmt.Sprintf("provider outage (%s): logging out at a safe moment, clock held until the provider answers", why))
	p.runtimeState("supervisor", types.StatePause)

	// a safe moment: the agent's scripts may still be playing, so first calm (stop them, out of combat), then wait
	// for no hostile near its aggro range and health up; a fight in the meantime is calmed again
	t0 := time.Now()
	for time.Since(t0) < safeWait && r.ctx.Err() == nil {
		p.mu.Lock()
		up := alive(p.session)
		p.mu.Unlock()
		if !up { // nothing in the world to keep safe
			break
		}
		calm(p.port)
		if safe(r.ctx, p.port) {
			break
		}
		time.Sleep(safePoll)
	}
	rec.SafeWaitS = time.Since(t0).Round(time.Second).Seconds()
	if r.ctx.Err() != nil {
		rec.Outcome = "stopped"
		return
	}
	p.stop() // the session logs out (logOut: fights back and retries if attacked during the countdown)
	p.setHold(true)
	heldAt := time.Now()
	rec.LoggedOut = heldAt.Format(time.RFC3339)
	defer func() { rec.HeldS = time.Since(heldAt).Round(time.Second).Seconds() }()

	for i := 0; ; i++ {
		wait := outageBackoff[min(i, len(outageBackoff)-1)]
		if time.Since(heldAt)+wait > outageLimit {
			p.setHold(false)
			rec.Outcome = "gave_up"
			// the judge ends the player's run unmet at its next poll
			b, _ := json.Marshal(map[string]any{"reason": "provider outage: " + why, "at": time.Now().UTC().Format(time.RFC3339)})
			os.WriteFile(tools.GiveUpFile(filepath.Join(r.dir, "board"), p.a.Char), b, 0o644)
			r.alert(p, fmt.Sprintf("provider outage lasted over %s: the run ends for this player", outageLimit))
			return
		}
		select {
		case <-r.ctx.Done():
			p.setHold(false)
			rec.Outcome = "stopped"
			return
		case <-time.After(wait):
		}
		rec.Probes++
		if err := p.probe(r.ctx); err != nil {
			p.logf("outage: provider still unavailable (%v)", err)
			continue
		}
		break
	}

	// back: log in, release the clock, resume the agent with what happened and where it is
	if !waitOffline(p.a.Char, 2*time.Minute) {
		r.alert(p, "outage: character still online; resuming anyway")
	}
	if err := p.login(r.ctx); err != nil {
		p.setHold(false)
		rec.Outcome = "stopped"
		r.alert(p, "outage: login failed: "+err.Error())
		return
	}
	away := time.Since(heldAt)
	p.setHold(false)
	left := p.minutesLeft()
	msg := fmt.Sprintf("Your model provider was unavailable (%s), so your agent stopped. Your character was logged out at a safe moment "+
		"%.0f minutes ago and has just logged back in where it was; the clock was paused meanwhile, so you lost no time. "+
		"%.0f minutes are left. Your scripts didn't survive the logout: start what you need again. Carry on.",
		why, away.Minutes(), left.Minutes())
	if err := p.startAgent(msg+p.restored(), p.conversation(), time.Now().Add(left)); err != nil {
		rec.Outcome = "stopped"
		r.alert(p, "outage: agent not resumed: "+err.Error())
		return
	}
	rec.Outcome, rec.End = "resumed", time.Now().Format(time.RFC3339)
	r.alert(p, fmt.Sprintf("provider back after %.0f min (clock held %.0f min): resumed, %.0f min left", time.Since(t0).Minutes(), away.Minutes(), left.Minutes()))
}

// safe: out of combat, alive (or dead: a corpse or ghost can't be attacked), at least half health, and at least 5 yd
// outside every hostile creature's aggro range.
func safe(ctx context.Context, port int) bool {
	out, err := eval(ctx, port, `local h = me.health(); local margin = 1e9
for _, c in ipairs(units.nearby(60)) do
  if c.alive and c.reaction == "hostile" and c.aggro_radius then margin = math.min(margin, c.distance - c.aggro_radius) end
end
return {combat = me.in_combat(), alive = me.alive(), hp = h.current / math.max(1, h.max), margin = margin}`)
	if err != nil {
		return false
	}
	var s struct {
		Combat, Alive bool
		HP, Margin    float64
	}
	if json.Unmarshal(out, &s) != nil || s.Combat {
		return false
	}
	return !s.Alive || (s.HP >= 0.5 && s.Margin >= 5)
}

// minutesLeft on this player's clock: its judge's (outages held don't count), from the standings board.
func (p *player) minutesLeft() time.Duration {
	var b struct {
		MinutesLeft *float64 `json:"minutes_left"`
	}
	if raw, err := os.ReadFile(filepath.Join(p.r.dir, "board", p.a.Char+".json")); err == nil && json.Unmarshal(raw, &b) == nil && b.MinutesLeft != nil {
		return time.Duration(*b.MinutesLeft * float64(time.Minute))
	}
	return time.Duration(p.r.minutesLeft() * float64(time.Minute))
}

// probe asks the agent's model for one word through its own runtime: nil when it answers.
func (p *player) probe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	const ask = "Reply with the single word ok."
	var cmd *exec.Cmd
	switch p.a.Runtime {
	case "codex":
		args := []string{"exec", "--json", "--skip-git-repo-check", "-s", "read-only"}
		if p.a.Model != "" {
			args = append(args, "-m", p.a.Model)
		}
		cmd = exec.CommandContext(ctx, "codex", append(args, ask)...)
	case "opencode":
		cmd = exec.CommandContext(ctx, "opencode", "run", "--pure", "--format", "json", "-m", p.a.Model, ask)
	case "none":
		return nil
	default:
		args := []string{"-p", ask, "--output-format", "json", "--strict-mcp-config"}
		if p.a.Model != "" {
			args = append(args, "--model", p.a.Model)
		}
		cmd = exec.CommandContext(ctx, "claude", args...)
	}
	cmd.Dir = os.TempDir()
	out, err := cmd.CombinedOutput()
	text := string(out)
	switch {
	case err != nil:
		return fmt.Errorf("%v: %s", err, lastLine(text))
	case strings.Contains(text, `"turn.failed"`), strings.Contains(text, `"is_error":true`), strings.Contains(text, `"type":"error"`):
		return fmt.Errorf("%s", lastLine(text))
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := lines[len(lines)-1]
	if len(l) > 200 {
		l = l[:200]
	}
	return l
}
