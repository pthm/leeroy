package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pthm/leeroy/azbench/config"
	"github.com/pthm/leeroy/azbench/judge"
	"github.com/pthm/leeroy/azbench/run"
)

// repoRoot: the LEEROY checkout: above the working directory or the binary (bin/), the first with bench/specs.yaml.
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
			if _, err := os.Stat(filepath.Join(d, "bench/specs.yaml")); err == nil {
				return d
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	die("can't find the LEEROY checkout (bench/specs.yaml) above here or the binary")
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

// benchCommand runs the benchmark's own commands; false if cmd isn't one.
func benchCommand(cmd string, args []string) bool {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	minutes := fs.Int("minutes", 0, "override the scenario's minutes")
	dry := fs.Bool("dry-run", false, "print the plan (and the first prompt) without starting anything")
	message := fs.String("message", "", "file: the message an agent's resumed conversation starts with ($LEFT = minutes left)")
	noBuild := fs.Bool("no-build", false, "don't rebuild bin/ first")
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
	case "run":
		pos := parse(1, "<scenario.yaml> [--minutes n] [--dry-run] [--no-build]")
		repo := repoRoot()
		b, err := config.LoadBench(filepath.Join(repo, "bench"))
		check(err)
		s, err := b.LoadScenario(pos[0])
		check(err)
		if *minutes > 0 {
			s.Minutes = *minutes
		}
		r, err := run.New(b, s, run.Options{Repo: repo})
		check(err)
		if *dry {
			fmt.Print(r.Plan())
			return true
		}
		if !*noBuild {
			check(run.Build(repo))
		}
		check(r.Run(false))
		fmt.Println(r.Dir())
	case "matrix":
		pos := parse(1, "<matrix.yaml> [--dry-run]")
		repo := repoRoot()
		b, err := config.LoadBench(filepath.Join(repo, "bench"))
		check(err)
		if !*dry && !*noBuild {
			check(run.Build(repo))
		}
		check(run.Matrix(b, pos[0], repo, *dry))
	case "resume":
		pos := parse(1, "<run dir> --message <file> [--no-build]")
		repo := repoRoot()
		b, err := config.LoadBench(filepath.Join(repo, "bench"))
		check(err)
		s, err := b.LoadScenario(filepath.Join(pos[0], "scenario.yaml"))
		check(err)
		dir, _ := filepath.Abs(pos[0])
		r, err := run.New(b, s, run.Options{Repo: repo, Dir: dir})
		check(err)
		if *message == "" {
			die("resume needs --message <file>: what each agent's conversation picks up with ($LEFT = minutes left)")
		}
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
		out, err := run.Control(pos[0], "GET", "/status", nil)
		check(err)
		fmt.Print(string(out))
	case "restart":
		pos := parse(2, "<run dir> <agent> --message <file>")
		out, err := run.Control(pos[0], "POST", "/restart", map[string]string{"agent": pos[1], "message": readFile(*message)})
		check(err)
		fmt.Print(string(out))
	case "reset":
		pos := parse(1, "<Character>...")
		check(run.Reset(pos))
	case "gm":
		pos := parse(0, "[x y z [map]] [--class mage]")
		check(run.GM(repoRoot(), *class, pos))
	case "apidoc":
		check(run.APIDoc(repoRoot()))
	case "prices":
		check(judge.Prices(filepath.Join(repoRoot(), "azbench/judge/prices.json")))
	default:
		return false
	}
	return true
}
