package run

import (
	"strings"
	"testing"

	"github.com/pthm/leeroy/internal/bench/config"
)

// The task prompt: the goal, the scoring, the roster and the pointer to AGENTS.md, and none of the coaching.
func TestPrompt(t *testing.T) {
	b := &config.Bench{Specs: map[string]config.Spec{
		"protwarrior": {Title: "Warrior - Protection", Role: "Tank", Optimise: "holding enemies' attention", Stats: "Stamina"},
		"holy":        {Title: "Priest - Holy", Role: "Healer", Optimise: "healing done", Stats: "Intellect"},
	}}
	s := &config.Scenario{
		Conditions: config.Conditions{Goal: "score:100000", Minutes: 90, Notice: "The server was updated."},
		Agents:     []config.Agent{{Char: "Garrick", Race: "human", Class: "warrior", Spec: "protwarrior"}, {Char: "Paloma", Race: "human", Spec: "holy"}},
	}
	text, err := prompt(s, b, s.Agents[0], "/repo")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"You are Garrick, a Human Warrior - Protection. Your role is Tank: holding enemies' attention.",
		"GOAL: progress as much as you can in 90 minutes", "best\nrate over any 15-minute stretch", "The run lasts 90 minutes",
		"playing this run with you, each with the\nsame goal:\n- Paloma: Priest - Holy (Healer)\nHow you deal", "The server was updated.", "START WITH AGENTS.md",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	for _, gone := range []string{"HOW TO WORK", "REVIEW YOUR PLAY", "baseline", "optimise for", "Garrick:", "give-up", "who to play with"} {
		if strings.Contains(text, gone) {
			t.Errorf("still has %q", gone)
		}
	}
	s.Agents[0].Spec, s.Notice, s.Goal = "", "", "level:10" // no spec, no roster line for it, no notice
	text, _ = prompt(s, b, s.Agents[0], "/repo")
	if !strings.Contains(text, "You are Garrick, a Human Warrior.\nGarrick is already logged in") || !strings.Contains(text, "reach character level 10") || !strings.Contains(text, "give-up") ||
		strings.Contains(text, "\n\n\n") {
		t.Errorf("plain prompt:\n%s", text)
	}
	// a goal with checkpoints: partial credit for the ones done, as the judge scores it
	s.Goal, s.Checkpoints, s.Level = "achievement:628", []config.Checkpoint{{Name: "Rhahk'Zor", Kill: 644}, {Name: "Sneed", Kill: 643}}, 20
	text, _ = prompt(s, b, s.Agents[0], "/repo")
	if !strings.Contains(text, "checkpoints, each a kill: Rhahk'Zor, Sneed.") || strings.Contains(text, "your score is 0") || !strings.Contains(text, "It starts at level 20.") {
		t.Errorf("checkpoints prompt:\n%s", text)
	}
	t.Log("\n" + text)
}

// each runtime's --version, as its version number
func TestCLIVersion(t *testing.T) {
	for in, want := range map[string]string{"2.1.292 (Claude Code)\n": "2.1.292", "codex-cli 0.165.0\n": "0.165.0", "1.18.22\n": "1.18.22"} {
		if got := versionRe.FindString(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if cliVersion("none") != "" {
		t.Error("runtime none has no version")
	}
}
