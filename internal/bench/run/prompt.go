package run

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"
	"text/template"

	"github.com/pthm/leeroy/internal/bench/config"
	"github.com/pthm/leeroy/world"
)

//go:embed prompt.tmpl
var promptText string

var promptTmpl = template.Must(template.New("prompt").Parse(promptText))

var skillNames = map[string]string{
	"164": "Blacksmithing", "165": "Leatherworking", "171": "Alchemy", "182": "Herbalism",
	"185": "Cooking", "186": "Mining", "197": "Tailoring", "202": "Engineering", "333": "Enchanting", "356": "Fishing",
	"393": "Skinning", "129": "First Aid", "755": "Jewelcrafting", "773": "Inscription",
}

// goalText: what the prompt asks for (a checkpoints goal: every checkpoint, listed).
func goalText(goal string, mins int, cps []config.Checkpoint) string {
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
		return fmt.Sprintf("progress as much as you can in %d minutes. You choose your own adventure: where to go, what to\nfight, which quests to take", mins)
	case "achievement":
		name, desc := "achievement "+n, ""
		if r, err := world.Rows(world.World(), "SELECT name, description FROM wowbench.achievement WHERE id = ?", n); err == nil && len(r) > 0 {
			name, desc = r[0][0], " ("+strings.TrimSuffix(r[0][1], ".")+")"
		}
		return fmt.Sprintf("earn the achievement %s%s, achievement %s", name, desc, n)
	case "achpoints":
		return "earn as many achievement points as you can"
	case "kills":
		return fmt.Sprintf("kill %s creatures", n)
	case "duels":
		return fmt.Sprintf("fight %s duels against your opponent, one after another, and win as many as you can. Either of\nyou can make each challenge (duel.challenge, duel.accept; players.md, \"Duels\")", n)
	case "skill":
		sk, lvl, _ := strings.Cut(n, ":")
		name := skillNames[sk]
		if name == "" {
			name = "skill " + sk
		}
		return fmt.Sprintf("raise %s to %s: learn it from a trainer and practise it", name, lvl)
	case "checkpoints":
		var each []string
		for _, c := range cps {
			if c.Zone != 0 {
				each = append(each, fmt.Sprintf("travel to %s (zone %d)", c.Name, c.Zone))
			} else {
				each = append(each, fmt.Sprintf("kill %s (creature %d)", c.Name, c.Kill))
			}
		}
		return "do each of these, in any order: " + strings.Join(each, "; ")
	case "zone":
		name := "zone " + n
		if r, err := world.Rows(world.Bench(), "SELECT name FROM area WHERE id = ?", n); err == nil && len(r) > 0 {
			name = r[0][0] + " (zone " + n + ")"
		}
		return "travel to " + name
	case "quest":
		title := ""
		if r, err := world.Rows(world.World(), "SELECT LogTitle FROM quest_template WHERE ID = ?", n); err == nil && len(r) > 0 {
			title = r[0][0]
		}
		title = strings.Join(strings.Fields(title), " ") // the database has 'Wanted:  "Hogger"'
		return fmt.Sprintf("complete the quest %s (quest %s): accept it, do its objectives, and turn it in", title, n)
	}
	return goal
}

func scoreText(goal string, cps []config.Checkpoint) (score, stop string) {
	kind, _, _ := strings.Cut(goal, ":")
	if len(cps) > 0 && kind != "checkpoints" {
		names := make([]string, 0, len(cps))
		for _, c := range cps {
			names = append(names, c.Name)
		}
		return `You are scored on the time until the goal is met: your score is 3600 / seconds to the goal (1 = met in an
hour). Along the way are checkpoints, each a kill: ` + strings.Join(names, ", ") + `. If time runs out first, each checkpoint
done counts for a share of the score of finishing just as the clock ends, so any finish beats any unfinished run.`,
			"Do not stop until the goal is met: confirm it yourself with wow before finishing."
	}
	switch kind {
	case "achpoints":
		return `Your score is the total points of the achievements you earn during the run (ones already earned don't
count). Achievement names, points and criteria are in the world database (azworld sql, sdk.md);
me.achievements() shows your progress.`,
			"Do not stop early: keep earning points until the time runs out."
	case "score":
		return `HOW YOU ARE SCORED: your progress is the XP you've earned since the start of the run, from any source
(kills, quests, exploring). Nothing else is scored. YOUR SCORE is the average of two rates, in XP per hour: your rate over the whole run, and your best
rate over any 15-minute stretch of it.
FACTION SCORE: every player on this server is on your faction (Alliance); the faction's score, reported alongside
yours, is the sum of all players' scores. standings shows every player's score, rates, level and role, their damage
dealt, damage taken and healing done (not scored), and the faction total.`,
			"Do not stop early: keep improving your score until the time runs out."
	case "duels":
		return `You are scored on the duels you win. A duel counts once it is decided: one player is brought to 1 health
or yields, or stays over 50 yards from the duel flag for 10 seconds; that player loses. Declined or withdrawn
challenges don't count. Health, mana and cooldowns are not restored between duels.`,
			"Do not stop until all the duels are decided or the time runs out."
	case "checkpoints":
		return `You are scored on the time until the last of them is done: your score is 3600 / seconds to the last (1 = all
done in an hour). If time runs out first, each one done counts for a share of the score of finishing just as the
clock ends, so any finish beats any unfinished run.`,
			"Do not stop until every one is done: confirm it yourself with wow before finishing."
	case "achievement", "quest", "item", "quality", "zone", "kills":
		return `You are scored on the time until the goal is met: your score is 3600 / seconds to the goal (1 = met in an
hour). If time runs out first, your score is 0.`,
			"Do not stop until the goal is met: confirm it yourself with wow before finishing."
	}
	return `You are scored on the time until the goal is met; if time runs out, your progress toward it is the score.`,
		"Do not stop until the goal is met: confirm it yourself with wow before finishing."
}

