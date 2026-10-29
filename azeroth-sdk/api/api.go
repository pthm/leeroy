// Package api serves the agent API as JSON-RPC 2.0 over HTTP (POST /rpc).
//
// The surface is small on purpose: the game API itself is the Lua API (docs/lua-api.md), reached three ways:
// script.eval (Lua), api.call (one function with JSON args: what the generated TypeScript SDK uses) and the
// event stream (events.query / events.wait). Values are the structs of package types.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/session"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// Sessions is the set of characters the daemon runs, by character name.
type Sessions map[string]*session.Bot

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// method: decode params into P, run, return result.
type method func(s Sessions, params json.RawMessage) (any, error)

func typed[P any](fn func(s Sessions, p P) (any, error)) method {
	return func(s Sessions, raw json.RawMessage) (any, error) {
		var p P
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, fmt.Errorf("bad params: %w", err)
			}
		}
		return fn(s, p)
	}
}

type charParams struct {
	Char string `json:"char"`
}

// engine finds a character's Lua engine. char may be empty when the daemon runs a single character.
func engine(s Sessions, char string) (*script.Engine, error) {
	if char == "" && len(s) == 1 {
		for name := range s {
			char = name
		}
	}
	b, ok := s[char]
	if !ok {
		return nil, fmt.Errorf("no session for character %q", char)
	}
	e := b.Engine()
	if e == nil {
		return nil, fmt.Errorf("%s is not in the world yet (status %s)", char, b.Status().Status)
	}
	return e, nil
}

var methods = map[string]method{
	"sessions.list": typed(func(s Sessions, _ struct{}) (any, error) {
		out := []map[string]any{}
		for name, b := range s {
			st := b.Status()
			out = append(out, map[string]any{"char": name, "status": st.Status, "error": st.Error})
		}
		return out, nil
	}),
	// script.eval -> {result, events?, script_errors?}: run Lua in the character's session (what its scripts see).
	// events = what happened since the previous eval; script_errors = script failures since then.
	"script.eval": typed(func(s Sessions, p struct {
		Char string `json:"char"`
		Code string `json:"code"`
	}) (any, error) {
		e, err := engine(s, p.Char)
		if err != nil {
			return nil, err
		}
		res, err := e.Eval(p.Code)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"result": res}
		if ev := news(e); ev != nil {
			out["events"] = ev
		}
		if se := e.TakeScriptErrors(); se != nil {
			out["script_errors"] = se
			if b := session_(s, p.Char); b != nil {
				b.Record("script_error", se)
			}
		}
		return out, nil
	}),
	// api.call: one API function by name with JSON arguments ("items.list", "move.to" [x, y, z]); its result
	"api.call": typed(func(s Sessions, p struct {
		Char string `json:"char"`
		Fn   string `json:"fn"`
		Args []any  `json:"args"`
	}) (any, error) {
		e, err := engine(s, p.Char)
		if err != nil {
			return nil, err
		}
		return e.Call(p.Fn, p.Args)
	}),
	// api.docs: every API function's signature and description
	"api.docs": typed(func(s Sessions, p charParams) (any, error) {
		e, err := engine(s, p.Char)
		if err != nil {
			return nil, err
		}
		return e.Docs(), nil
	}),
	// events.query: the event history (nothing is consumed). since = seq, types = {"chat", "quest_*"}, match =
	// text in the event, last = n most recent (default 50), limit = first n after since.
	"events.query": typed(func(s Sessions, p struct {
		Char string `json:"char"`
		script.Query
	}) (any, error) {
		e, err := engine(s, p.Char)
		if err != nil {
			return nil, err
		}
		if p.Last == 0 && p.Limit == 0 {
			p.Last = 50
		}
		return e.Bus.Query(p.Query), nil
	}),
	// events.wait: blocks until an event matching types/match arrives after since (default: now), or timeout_s
	"events.wait": typed(func(s Sessions, p struct {
		Char    string  `json:"char"`
		Timeout float64 `json:"timeout_s"`
		Since   *uint64 `json:"since"`
		script.Query
	}) (any, error) {
		e, err := engine(s, p.Char)
		if err != nil {
			return nil, err
		}
		if p.Timeout <= 0 || p.Timeout > 600 {
			p.Timeout = 60
		}
		p.Query.Since = e.Bus.Seq()
		if p.Since != nil {
			p.Query.Since = *p.Since
		}
		evs := e.Bus.Wait(p.Query, time.Duration(p.Timeout*float64(time.Second)))
		return map[string]any{"events": evs, "timed_out": len(evs) == 0, "seq": e.Bus.Seq()}, nil
	}),
}

