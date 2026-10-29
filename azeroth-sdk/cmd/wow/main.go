// wow: the command line for an azsdk session: run Lua in it, load scripts, read and wait on its events. Everything
// an agent does in the game goes through it. Its replies are printed the way jq -c prints them.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/pthm/leeroy/azeroth-sdk/jqjson"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const wowUsage = `wow 'return me.level()'              evaluate Lua in the live session (shared with your running scripts), print JSON
wow < snippet.lua                   same, from stdin
wow -f grind.lua                    (re)load a script file
wow status                          what you'd see on screen: you, your fight, NPCs and mobs around you, quests, what to improve
wow tasks                           your running script tasks: state, status, what each is waiting for
wow events [--types chat,quest_*] [--last 50] [--since seq] [--match text]
                                    the event log (chat, combat, loot, quests, action results, errors, script logs)
wow wait [--types chat:whisper,death] [--match text] [seconds]
                                    block until a matching event happens (default: your subscription), max 600s
wow subscribe [types | +types | -types]
                                    what wakes you (wow wait) and what replies show in full: show it, replace it,
                                    or add/remove types. Chat by kind: chat:whisper, chat:npc_*, chat (all)
wow answer <id> <choice>            answer a script's ask() (an "attention" event)
wow -w 60 'lua'                     sleep 60s first, then evaluate
Every eval reply carries "events": what happened since your last call (counts by type, important ones in full).
Subscribed chat, invites, trades, script questions, script errors and slow ticks are also printed to stderr
("wow: chat ...", "wow: script_errors ..."), so a reply piped through jq or cut can't hide them.
Session: the azsdk daemon on port $WOW_PORT (default 8090).
`

var digits = regexp.MustCompile(`^[0-9]+$`)

// rpc calls the session and returns jq's view of the reply: the result or {"error": message}; ok is false when
// there was no reply (the error went to stderr, like curl's).
func rpc(method string, params any) (v any, ok bool) {
	body := `{"jsonrpc":"2.0","id":1,"method":` + jqjson.Compact(method) + `,"params":` + jqjson.Compact(params) + `}`
	port := os.Getenv("WOW_PORT")
	if port == "" {
		port = "8090"
	}
	resp, err := (&http.Client{Timeout: 11 * time.Minute}).Post("http://127.0.0.1:"+port+"/rpc", "application/json", strings.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "wow: can't reach the session on 127.0.0.1:%s (%v)\n", port, err)
		return nil, false
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	vs, _ := jqjson.ParseAll(data)
	if len(vs) == 0 {
		return nil, false
	}
	if e := jqjson.Field(vs[0], "error"); jqjson.Truthy(e) {
		o := jqjson.NewObj()
		o.Set("error", jqjson.Field(e, "message"))
		return o, true
	}
	return jqjson.Field(vs[0], "result"), true
}

// out prints a reply line, or nothing when there was no reply.
func out(v any, ok bool) {
	if ok {
		fmt.Println(jqjson.Compact(v))
	}
}

// evalLua runs code and echoes what needs the agent's eye to stderr (piping the reply through jq can't hide it).
func evalLua(code string) int {
	p := jqjson.NewObj()
	p.Set("code", code)
	r, ok := rpc("script.eval", p)
	if ok {
		fmt.Println(jqjson.Compact(r))
	} else {
		fmt.Println() // printf '%s\n' "" with no reply
	}
	if imp, ok := jqjson.Field(jqjson.Field(r, "events"), "important").([]any); ok {
		for _, e := range imp {
			t, _ := jqjson.Field(e, "type").(string)
			d := jqjson.Field(e, "data")
			switch t {
			case "chat":
				fmt.Fprintf(os.Stderr, "wow: %s %s from %s: %s\n", t, jqjson.Interp(jqjson.Field(d, "kind")), jqjson.Interp(jqjson.Field(d, "from")), jqjson.Interp(jqjson.Field(d, "text")))
			case "party_invite", "trade", "attention":
				fmt.Fprintf(os.Stderr, "wow: %s %s\n", t, jqjson.Compact(d))
			case "slow_tick":
				fmt.Fprintf(os.Stderr, "wow: slow_tick %s\n", jqjson.Compact(d))
			}
		}
	}
	if se := jqjson.Field(r, "script_errors"); se != nil {
		last, _ := jqjson.Field(se, "last").(string)
		last, _, _ = strings.Cut(last, "\n") // the traceback is in wow tasks and the script_error event
		fmt.Fprintf(os.Stderr, "wow: script_errors %s, last: %s\n", jqjson.Interp(jqjson.Field(se, "count")), last)
	}
	return 0
}

// params turns --types a,b --last n --since n --limit n --match s [seconds] into the query object.
func params(args []string) (*jqjson.Obj, bool) {
	p := jqjson.NewObj()
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			i++
			if i < len(args) {
				return args[i]
			}
			return ""
		}
		switch a {
		case "--types":
			v := next()
			t := []any{}
			if v != "" {
				for _, s := range strings.Split(v, ",") {
					t = append(t, s)
				}
			}
			p.Set("types", t)
		case "--last", "--since", "--limit":
			v := next()
			if vs, err := jqjson.ParseAll([]byte(v)); err != nil || len(vs) != 1 {
				fmt.Fprintf(os.Stderr, "wow: %s needs a number\n", a)
				return nil, false
			} else {
				p.Set(a[2:], vs[0])
			}
		case "--match":
			p.Set("match", next())
		default:
			if !digits.MatchString(a) {
				fmt.Fprintf(os.Stderr, "unknown option %s\n", a)
				return nil, false
			}
			n, _ := strconv.Atoi(a)
			p.Set("timeout_s", n)
		}
	}
	return p, true
}

// lines prints one event per line: {seq, ago_s, type, ...data}
func lines(v any) {
	items := []any{v}
	if a, ok := v.([]any); ok {
		items = a
	}
	now := float64(time.Now().UnixMicro()) / 1000 // jq's now*1000
	for _, e := range items {
		if !jqjson.Truthy(jqjson.Field(e, "seq")) {
			fmt.Println(jqjson.Compact(e))
			continue
		}
		if jqjson.Field(e, "t") == nil { // jq: now*1000 - null fails, and the line is dropped
			fmt.Fprintf(os.Stderr, "jq: error (at <stdin>:1): number (%s) and null (null) cannot be subtracted\n", strconv.FormatFloat(now, 'f', -1, 64))
			continue
		}
		o := jqjson.NewObj()
		o.Set("seq", jqjson.Field(e, "seq"))
		o.Set("ago_s", math.Floor((now-jqjson.Num(jqjson.Field(e, "t")))/1000))
		o.Set("type", jqjson.Field(e, "type"))
		if d, ok := jqjson.Field(e, "data").(*jqjson.Obj); ok {
			for _, k := range d.Keys() {
				o.Set(k, d.Get(k))
			}
		}
		fmt.Println(jqjson.Compact(o))
	}
}

// Wow is the agents' session command (the usage above).
func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch arg(0) {
	case "--help", "-h", "help":
		fmt.Print(wowUsage)
		return 0
	case "status":
		return evalLua("return me.status()")
	case "tasks":
		return evalLua("return task.list()")
	case "answer":
		return evalLua("return answer(" + arg(1) + ", " + jqjson.Compact(arg(2)) + ")")
	case "events":
		p, ok := params(args[1:])
		if !ok {
			return 1
		}
		if r, ok := rpc("events.query", p); ok {
			lines(r)
		}
		return 0
	case "wait":
		p, ok := params(args[1:])
		if !ok {
			return 1
		}
		r, ok := rpc("events.wait", p)
		if !ok {
			return 0
		}
		h := jqjson.NewObj()
		h.Set("timed_out", jqjson.Field(r, "timed_out"))
		h.Set("seq", jqjson.Field(r, "seq"))
		fmt.Println(jqjson.Compact(h))
		evs := jqjson.Field(r, "events")
		if !jqjson.Truthy(evs) {
			evs = []any{}
		}
		lines(evs)
		return 0
	case "subscribe":
		v := arg(1)
		switch {
		case v == "":
			out(rpc("events.subscribe", jqjson.NewObj()))
		case v[0] == '+' || v[0] == '-':
			r, _ := rpc("events.subscribe", jqjson.NewObj())
			cur, _ := r.([]any)
			var t []any
			for _, s := range strings.Split(v[1:], ",") {
				t = append(t, s)
			}
			types := []any{}
			if v[0] == '+' {
				types = append(append(types, cur...), minus(t, cur)...)
			} else {
				types = append(types, minus(cur, t)...)
			}
			p := jqjson.NewObj()
			p.Set("types", types)
			out(rpc("events.subscribe", p))
		default:
			t := []any{}
			for _, s := range strings.Split(v, ",") {
				t = append(t, s)
			}
			p := jqjson.NewObj()
			p.Set("types", t)
			out(rpc("events.subscribe", p))
		}
		return 0
	case "-w":
		if !digits.MatchString(arg(1)) {
			fmt.Println("usage: wow -w <seconds> <lua>")
			return 1
		}
		n, _ := strconv.Atoi(arg(1))
		time.Sleep(time.Duration(min(n, 600)) * time.Second)
		args = args[2:]
	case "-c": // legacy: wait for chat
		if !digits.MatchString(arg(1)) {
			fmt.Println("usage: wow -c <seconds> <lua>")
			return 1
		}
		p := jqjson.NewObj()
		p.Set("types", []any{"chat"})
		p.Set("timeout_s", json.Number(arg(1)))
		rpc("events.wait", p)
		args = args[2:]
	}
	var code string
	switch {
	case arg(0) == "-f":
		f, _ := filepath.Abs(arg(1))
		code = `return dofile("` + f + `")`
	case arg(0) != "":
		code = arg(0)
	default:
		b, _ := io.ReadAll(os.Stdin)
		code = string(bytes.TrimRight(b, "\n")) // like $(cat)
	}
	return evalLua(code)
}

// minus is jq's array subtraction: a without any element of b.
func minus(a, b []any) []any {
	out := []any{}
	for _, x := range a {
		keep := true
		for _, y := range b {
			if jqjson.Compact(x) == jqjson.Compact(y) {
				keep = false
			}
		}
		if keep {
			out = append(out, x)
		}
	}
	return out
}
