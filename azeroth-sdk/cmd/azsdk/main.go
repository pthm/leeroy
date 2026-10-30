// azsdk: runs characters on an AzerothCore server and serves the agent API.
//
//	azsdk -config azsdk.json
//	azsdk -char Garrick -account agent1 [-password agent1] [-race 1 -class 1] [-listen 127.0.0.1:8090] [-data ./data]
//
// The second form runs one character without a config file (one daemon per agent, as LEEROY's bench does).
// -duration stops after that long (e.g. a short first login that only creates the character).
// Each character logs in (creating it if needed) and stays in the world until the daemon stops. Agents
// talk to http://<listen>/rpc (JSON-RPC 2.0); see api/.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/api"
	"github.com/pthm/leeroy/azeroth-sdk/session"
)

type character struct {
	Name      string `json:"name"`
	Account   string `json:"account"`
	Password  string `json:"password"`
	Race      uint8  `json:"race"`  // used only when the character has to be created
	Class     uint8  `json:"class"` // 〃
	Script    string `json:"script"`
	Workspace string `json:"workspace"`
}

type config struct {
	AuthServer string      `json:"auth_server"` // host:port of the authserver, e.g. 127.0.0.1:3724
	DataDir    string      `json:"data_dir"`    // maps/, mmaps/, vmaps/, dbc/ extracted from the server's data
	Listen     string      `json:"listen"`      // API address, default 127.0.0.1:8780
	Record     string      `json:"record"`      // optional event log, see -record
	Characters []character `json:"characters"`
}

func main() {
	path := flag.String("config", "azsdk.json", "config file (ignored with -char)")
	agent := flag.String("agent", "", "benchmark agent identity for diagnostics (defaults to character name)")
	var one character
	flag.StringVar(&one.Name, "char", "", "run this one character instead of a config file")
	flag.StringVar(&one.Account, "account", "", "account for -char")
	flag.StringVar(&one.Password, "password", "", "password for -char (default: the account name)")
	race := flag.Uint("race", 1, "race if -char has to be created")
	class := flag.Uint("class", 1, "class if -char has to be created")
	flag.StringVar(&one.Script, "script", "", "Lua script to load at login (-char)")
	flag.StringVar(&one.Workspace, "workspace", "", "directory Lua may read with dofile/loadfile")
	listen := flag.String("listen", "", "API address (overrides the config)")
	dataDir := flag.String("data", "", "data dir (overrides the config)")
	auth := flag.String("auth", "", "authserver host:port (overrides the config)")
	duration := flag.Duration("duration", 0, "stop after this long (0 = until interrupted)")
	events := flag.String("events", "", "keep each character's event stream in <dir>/<char>.events.jsonl (survives restarts)")
	record := flag.String("record", "", "append an event log (recorder JSONL: positions, combat, chat, API calls) to this file")
	flag.Parse()

	var cfg config
	if one.Name != "" {
		if one.Password == "" {
			one.Password = one.Account
		}
		one.Race, one.Class = uint8(*race), uint8(*class)
		cfg = config{AuthServer: "127.0.0.1:3724", DataDir: "./data", Characters: []character{one}}
	} else {
		raw, err := os.ReadFile(*path)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			log.Fatalf("%s: %v", *path, err)
		}
	}
	for flagVal, field := range map[string]*string{*listen: &cfg.Listen, *dataDir: &cfg.DataDir, *auth: &cfg.AuthServer, *record: &cfg.Record} {
		if flagVal != "" {
			*field = flagVal
		}
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8780"
	}

	var rec *session.Recorder
	if cfg.Record != "" {
		var err error
		if rec, err = session.OpenRecorder(cfg.Record); err != nil {
			log.Fatal(err)
		}
	}
	sessions := api.Sessions{}
	var running sync.WaitGroup
	for _, c := range cfg.Characters {
		if c.Workspace == "" && c.Script != "" {
			c.Workspace = filepath.Dir(c.Script)
		}
		if c.Script != "" {
			absolute, err := filepath.Abs(c.Script)
			if err != nil {
				log.Fatal(err)
			}
			c.Script = absolute
		}
		b := session.NewBot(c.Name, session.Config{
			Username: c.Account, Password: c.Password, AuthServer: cfg.AuthServer, CharacterName: c.Name,
			Race: c.Race, Class: c.Class, DataDir: cfg.DataDir, LuaScript: c.Script,
			LuaWorkspace: c.Workspace,
			AgentID:      *agent,
		})
		if *events != "" {
			os.MkdirAll(*events, 0o755)
			b.SetEventsFile(filepath.Join(*events, c.Name+".events.jsonl"))
		}
		b.SetRecorder(rec)
		sessions[c.Name] = b
		running.Add(1)
		go func() {
			defer running.Done()
			res := b.Run()
			log.Printf("%s: session ended: %s %s", c.Name, res.Status, res.Error)
		}()
	}

	go func() {
		log.Printf("agent API on http://%s/rpc (%d characters)", cfg.Listen, len(sessions))
		log.Fatal(http.ListenAndServe(cfg.Listen, api.Handler(sessions)))
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	ended := make(chan struct{})
	go func() { running.Wait(); close(ended) }()
	var timeout <-chan time.Time
	if *duration > 0 {
		timeout = time.After(*duration)
	}
	select {
	case <-stop:
	case <-timeout:
	case <-ended: // every session failed or ended (e.g. login rejected): exit non-zero
		os.Exit(1)
	}
	for _, b := range sessions {
		b.Stop()
	}
	running.Wait() // let sessions log out cleanly
}