// The agent's position in each character's event stream: every eval reply says what happened since the last one.
var (
	cursorMu sync.Mutex
	cursors  = map[*script.Engine]uint64{}
)

// important event types are shown in full in eval replies; the rest are counted
var important = []string{"chat", "attention", "death", "resurrected", "level_up", "script_error", "error", "notice",
	"party_invite", "party_changed", "quest_complete", "quest_turned_in", "gear_broken", "trade"}

// news summarises the events since the agent's last eval: {seq, counts by type, important events (last 20)}.
func news(e *script.Engine) map[string]any {
	cursorMu.Lock()
	since, seen := cursors[e]
	now := e.Bus.Seq()
	cursors[e] = now
	cursorMu.Unlock()
	if !seen || now == since {
		return nil
	}
	evs := e.Bus.Query(script.Query{Since: since})
	counts, keep := map[string]int{}, []script.Event{}
	for _, ev := range evs {
		counts[ev.Type]++
		failed := false
		switch d := ev.Data.(type) {
		case map[string]any: // a result
			failed = ev.Type == "result" && d["ok"] == false
		case types.TaskEvent:
			failed = ev.Type == "task_finished" && d.State != "done"
		case types.Chat:
			if d.Kind == "whisper_sent" {
				continue
			}
		}
		if failed || matchAny(ev.Type, important) {
			keep = append(keep, ev)
		}
	}
	if len(keep) > 20 {
		keep = keep[len(keep)-20:]
	}
	out := map[string]any{"seq": now, "counts": counts}
	if len(keep) > 0 {
		out["important"] = keep
	}
	if len(evs) < int(now-since) {
		out["note"] = "older events trimmed; wow events --since for the rest"
	}
	return out
}

func matchAny(t string, pats []string) bool {
	for _, p := range pats {
		if p == t || (strings.HasSuffix(p, "*") && strings.HasPrefix(t, strings.TrimSuffix(p, "*"))) {
			return true
		}
	}
	return false
}

// session_ finds a character's session (char may be empty for a single-character daemon), or nil.
func session_(s Sessions, char string) *session.Bot {
	if char == "" && len(s) == 1 {
		for _, b := range s {
			return b
		}
	}
	return s[char]
}

// callRecorded runs a method and, with a recorder attached, logs it as an "api" event (params truncated).
// Read-only and waiting methods are skipped.
func callRecorded(s Sessions, name string, params json.RawMessage, m method) (any, error) {
	var p charParams
	json.Unmarshal(params, &p)
	if b := session_(s, p.Char); b != nil && name != "sessions.list" && name != "api.docs" && name != "events.query" && name != "events.wait" {
		raw := string(params)
		if len(raw) > 500 {
			raw = raw[:500] + "..."
		}
		b.Record("api", map[string]any{"method": name, "params": raw})
	}
	return m(s, params)
}

// Handler serves POST /rpc (one JSON-RPC request per body) and GET /methods (method names).
func Handler(s Sessions) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /rpc", func(w http.ResponseWriter, r *http.Request) {
		var req request
		resp := map[string]any{"jsonrpc": "2.0"}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			resp["error"] = rpcError{-32700, "parse error: " + err.Error()}
		} else if m, ok := methods[req.Method]; !ok {
			resp["id"], resp["error"] = req.ID, rpcError{-32601, "unknown method " + req.Method}
		} else if res, err := callRecorded(s, req.Method, req.Params, m); err != nil {
			resp["id"], resp["error"] = req.ID, rpcError{-32000, err.Error()}
		} else {
			resp["id"], resp["result"] = req.ID, res
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("GET /methods", func(w http.ResponseWriter, _ *http.Request) {
		names := make([]string, 0, len(methods))
		for k := range methods {
			names = append(names, k)
		}
		json.NewEncoder(w).Encode(names)
	})
	return mux
}
