// Package api serves the agent API as JSON-RPC 2.0 over HTTP (POST /rpc).
//
// Phase 0: every method is a thin, typed wrapper over the session's Lua bindings, so behaviour matches
// what in-session scripts see. Long waits (wait.chat, wait.idle) long-poll. A WebSocket event stream and
// verified ActionResults come in phase 1. sdk/ts mirrors these method names and shapes.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pthm/wowbench/azeroth-sdk/script"
	"github.com/pthm/wowbench/azeroth-sdk/session"
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

// eval runs Lua in a character's session (the same environment as its scripts).
func eval(s Sessions, char, code string) (any, error) {
	e, err := engine(s, char)
	if err != nil {
		return nil, err
	}
	return e.Eval(code)
}

// lua quotes a string as a Lua literal.
func lua(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return `"` + r.Replace(s) + `"`
}

const stateLua = `
local x, y, z, o, map = bot.call("Position")
local hp, hpmax = bot.get_health()
local pw, pwmax = bot.get_power()
local xp, xpmax = bot.get_xp()
local spell, left = bot.is_casting()
local r = %g
return {
  level = bot.get_level(), xp = xp, xp_max = xpmax, health = hp, health_max = hpmax,
  power = pw, power_max = pwmax, power_type = bot.get_power_type(), money = bot.money(),
  position = { x = x, y = y, z = z, o = o, map = map },
  alive = bot.is_alive(), ghost = bot.is_ghost(), in_combat = bot.in_combat(), moving = bot.is_moving(),
  target = bot.get_target(), casting = spell and { spell = spell, ms_left = left } or nil,
  units = bot.get_nearby_units(r), players = bot.get_nearby_players(r), attackers = bot.get_attackers(r),
  bags = bot.bag_items(), quests = bot.quest_log(), chat_pending = bot.chat_pending(),
}`

