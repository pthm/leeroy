package run

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	"github.com/pthm/leeroy/azbench/config"
	"github.com/pthm/leeroy/azworld/world"
)

//go:embed prompt.tmpl
var promptText string

var promptTmpl = template.Must(template.New("prompt").Parse(promptText))

var skillNames = map[string]string{"164": "Blacksmithing", "165": "Leatherworking", "171": "Alchemy", "182": "Herbalism",
	"185": "Cooking", "186": "Mining", "197": "Tailoring", "202": "Engineering", "333": "Enchanting", "356": "Fishing",
	"393": "Skinning", "129": "First Aid", "755": "Jewelcrafting", "773": "Inscription"}

// goalText: what the prompt asks for.
func goalText(goal string, mins int) string {
	kind, n, _ := strings.Cut(goal, ":")
	switch kind {
	case "level":
		return "reach character level " + n
	case "quests":
		return "complete " + n + " quests (accepted, objectives done, and turned in to the quest ender)"
	case "money":
		return "hold " + n + " copper"
	case "item":
		return "own item " + n + " (item_template entry)"
	case "quality":
		return "own any item of quality " + n + " or better (3 = rare)"
	case "score":
		return fmt.Sprintf("progress as much as you can in %d minutes. You choose your own adventure: where to go, what to\nfight, which quests to take, who to play with", mins)
	case "achpoints":
		return "earn as many achievement points as you can"
	case "skill":
		sk, lvl, _ := strings.Cut(n, ":")
		name := skillNames[sk]
		if name == "" {
			name = "skill " + sk
		}
		return fmt.Sprintf("raise %s to %s: learn it from a trainer and practise it", name, lvl)
	case "quest":
		title := ""
		if r, err := world.Rows(world.World(), "SELECT LogTitle FROM quest_template WHERE ID = ?", n); err == nil && len(r) > 0 {
			title = r[0][0]
		}
		return fmt.Sprintf("complete the quest %q (quest %s): accept it, do its objectives, and turn it in", title, n)
	}
	return goal
}

func scoreText(goal string, mins int) (score, stop string) {
	kind, _, _ := strings.Cut(goal, ":")
	switch kind {
	case "achpoints":
		return fmt.Sprintf(`Your score is the total points of the achievements you earn during the next %d minutes
(ones already earned don't count). There is no finish line: keep earning until time runs out. Achievement
names, points and criteria are in the world database (azworld sql; see AGENTS.md); me.achievements() shows your progress.`, mins),
			"Do not stop early: keep earning points until the time runs out."
	case "score":
		return fmt.Sprintf(`HOW YOU ARE JUDGED: every 15 seconds the judge reads your character from the server database. Your
progress is the XP you've earned since the start (kills, quests, exploring: the game already gives more for harder
content). Gear, money, talents and everything else aren't scored: they're how you earn XP faster.
YOUR SCORE is the average of two rates, in XP per hour: your rate over the whole %d minutes, and
your best rate over any 15-minute stretch. Sustained progress counts, and so does finding a better way to play:
time spent trying a new strategy lowers your overall rate but not your best stretch. Deaths, stalls and time spent
stuck cost you through the time they lose. There is no finish line.
FACTION SCORE: every player on this server is on your faction (Alliance), and the faction's score is the sum of
all players' scores. It is reported alongside yours; your own score is still yours alone.
The standings command shows where every player in this run stands right now (rank, score, recent rate, level) and
the faction total. It also shows each player's role and their damage dealt, damage taken and healing done this
run: the measure of how each plays their role. That's not scored: it's for you to see how you and others play.`, mins),
			"Do not stop early: keep improving your score until the time runs out."
	}
	return fmt.Sprintf(`You are scored on wall-clock time until an external judge sees the goal met; if time
runs out (%d minutes), your progress toward it is the score.`, mins),
		"Do not stop until the goal is met: confirm it yourself with wow before finishing."
}

// prompt renders an agent's task prompt.
func prompt(s *config.Scenario, b *config.Bench, a config.Agent, repo string) (string, error) {
	race, _ := config.ID(config.Races, a.Race)
	identity := fmt.Sprintf("Your character %s is already logged in and waiting.", a.Char)
	if sp, ok := b.Specs[a.Spec]; ok {
		identity = fmt.Sprintf(`You are %s, a %s %s. Your role is %s: you should optimise for %s.
Your key stats are %s. Build your character for that: your talents (talents.list, talents.learn), the spells
you train and the gear you choose. %s is already logged in and waiting.`, a.Char, config.RaceName(race), sp.Title,
			sp.Role, sp.Optimise, sp.Stats, a.Char)
	}
	var roster []string // the other players with a spec: "- Char: Warrior - Protection (Tank)"
	for _, o := range s.Agents {
		if sp, ok := b.Specs[o.Spec]; ok && o.Char != a.Char {
			roster = append(roster, fmt.Sprintf("- %s: %s (%s)", o.Char, sp.Title, sp.Role))
		}
	}
	notice, err := s.NoticeText()
	if err != nil {
		return "", err
	}
	score, stop := scoreText(s.Goal, s.Minutes)
	var out strings.Builder
	err = promptTmpl.Execute(&out, map[string]any{"Identity": identity, "GoalText": goalText(s.Goal, s.Minutes),
		"Hint": strings.TrimSpace(s.Hint), "ScoreText": score, "StopText": stop, "Repo": repo,
		"Roster": strings.Join(roster, "\n"), "Notice": notice, "Minutes": s.Minutes})
	return out.String(), err
}
