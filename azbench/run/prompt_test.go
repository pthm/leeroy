package run

import (
	"strings"
	"testing"

	"github.com/pthm/leeroy/azbench/config"
)

// The task prompt: the goal, the scoring, the roster and the pointer to AGENTS.md, and none of the coaching.
func TestPrompt(t *testing.T) {
	b := &config.Bench{Specs: map[string]config.Spec{
		"protwarrior": {Title: "Warrior - Protection", Role: "Tank", Optimise: "holding enemies' attention", Stats: "Stamina"},
		"holy":        {Title: "Priest - Holy", Role: "Healer", Optimise: "healing done", Stats: "Intellect"},
	}}
	s := &config.Scenario{Goal: "score:100000", Minutes: 90, Notice: "The server was updated.",
		Agents: []config.Agent{{Char: "Garrick", Race: "human", Spec: "protwarrior"}, {Char: "Paloma", Race: "human", Spec: "holy"}}}
	text, err := prompt(s, b, s.Agents[0], "/repo")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"You are Garrick, a Human Warrior - Protection. Your role is Tank: holding enemies' attention.",
		"GOAL: progress as much as you can in 90 minutes", "best\nrate over any 15-minute stretch", "The run lasts 90 minutes",
		"This run:\n- Paloma: Priest - Holy (Healer)\nHow you deal", "The server was updated.", "START WITH AGENTS.md"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	for _, gone := range []string{"HOW TO WORK", "REVIEW YOUR PLAY", "baseline", "optimise for", "Garrick:"} {
		if strings.Contains(text, gone) {
			t.Errorf("still has %q", gone)
		}
	}
	s.Agents[0].Spec, s.Notice, s.Goal = "", "", "level:10" // no spec, no roster line for it, no notice
	text, _ = prompt(s, b, s.Agents[0], "/repo")
	if !strings.Contains(text, "Your character Garrick is already logged in") || !strings.Contains(text, "reach character level 10") ||
		strings.Contains(text, "\n\n\n") {
		t.Errorf("plain prompt:\n%s", text)
	}
	t.Log("\n" + text)
}
