// azbench: the LEEROY benchmark. One binary for running benchmarks from YAML configs (bench/scenarios,
// bench/matrices), judging and reporting them, and the agents' standings (bench/agent/bin/standings is a link to
// it: it acts as the one it's called by). The agents' other tools are the game's (wow, in azeroth-sdk) and the
// world's (azworld).
//
//	azbench run <scenario.yaml> [--minutes n] [--dry-run]    a team (or solo) run: sessions, judges, agents
//	azbench matrix <matrix.yaml> [--dry-run]                 a grid of solo runs
//	azbench resume <run dir> --message <file> [--minutes n]  continue a paused run
//	azbench pause|status <run dir>                           control a running run
//	azbench restart <run dir> <agent> --message <file>       bring one agent back mid-run
//	azbench judge <Char> <goal> [flags]                      score one character (judge/README.md)
//	azbench report <run dir>                                 the report card and team.json
//	azbench compare <run dir>...                             players of several runs by runtime and model
//	azbench signals <Char>                                   a character's progress signals
//	azbench reset <Char>...                                  delete characters for a fresh run (restarts the worldserver)
//	azbench gm [x y z [map]] [--class mage]                  a GM dev character: Devgm on 8112, Dev<class> on 8113
//	azbench apidoc                                           regenerate the API reference from a live session
//	azbench prices                                           refresh the price table (judge/prices.json) from models.dev
//	azbench standings                                        the agents' standings
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pthm/leeroy/azbench/judge"
	"github.com/pthm/leeroy/azbench/tools"
)

func main() {
	args := os.Args[1:]
	cmd := filepath.Base(os.Args[0])
	if cmd == "azbench" || cmd == "main" {
		if len(args) == 0 {
			usage()
		}
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "judge":
		os.Exit(judge.Main(args))
	case "report", "signals", "compare":
		os.Exit(judge.Main(append([]string{cmd}, args...)))
	case "standings":
		os.Exit(tools.Standings(args))
	}
	if benchCommand(cmd, args) {
		return
	}
	usage()
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: azbench run|matrix|resume|pause|status|restart|judge|report|compare|signals|reset|gm|apidoc|prices|standings ...")
	os.Exit(2)
}
