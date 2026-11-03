package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/pthm/leeroy/internal/bench/account"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pthm/leeroy/internal/bench/config"
	"github.com/pthm/leeroy/internal/bench/judge"
	"github.com/pthm/leeroy/internal/bench/run"
)

// repoRoot: the LEEROY checkout: above the working directory or the binary (bin/), the first with suite/specs.yaml.
func repoRoot() string {
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		if e, err := filepath.EvalSymlinks(exe); err == nil {
			exe = e
		}
		starts = append(starts, filepath.Dir(exe))
	}
	for _, d := range starts {
		for ; ; d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, "suite/specs.yaml")); err == nil {
				return d
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	die("can't find the LEEROY checkout (suite/specs.yaml) above here or the binary")
	return ""
}

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "azbench: "+f+"\n", a...)
	os.Exit(1)
}

func check(err error) {
	if err != nil {
		die("%v", err)
	}
}

func readFile(f string) string {
	if f == "" {
		return ""
	}
	b, err := os.ReadFile(f)
	check(err)
	return string(b)
}

// models: the --model list.
func models(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// benchCommand runs the benchmark's own commands; false if cmd isn't one.
func benchCommand(cmd string, args []string) bool {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	minutes := fs.Int("minutes", 0, "override the scenario's minutes")
	dry := fs.Bool("dry-run", false, "print the plan (and the first prompt) without starting anything")
	message := fs.String("message", "", "file: the message an agent's resumed conversation starts with ($LEFT = minutes left)")
	noBuild := fs.Bool("no-build", false, "don't rebuild bin/ first")
	reset := fs.Bool("reset", false, "run: start from scratch: delete the task's characters, move its agents' workspaces to var/workspaces/.old, restart the worldserver")
	monitorEvery := fs.Duration("monitor", 0, "run, resume: sample the machine (CPU, memory, disk, network, its processes) this often into the run's server/resources.jsonl, e.g. 10s")
	port := fs.Int("port", 0, "run: the first session's port (default 8090), to run beside another run")
	task := fs.String("task", "", "publish: the task the runs belong to on the site (default: the run's, from run.json)")
	out := fs.String("out", "", "publish: where to write (default: site/src/data/runs)")
	model := fs.String("model", "", "run, check: only these of the task's models (comma-separated keys of suite/models.yaml)")
	spec := fs.String("spec", "", "kit gen: the spec the gear is for (a key of suite/specs.yaml; default the class's usual)")
	quality := fs.String("quality", "uncommon", "kit gen: the best quality of gear: uncommon (green), rare (blue) or epic")
	notFrom := fs.Int("not-from", 0, "kit gen: a map (dungeon) whose loot and quests the kit leaves out: the one it's for")
	maxIlvl := fs.Int("max-ilvl", 0, "kit gen: the highest item level (default the level + 10)")
	race := fs.String("race", "human", "kit gen: the race (some quests and items are for some races)")
	class := fs.String("class", "warrior", "gm: the GM character's class (warrior: Devgm on 8112; another class: Dev<class> on 8113)")
	parse := func(n int, usage string) []string {
		var pos []string // positional arguments, flags anywhere
		for len(args) > 0 {
			fs.Parse(args)
			if fs.NArg() == 0 {
				break
			}
			pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
		}
		if len(pos) < n {
			die("usage: azbench %s %s", cmd, usage)
		}
		return pos
	}
	switch cmd {
	case "check":
		pos := parse(1, "<task.yaml> [--model m,...]")
		repo := repoRoot()
		b, err := config.LoadBench(filepath.Join(repo, "suite"))
		check(err)
		ss, err := b.LoadTask(pos[0], models(*model))
		check(err)
		var reports []any
		for _, s := range ss {
			reports = append(reports, run.CheckScenario(repo, s))
		}
		encoded, err := json.MarshalIndent(reports, "", "  ")
		check(err)
		fmt.Println(string(encoded))
		return true
	case "run":
		pos := parse(1, "<task.yaml> [--model m,...] [--minutes n] [--port n] [--monitor 10s] [--dry-run] [--no-build] [--reset]")
		repo := repoRoot()
		b, err := config.LoadBench(filepath.Join(repo, "suite"))
		check(err)
		ss, err := b.LoadTask(pos[0], models(*model))
		check(err)
		if !*dry && !*noBuild {
			check(run.Build(repo))
		}
		for _, s := range ss { // one after another: a task played apart doesn't share the world
			if *minutes > 0 {
				s.Minutes = *minutes
			}
			if *reset && !*dry {
				fmt.Fprintln(os.Stderr, "reset: deleting the characters, moving the workspaces aside, restarting the worldserver")
				check(run.Fresh(repo, s))
			}
			r, err := run.New(b, s, run.Options{Repo: repo, BasePort: *port, Monitor: *monitorEvery})
			check(err)
			if *dry {
				fmt.Print(r.Plan())
				continue
			}
			check(r.Run(false))
			fmt.Println(r.Dir())
		}
	case "resume":
		pos := parse(1, "<run dir> --message <file> [--minutes n] [--no-build]")
		repo := repoRoot()
		b, err := config.LoadBench(filepath.Join(repo, "suite"))
		check(err)
		s, err := b.LoadScenario(filepath.Join(pos[0], "scenario.yaml"))
		check(err)
		if *minutes > 0 { // a new total length: more time for a run that's still going, or a run that ended
			s.Minutes = *minutes
		}
		dir, _ := filepath.Abs(pos[0])
		r, err := run.New(b, s, run.Options{Repo: repo, Dir: dir, Monitor: *monitorEvery})
		check(err)
		if *message == "" {
			die("resume needs --message <file>: what each agent's conversation picks up with ($LEFT = minutes left)")
		}
		check(run.Running(dir)) // before the build: bin/ is in use while it runs
		if !*noBuild {
			check(run.Build(repo))
		}
		check(r.Resume(readFile(*message)))
	case "pause":
		pos := parse(1, "<run dir>")
		out, err := run.Control(pos[0], "POST", "/pause", nil)
		check(err)
		fmt.Print(string(out))
	case "status":
		pos := parse(1, "<run dir>")
		out, err := run.Status(pos[0])
		check(err)
		fmt.Print(string(out))
	case "restart":
		pos := parse(2, "<run dir> <agent> --message <file>")
		out, err := run.Control(pos[0], "POST", "/restart", map[string]string{"agent": pos[1], "message": readFile(*message)})
		check(err)
		fmt.Print(string(out))
	case "maintenance":
		pos := parse(3, "<run dir> <minutes> <message>")
		mins, err := strconv.ParseFloat(pos[1], 64)
		check(err)
		out, err := run.Control(pos[0], "POST", "/maintenance", map[string]any{"minutes": mins, "message": strings.Join(pos[2:], " ")})
		check(err)
		fmt.Print(string(out))
	case "reset":
		pos := parse(1, "<Character>...")
		check(run.Reset(pos))
	case "account":
		pos := parse(2, "<user> <password> [GM level]")
		gm := 0
		if len(pos) > 2 {
			gm, _ = strconv.Atoi(pos[2])
		}
		check(account.Ensure(pos[0], pos[1], gm))
		fmt.Printf("account %s ready (GM level %d)\n", strings.ToUpper(pos[0]), gm)
	case "gm":
		pos := parse(0, "[x y z [map]] [--class mage]")
		check(run.GM(repoRoot(), *class, pos))
	case "apidoc":
		check(run.APIDoc(repoRoot()))
	case "publish":
		pos := parse(1, "<run dir>... [--task name] [--out dir]")
		root := repoRoot()
		if *out == "" {
			*out = filepath.Join(root, "site/src/data/runs")
		}
		for _, d := range pos {
			f, err := publish(root, d, *task, *out)
			check(err)
			fmt.Println(f)
		}
	case "kit":
		pos := parse(2, "gen <class> <level> [--spec s] [--race r] | save <Char> | apply <Char> <kit>")
		switch {
		case pos[0] == "gen" && len(pos) == 3:
			class, err := config.ID(config.Classes, pos[1])
			check(err)
			raceID, err := config.ID(config.Races, *race)
			check(err)
			level, err := strconv.Atoi(pos[2])
			check(err)
			q := slices.Index([]string{"uncommon", "rare", "epic"}, *quality) + 2
			if q < 2 {
				die("--quality: uncommon, rare or epic")
			}
			if *maxIlvl == 0 {
				*maxIlvl = level + 10
			}
			k, notes, err := run.GenKit(class, raceID, *spec, level, q, *maxIlvl, *notFrom)
			check(err)
			fmt.Print(run.KitYAML(k, notes, fmt.Sprintf("azbench kit gen %s %d --spec %s --race %s --quality %s --max-ilvl %d --not-from %d: gear the\ncharacter could get by playing (drops, quest rewards, crafted), bags from vendors; edit freely.", pos[1], level, *spec, *race, *quality, *maxIlvl, *notFrom)))
		case pos[0] == "apply" && len(pos) == 3: // for testing a kit: on an offline character
			repo := repoRoot()
			b, err := config.LoadBench(filepath.Join(repo, "suite"))
			check(err)
			k, err := b.LoadKit(strings.TrimSuffix(filepath.Base(pos[2]), ".yaml"))
			check(err)
			check(run.ApplyKit(k, pos[1]))
		case pos[0] == "save" && len(pos) == 2:
			k, notes, err := run.SaveKit(pos[1])
			check(err)
			fmt.Print(run.KitYAML(k, notes, "azbench kit save "+pos[1]+": the character as it is."))
		default:
			die("usage: azbench kit gen <class> <level> [--spec s] [--race r] | save <Char> | apply <Char> <kit>")
		}
	case "prices":
		check(judge.Prices(filepath.Join(repoRoot(), "internal/bench/judge/prices.json")))
	default:
		return false
	}
	return true
}
