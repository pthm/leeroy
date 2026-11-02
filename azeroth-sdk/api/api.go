// Package api serves the agent API as JSON-RPC 2.0 over HTTP (POST /rpc).
//
// The surface is small on purpose: the game API itself is the Lua API (docs/sdk.md), reached three ways:
// script.eval (Lua), api.call (one function with JSON args: what the generated TypeScript SDK uses) and the
// event stream (events.query / events.wait). Values are the structs of package types.
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
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
		Full bool   `json:"full"`
		script.Query
	}) (any, error) {
		e, err := engine(s, p.Char)
		if err != nil {
			return nil, err
		}
		if p.Last == 0 && p.Limit == 0 {
			p.Last = 50
		}
		return compactEvents(e.Bus.Query(p.Query), p.Full), nil
	}),
	// events.subscribe: set what wakes the agent (events.wait without types) and what eval replies show in full;
	// no types: just read it. Returns the subscription.
	"events.subscribe": typed(func(s Sessions, p struct {
		Char  string   `json:"char"`
		Types []string `json:"types"`
	}) (any, error) {
		e, err := engine(s, p.Char)
		if err != nil {
			return nil, err
		}
		if p.Types != nil {
			subsMu.Lock()
			subs[e] = p.Types
			subsMu.Unlock()
		}
		return subscribed(e), nil
	}),
	// events.wait: blocks until an event matching types/match (default: the subscription) arrives after since
	// (default: now), or timeout_s. A wake that is only chat (not a whisper, not naming the character) keeps collecting
	// until 2 s of quiet, 8 s at most (wait).
	"events.wait": typed(func(s Sessions, p struct {
		Char    string  `json:"char"`
		Timeout float64 `json:"timeout_s"`
		Since   *uint64 `json:"since"`
		Full    bool    `json:"full"`
		script.Query
	}) (any, error) {
		e, err := engine(s, p.Char)
		if err != nil {
			return nil, err
		}
		name := ""
		if b := session_(s, p.Char); b != nil {
			name = b.Name()
		}
		if p.Timeout <= 0 || p.Timeout > 600 {
			p.Timeout = 60
		}
		if len(p.Types) == 0 {
			p.Types = subscribed(e)
		}
		p.Query.Since, p.Query.Wake = e.Bus.Seq(), true
		if p.Since != nil {
			p.Query.Since = *p.Since
		}
		evs := wait(e.Bus, p.Query, time.Duration(p.Timeout*float64(time.Second)), name)
		return map[string]any{"events": compactEvents(evs, p.Full), "timed_out": len(evs) == 0, "seq": e.Bus.Seq()}, nil
	}),
	// events.harness: an event from the run's supervisor into the character's event stream (harnessEvents only)
	"events.harness": typed(func(s Sessions, p struct {
		Char string          `json:"char"`
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}) (any, error) {
		e, err := engine(s, p.Char)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(harnessEvents, p.Type) {
			return nil, fmt.Errorf("events.harness: %q is not one of %v", p.Type, harnessEvents)
		}
		var data map[string]any
		if err := json.Unmarshal(p.Data, &data); err != nil {
			return nil, fmt.Errorf("events.harness: data: %w", err)
		}
		return e.Emit(p.Type, data).Seq, nil
	}),
}

// How long events.wait keeps collecting after a wake that is only chat: until chatQuiet passes with no more, at most
// chatMax after the first. A burst of chat (a script's protocol messages, a conversation) is then one wake.
var chatQuiet, chatMax = 2 * time.Second, 8 * time.Second

// wait is events.wait: the events matching q, waiting up to timeout for the first. When they're all chat that is
// neither a whisper nor names the character (name), it keeps collecting matching events until chatQuiet of quiet,
// chatMax, the timeout, or an event that would wake at once.
func wait(bus *script.Bus, q script.Query, timeout time.Duration, name string) []script.Event {
	end := time.Now().Add(timeout)
	evs := bus.Wait(q, timeout)
	if !idleChat(evs, name) {
		return evs
	}
	if c := time.Now().Add(chatMax); c.Before(end) {
		end = c
	}
	for left := time.Until(end); left > 0; left = time.Until(end) {
		q.Since = evs[len(evs)-1].Seq
		more := bus.Wait(q, min(chatQuiet, left))
		evs = append(evs, more...)
		if !idleChat(more, name) {
			break
		}
	}
	return evs
}

// idleChat: evs is chat only, none of it a whisper or naming the character
func idleChat(evs []script.Event, name string) bool {
	for _, ev := range evs {
		c, ok := ev.Data.(types.Chat)
		if !ok || strings.Contains(c.Kind, "whisper") || name != "" && strings.Contains(strings.ToLower(c.Text), strings.ToLower(name)) {
			return false
		}
	}
	return len(evs) > 0
}