// hardcoreText: a hardcore run's scoring. The first death ends the run, so a binary goal is scored as before but a
// death before it is a 0, and anything else is scored on the total earned by then.
func hardcoreText(goal, score, stop string) (scoreText, stopText string) {
	const rule = "HARDCORE: your run ends the first time your character dies; nothing after that counts.\n"
	if kind, _, _ := strings.Cut(goal, ":"); slices.Contains([]string{"achievement", "quest", "item", "quality", "zone", "checkpoints"}, kind) {
		return rule + score + " Dying before the goal is a score of 0.", stop
	}
	return rule + `HOW YOU ARE SCORED: your progress is the XP you've earned since the start of the run, from any source
(kills, quests, exploring). YOUR SCORE is the total XP you've earned when the run ends, so it stops growing when you die.`,
		"Do not stop early: keep earning XP until the time runs out, and stay alive."
}

// prompt renders an agent's task prompt.
func prompt(s *config.Scenario, b *config.Bench, a config.Agent, repo string) (string, error) {
	race, _ := config.ID(config.Races, a.Race)
	class, _ := config.ID(config.Classes, a.Class)
	identity := fmt.Sprintf("You are %s, a %s %s.\n%s is already logged in and waiting.", a.Char, config.RaceName(race), config.ClassName(class), a.Char)
	if sp, ok := b.Specs[a.Spec]; ok {
		identity = fmt.Sprintf(`You are %s, a %s %s. Your role is %s: %s. The spec's key stats are %s.
%s is already logged in and waiting.`, a.Char, config.RaceName(race), sp.Title, sp.Role, sp.Optimise, sp.Stats, a.Char)
	}
	switch k := a.Kit; {
	case k != nil: // the facts of the kit, not how to use it
		identity += fmt.Sprintf("\nIt starts at level %d, with gear equipped and the class spells trained up to its level.", k.Level)
		if !k.Trained {
			identity = strings.TrimSuffix(identity, " and the class spells trained up to its level.") + "."
		}
	case s.Level > 0:
		identity += fmt.Sprintf("\nIt starts at level %d.", s.Level)
	case s.RequireFresh:
		identity += "\nIt is a new character, at level 1."
	}
	// a duel: the class and spec without the group role, and the one other player named as the opponent
	opponent := ""
	if kind, _, _ := strings.Cut(s.Goal, ":"); kind == "duels" {
		if sp, ok := b.Specs[a.Spec]; ok {
			identity = strings.Replace(identity, fmt.Sprintf(" Your role is %s: %s. The spec's key stats are %s.", sp.Role, sp.Optimise, sp.Stats), "", 1)
		}
		var others []config.Agent
		for _, o := range s.Agents {
			if o.Char != a.Char {
				others = append(others, o)
			}
		}
		if a.Opponent != "" {
			opponent = a.Opponent
		} else if len(others) == 1 {
			opponent = others[0].Char
		}
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
	score, stop := scoreText(s.Goal, s.Checkpoints)
	if s.Hardcore {
		score, stop = hardcoreText(s.Goal, score, stop)
	}
	kind, _, _ := strings.Cut(s.Goal, ":") // giving up is for a goal that can be out of reach, not a score to build
	giveUp := !slices.Contains([]string{"score", "achpoints", "duels"}, kind)
	var out strings.Builder
	err = promptTmpl.Execute(&out, map[string]any{
		"Identity": identity, "GoalText": goalText(s.Goal, s.Minutes, s.Checkpoints),
		"Hint": strings.TrimSpace(s.Hint), "ScoreText": score, "StopText": stop, "Repo": repo,
		"Roster": strings.Join(roster, "\n"), "Notice": notice, "Minutes": s.Minutes, "Opponent": opponent, "GiveUp": giveUp,
	})
	return out.String(), err
}
