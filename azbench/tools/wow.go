package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
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
Subscribed chat, invites, trades and script questions are also printed to stderr ("wow: chat ..."),
with the minutes left in the run ("wow: 42 min left").
Session: the azsdk daemon on port $WOW_PORT (default 8090).
`

var digits = regexp.MustCompile(`^[0-9]+$`)

// rpc calls the session and returns jq's view of the reply: the result or {"error": message}; ok is false when
// there was no reply (the error went to stderr, like curl's).
func rpc(method string, params any) (v any, ok bool) {
	var pb strings.Builder
	enc(&pb, params)
	body := `{"jsonrpc":"2.0","id":1,"method":` + compact(method) + `,"params":` + pb.String() + `}`
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
	vs, _ := parseAll(data)
	if len(vs) == 0 {
		return nil, false
	}
	if e := field(vs[0], "error"); truthy(e) {
		o := newObj()
		o.set("error", field(e, "message"))
		return o, true
	}
	return field(vs[0], "result"), true
}

// out prints a reply line, or nothing when there was no reply.
func out(v any, ok bool) {
	if ok {
		fmt.Println(compact(v))
	}
}

// evalLua runs code and echoes what needs the agent's eye to stderr (piping the reply through jq can't hide it).
func evalLua(code string) int {
	p := newObj()
	p.set("code", code)
	r, ok := rpc("script.eval", p)
	if ok {
		fmt.Println(compact(r))
	} else {
		fmt.Println() // printf '%s\n' "" with no reply
	}
	if imp, ok := field(field(r, "events"), "important").([]any); ok {
		for _, e := range imp {
			t, _ := field(e, "type").(string)
			d := field(e, "data")
			switch t {
			case "chat":
				fmt.Fprintf(os.Stderr, "wow: %s %s from %s: %s\n", t, interp(field(d, "kind")), interp(field(d, "from")), interp(field(d, "text")))
			case "party_invite", "trade", "attention":
				fmt.Fprintf(os.Stderr, "wow: %s %s\n", t, compact(d))
			}
		}
	}
	if b := os.Getenv("WOWBENCH_BOARD"); b != "" {
		if data, err := os.ReadFile(filepath.Join(b, os.Getenv("WOWBENCH_CHAR")+".json")); err == nil {
			if vs, _ := parseAll(data); len(vs) > 0 {
				if m := field(vs[0], "minutes_left"); truthy(m) {
					fmt.Fprintf(os.Stderr, "wow: %s min left\n", interp(m))
				}
			}
		}
	}
	return 0
}

// params turns --types a,b --last n --since n --limit n --match s [seconds] into the query object.
func params(args []string) (*obj, bool) {
	p := newObj()
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
			p.set("types", t)
		case "--last", "--since", "--limit":
			v := next()
			if vs, err := parseAll([]byte(v)); err != nil || len(vs) != 1 {
				fmt.Fprintf(os.Stderr, "wow: %s needs a number\n", a)
				return nil, false
			} else {
				p.set(a[2:], vs[0])
			}
		case "--match":
			p.set("match", next())
		default:
			if !digits.MatchString(a) {
				fmt.Fprintf(os.Stderr, "unknown option %s\n", a)
				return nil, false
			}
			n, _ := strconv.Atoi(a)
			p.set("timeout_s", n)
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
		if !truthy(field(e, "seq")) {
			fmt.Println(compact(e))
			continue
		}
		if field(e, "t") == nil { // jq: now*1000 - null fails, and the line is dropped
			fmt.Fprintf(os.Stderr, "jq: error (at <stdin>:1): number (%s) and null (null) cannot be subtracted\n", strconv.FormatFloat(now, 'f', -1, 64))
			continue
		}
		o := newObj()
		o.set("seq", field(e, "seq"))
		o.set("ago_s", math.Floor((now-num(field(e, "t")))/1000))
		o.set("type", field(e, "type"))
		if d, ok := field(e, "data").(*obj); ok {
			for _, k := range d.keys {
				o.set(k, d.vals[k])
			}
		}
		fmt.Println(compact(o))
	}
}

// Wow is the agents' session command (the usage above).
func Wow(args []string) int {
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
		return evalLua("return answer(" + arg(1) + ", " + compact(arg(2)) + ")")
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
		h := newObj()
		h.set("timed_out", field(r, "timed_out"))
		h.set("seq", field(r, "seq"))
		fmt.Println(compact(h))
		evs := field(r, "events")
		if !truthy(evs) {
			evs = []any{}
		}
		lines(evs)
		return 0
	case "subscribe":
		v := arg(1)
		switch {
		case v == "":
			out(rpc("events.subscribe", newObj()))
		case v[0] == '+' || v[0] == '-':
			r, _ := rpc("events.subscribe", newObj())
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
			p := newObj()
			p.set("types", types)
			out(rpc("events.subscribe", p))
		default:
			t := []any{}
			for _, s := range strings.Split(v, ",") {
				t = append(t, s)
			}
			p := newObj()
			p.set("types", t)
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
		p := newObj()
		p.set("types", []any{"chat"})
		p.set("timeout_s", json.Number(arg(1)))
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
			if compact(x) == compact(y) {
				keep = false
			}
		}
		if keep {
			out = append(out, x)
		}
	}
	return out
}
