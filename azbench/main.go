// azbench: the LEEROY benchmark. One binary for running benchmarks from YAML configs (bench/scenarios,
// bench/matrices), judging and reporting them, and the agents' own tools (wow, standings, worlddb, party: the files
// in bench/agent/bin are links to it, and it acts as the one it's called by).
//
//	azbench run <scenario.yaml> [--minutes n] [--dry-run]    a team (or solo) run: sessions, judges, agents
//	azbench matrix <matrix.yaml> [--dry-run]                 a grid of solo runs
//	azbench resume <run dir> --message <file> [--minutes n]  continue a paused run
//	azbench pause|status <run dir>                           control a running run
//	azbench restart <run dir> <agent> --message <file>       bring one agent back mid-run
//	azbench judge <Char> <goal> [flags]                      score one character (judge/README.md)
//	azbench report <run dir>                                 the report card and team.json
//	azbench signals <Char>                                   a character's progress signals
//	azbench reset <Char>...                                  delete characters for a fresh run (restarts the worldserver)
//	azbench gm [x y z [map]]                                 the GM dev character on port 8112
//	azbench apidoc                                           regenerate the API reference from a live session
//	azbench wow|standings|worlddb|party ...                  the agents' tools
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
	case "report", "signals", "party":
		os.Exit(judge.Main(append([]string{cmd}, args...)))
	case "wow":
		os.Exit(tools.Wow(args))
	case "standings":
		os.Exit(tools.Standings(args))
	case "worlddb":
		os.Exit(tools.Worlddb(args))
	}
	if benchCommand(cmd, args) {
		return
	}
	usage()
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: azbench run|matrix|resume|pause|status|restart|judge|report|signals|reset|gm|apidoc|wow|standings|worlddb|party ...")
	os.Exit(2)
}
