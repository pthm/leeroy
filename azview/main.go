// azview: the LEEROY viewer. It reads recorder logs (the server's mod-recorder log, azsdk -record files, a bench
// run's records and transcripts) and shows a run on the map.
//
//	azview live [log.jsonl ...] [--run <dir>] [--port 8800] [--chars A,B] [--since 60m]   live view in the browser
//	azview replay <run or team dir> [--out replay.html] ...                              self-contained replay page
//	azview movecheck [recorder.jsonl] [--chars A,B] [--from <ms>]                        audit recorded movement
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "live":
			runLive(os.Args[2:])
			return
		case "replay":
			runReplay(os.Args[2:])
			return
		case "movecheck":
			runMovecheck(os.Args[2:])
			return
		}
	}
	fmt.Fprintln(os.Stderr, "usage: azview live|replay|movecheck [args]  (see the top of each command's file, or azview/README.md)")
	os.Exit(2)
}