// harnessEvents: the events the supervisor sends (events.harness)
var harnessEvents = []string{"goal_met", "maintenance"}

// The agent's position in each character's event stream: every eval reply says what happened since the last one.
var (
	cursorMu sync.Mutex
	cursors  = map[*script.Engine]uint64{}
)

// What wakes the agent and what eval replies show in full, per character: the agent's choice (events.subscribe).
// Chat is by kind, so NPC lines and channel chatter only reach it if it asks for them.
var (
	subsMu sync.Mutex
	subs   = map[*script.Engine][]string{}
)

var defaultSubs = []string{"goal_met", "maintenance", "attention", "death", "stall", "failure_loop", "script_error", "task_finished", "party_invite", "trade", "summon_request", "ready_check", "loot_roll_start",
	"gear_broken", "chat:whisper", "chat:party", "chat:raid", "chat:say"}

func subscribed(e *script.Engine) []string {
	subsMu.Lock()
	defer subsMu.Unlock()
	if s, ok := subs[e]; ok {
		return s
	}
	return defaultSubs
}

// informational events eval replies always show in full (they don't wake the agent unless subscribed). Channel
// chat is how players broadcast to each other, so it's shown even though it doesn't wake by default.
var shown = []string{"goal_met", "maintenance", "level_up", "resurrected", "error", "notice", "party_changed", "quest_complete", "quest_turned_in",
	"chat:channel", "slow_tick"}

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
	counts, keep, sub := map[string]int{}, []script.Event{}, subscribed(e)
	for _, ev := range evs {
		counts[ev.Type]++
		failed := false
		switch d := ev.Data.(type) {
		case map[string]any: // a result
			failed = ev.Type == "result" && d["ok"] == false
		case types.TaskEvent:
			failed = ev.Type == "task_finished" && d.State != "done"
		}
		if failed || script.Matches(ev, sub) || script.Matches(ev, shown) {
			keep = append(keep, ev)
		}
	}
	if len(keep) > 20 {
		keep = keep[len(keep)-20:]
	}
	out := map[string]any{"seq": now, "counts": counts}
	if len(keep) > 0 {
		out["important"] = compactEvents(keep, false)
	}
	if len(evs) < int(now-since) {
		out["note"] = "older events trimmed; wow events --since for the rest"
	}
	return out
}

// routeShown: a list of more points than this (a route's waypoints) is shortened in the events a reply shows
const routeShown = 8

// compactEvents shortens what replies show of events (unless full): every list of more than routeShown points
// becomes {count, start, end} (its first and last two) and where to read it whole. The stream keeps them whole.
func compactEvents(evs []script.Event, full bool) []script.Event {
	if full {
		return evs
	}
	out := make([]script.Event, len(evs))
	for i, ev := range evs {
		out[i] = ev
		raw, err := json.Marshal(ev.Data)
		if err != nil || !bytes.Contains(raw, []byte(`"x":`)) {
			continue
		}
		var data any
		json.Unmarshal(raw, &data)
		whole := "wow events --full"
		if m, ok := data.(map[string]any); ok && ev.Type == "result" && m["id"] != nil {
			whole = fmt.Sprintf("result(%v)", m["id"])
		}
		out[i].Data = shortenRoutes(data, whole)
	}
	return out
}

func shortenRoutes(v any, whole string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, item := range x {
			x[k] = shortenRoutes(item, whole)
		}
	case []any:
		if len(x) > routeShown && points(x) {
			return map[string]any{"count": len(x), "start": x[:2], "end": x[len(x)-2:], "whole": whole}
		}
		for i, item := range x {
			x[i] = shortenRoutes(item, whole)
		}
	}
	return v
}

// points: every element is a position ({x, y, ...})
func points(list []any) bool {
	for _, p := range list {
		m, ok := p.(map[string]any)
		if _, isNum := m["x"].(float64); !ok || !isNum {
			return false
		}
		if _, isNum := m["y"].(float64); !isNum {
			return false
		}
	}
	return true
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

// callRecorded runs a method and logs it as an "api" record in the game log (params truncated).
// Read-only and waiting methods are skipped.
func callRecorded(s Sessions, name string, params json.RawMessage, m method) (any, error) {
	var p charParams
	json.Unmarshal(params, &p)
	if b := session_(s, p.Char); b != nil && name != "sessions.list" && name != "api.docs" && name != "events.query" && name != "events.wait" {
		raw := string(params)
		if len(raw) > 500 {
			raw = raw[:500] + "..."
		}
		b.GameLog("api", types.APICall{Method: name, Params: raw})
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
