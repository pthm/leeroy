// azworld answers questions about the game world from the server's databases and the extracted game data, as JSON:
// where an NPC stands, what a quest needs, where an item drops, safe routes, map images. Agents call it through
// bench/agent/bin/azworld; `azworld help` lists the commands. See world.Run.
//
//	azworld setup [-schema wowbench]   build the wowbench schema the queries read (client DBC data the world DB
//	                                   lacks, plus precomputed joins). Needs data/dbc, data/maps and admin access to
//	                                   the database on $LEEROY_DB_ADDR (default 127.0.0.1:3306); see world.Setup.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pthm/leeroy/azworld/world"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		fs := flag.NewFlagSet("setup", flag.ExitOnError)
		schema := fs.String("schema", "wowbench", "schema to (re)build; another name builds a copy to compare against")
		fs.Parse(os.Args[2:])
		// the repo root: above data/ (it has the server checkout, for DBCEnums.h)
		repo, _ := filepath.Abs(filepath.Dir(world.DataDir()))
		s, err := world.Setup(*schema, repo)
		if err != nil {
			fmt.Fprintln(os.Stderr, "azworld setup:", err)
			os.Exit(1)
		}
		fmt.Println(s)
		return
	}
	os.Exit(world.Run(os.Args[1:], os.Stdout))
}
