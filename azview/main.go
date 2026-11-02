// azview: the LEEROY viewer. It reads recorder logs (the server's mod-recorder log, azsdk -record files, a bench
// run's records and transcripts) and shows a run, live or finished, on the map.
//
//	azview live [game log ...] [--run <dir>] [--port 8800] [--chars A,B] [--since 60m]   the viewer in the browser
//	(every command: [--data <dir>], the extracted game data; default $LEEROY_DATA or the nearest data/)
//	azview movecheck <game log...> [--chars A,B] [--from <ms>]                           audit recorded movement
package main

import (
	"fmt"
	"os"

	"github.com/pthm/leeroy/azworld/world"
)

func main() {
	os.Args = world.DataFlag(os.Args) // --data <dir>: the extracted game data, else $LEEROY_DATA or the nearest data/
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "live":
			runLive(os.Args[2:])
			return
		case "movecheck":
			runMovecheck(os.Args[2:])
			return
		}
	}
	fmt.Fprintln(os.Stderr, "usage: azview live|movecheck [args]  (see the top of each command's file, or azview/README.md)")
	os.Exit(2)
}