var methods = map[string]method{
	"sessions.list": typed(func(s Sessions, _ struct{}) (any, error) {
		out := []map[string]any{}
		for name, b := range s {
			st := b.Status()
			out = append(out, map[string]any{"char": name, "status": st.Status, "error": st.Error})
		}
		return out, nil
	}),
	// state.get: one consistent snapshot of the character and its surroundings (radius, default 40 yd)
	"state.get": typed(func(s Sessions, p struct {
		Char   string  `json:"char"`
		Radius float64 `json:"radius"`
	}) (any, error) {
		if p.Radius <= 0 {
			p.Radius = 40
		}
		return eval(s, p.Char, fmt.Sprintf(stateLua, p.Radius))
	}),
	// script.eval -> {result, script_errors?}: script_errors = on_tick failures since the last eval
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
		if se := e.TakeScriptErrors(); se != nil {
			out["script_errors"] = se
			if b := session_(s, p.Char); b != nil {
				b.Record("script_error", se)
			}
		}
		return out, nil
	}),
	"move.to": typed(func(s Sessions, p struct {
		Char string   `json:"char"`
		X    float64  `json:"x"`
		Y    float64  `json:"y"`
		Z    *float64 `json:"z"`
	}) (any, error) {
		z := "bot.ground_z(x, y)"
		if p.Z != nil {
			z = fmt.Sprint(*p.Z)
		}
		return eval(s, p.Char, fmt.Sprintf("local x, y = %g, %g; return bot.move_to(x, y, %s)", p.X, p.Y, z))
	}),
	"move.stop": typed(func(s Sessions, p charParams) (any, error) { return eval(s, p.Char, "bot.stop_moving()") }),
	"combat.attack": typed(func(s Sessions, p struct {
		Char string `json:"char"`
		GUID string `json:"guid"`
	}) (any, error) {
		g := lua(p.GUID)
		return eval(s, p.Char, fmt.Sprintf("bot.set_target(%s); bot.face_target(%s); return bot.attack(%s)", g, g, g))
	}),
	"combat.stop": typed(func(s Sessions, p charParams) (any, error) { return eval(s, p.Char, "bot.stop_attack()") }),
	"spell.cast": typed(func(s Sessions, p struct {
		Char   string `json:"char"`
		Spell  uint32 `json:"spell"`
		Target string `json:"target"`
	}) (any, error) {
		t := "bot.get_own_guid()"
		if p.Target != "" {
			t = lua(p.Target)
		}
		return eval(s, p.Char, fmt.Sprintf("return bot.cast_spell(%d, %s)", p.Spell, t))
	}),
	"loot.all": typed(func(s Sessions, p struct {
		Char string `json:"char"`
		GUID string `json:"guid"`
	}) (any, error) {
		return eval(s, p.Char, fmt.Sprintf("return bot.loot_all(%s)", lua(p.GUID)))
	}),
	"quest.accept": typed(func(s Sessions, p struct {
		Char  string `json:"char"`
		NPC   string `json:"npc"`
		Quest uint32 `json:"quest"`
	}) (any, error) {
		return eval(s, p.Char, fmt.Sprintf("return bot.call(\"QuestgiverAcceptQuest\", %s, %d)", lua(p.NPC), p.Quest))
	}),
	"quest.turnIn": typed(func(s Sessions, p struct {
		Char   string `json:"char"`
		NPC    string `json:"npc"`
		Quest  uint32 `json:"quest"`
		Reward uint32 `json:"reward"`
	}) (any, error) {
		return eval(s, p.Char, fmt.Sprintf("return bot.turn_in(%s, %d, %d)", lua(p.NPC), p.Quest, p.Reward))
	}),
	"death.release": typed(func(s Sessions, p charParams) (any, error) {
		return eval(s, p.Char, `return bot.call("RepopRequest")`)
	}),
	"death.reclaim": typed(func(s Sessions, p charParams) (any, error) {
		return eval(s, p.Char, `return bot.call("ReclaimCorpse")`)
	}),
	// chat.send: kind = say | party | whisper (to = name) | channel (to = channel name)
	"chat.send": typed(func(s Sessions, p struct {
		Char string `json:"char"`
		Kind string `json:"kind"`
		To   string `json:"to"`
		Text string `json:"text"`
	}) (any, error) {
		code := map[string]string{
			"say": "bot.send_chat(%[2]s)", "party": "bot.party(%[2]s)",
			"whisper": "bot.whisper(%[1]s, %[2]s)", "channel": "bot.channel(%[1]s, %[2]s)",
		}[p.Kind]
		if code == "" {
			return nil, fmt.Errorf("chat.send: kind must be say, party, whisper or channel")
		}
		return eval(s, p.Char, fmt.Sprintf(code, lua(p.To), lua(p.Text)))
	}),
	"chat.read": typed(func(s Sessions, p charParams) (any, error) { return eval(s, p.Char, "return bot.read_chat()") }),
	// wait.chat: returns once another player messages the character (or timeout_s passes) with the number of
	// unread messages; read them with chat.read
	"wait.chat": typed(func(s Sessions, p struct {
		Char    string  `json:"char"`
		Timeout float64 `json:"timeout_s"`
	}) (any, error) {
		return waitFor(s, p.Char, p.Timeout, "return bot.chat_pending() > 0", "return bot.chat_pending()")
	}),
	// wait.idle: returns once the character has stopped moving (arrived or gave up), with its position
	"wait.idle": typed(func(s Sessions, p struct {
		Char    string  `json:"char"`
		Timeout float64 `json:"timeout_s"`
	}) (any, error) {
		return waitFor(s, p.Char, p.Timeout, "return not bot.is_moving()", `local x, y, z = bot.get_position(); return {x = x, y = y, z = z}`)
	}),
}

// waitFor long-polls a Lua condition every 250 ms, then returns the result of `then`.
func waitFor(s Sessions, char string, timeout float64, cond, then string) (any, error) {
	if timeout <= 0 || timeout > 600 {
		timeout = 60
	}
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	for {
		ok, err := eval(s, char, cond)
		if err != nil {
			return nil, err
		}
		if ok == true || time.Now().After(deadline) {
			res, err := eval(s, char, then)
			return map[string]any{"timed_out": ok != true, "result": res}, err
		}
		time.Sleep(250 * time.Millisecond)
	}
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
	if b := session_(s, p.Char); b != nil && name != "sessions.list" && name != "state.get" && name != "wait.chat" && name != "wait.idle" {
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
