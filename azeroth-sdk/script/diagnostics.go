package script

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pthm/leeroy/azeroth-sdk/types"
	lua "github.com/yuin/gopher-lua"
)

type diagnosticStream struct {
	mu             sync.Mutex
	session, agent string
	seq, nextCall  uint64
	sink           func(types.Diagnostic)
}

type diagnosticCall struct {
	event types.Diagnostic
	start time.Time
}

func newDiagnosticStream() *diagnosticStream {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return &diagnosticStream{session: hex.EncodeToString(id[:])}
}

// SetDiagnosticSink is called before running scripts. The sink must not reenter the Lua engine.
// Writes are serialized in sequence order, including requests completed by network goroutines.
func (e *Engine) SetDiagnosticSink(agent string, sink func(types.Diagnostic)) {
	e.diag.mu.Lock()
	e.diag.agent, e.diag.sink = agent, sink
	e.diag.mu.Unlock()
	e.diag.emit(types.Diagnostic{Kind: "runtime_state", Details: types.DiagnosticDetails{Component: "session", State: "start"}})
}

// RecordObservation records typed session observations using the same identity and sequence as actions.
// It is a Go-side recorder hook, not an agent API, and must not be called from inside the diagnostic sink.
func (e *Engine) RecordObservation(kind string, details types.DiagnosticDetails) {
	e.diag.emit(types.Diagnostic{Kind: kind, Details: details})
}

func (e *Engine) endTaskDiagnostics(t *Task, code string) {
	for i := len(t.calls) - 1; i >= 0; i-- {
		e.endDiagnostic(t.calls[i], false, code, 0, 0)
	}
	t.calls = nil
}

func (d *diagnosticStream) emit(ev types.Diagnostic) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	ev.Schema, ev.Seq, ev.T = 1, d.seq, time.Now().UnixMilli()
	ev.Session, ev.Agent = d.session, d.agent
	if d.sink != nil {
		d.sink(ev)
	}
}

func bounded(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func source(L *lua.LState) (string, int) {
	for i := 1; i < 12; i++ {
		d, ok := L.GetStack(i)
		if !ok {
			break
		}
		if _, err := L.GetInfo("Sl", d, lua.LNil); err == nil && d.CurrentLine > 0 &&
			d.Source != "act.lua" && d.Source != "runtime.lua" && d.Source != "trampoline" && !strings.ContainsAny(d.Source, "\n\r") {
			return bounded(d.Source, 256), d.CurrentLine
		}
	}
	return "", 0
}

func (e *Engine) beginDiagnostic(L *lua.LState, op string, details types.DiagnosticDetails) *diagnosticCall {
	e.diag.mu.Lock()
	e.diag.nextCall++
	id := fmt.Sprintf("c%d", e.diag.nextCall)
	e.diag.mu.Unlock()
	ev := types.Diagnostic{Kind: "action_finished", Call: id, Op: bounded(op, 128), Details: details}
	ev.Details.Source, ev.Details.Line = source(L)
	if t := e.taskOf(L); t != nil {
		ev.Task, ev.TaskID = bounded(t.Name, 128), t.ID
		if len(t.calls) > 0 {
			ev.ParentCall = t.calls[len(t.calls)-1].event.Call
		}
	}
	return &diagnosticCall{event: ev, start: time.Now()}
}

func (e *Engine) endDiagnostic(c *diagnosticCall, ok bool, code string, request, attempts int) {
	ev := c.event
	ev.Outcome = "succeeded"
	if !ok {
		ev.Outcome, ev.Code = "failed", bounded(code, 128)
	}
	ev.Request, ev.DurationMS, ev.Details.Attempts = request, time.Since(c.start).Milliseconds(), attempts
	e.diag.emit(ev)
}

// Only numeric identifiers and short GUIDs are retained, never caller-provided prose.
func diagnosticArgs(op string, args map[string]lua.LValue) types.DiagnosticDetails {
	num := func(k string) float64 {
		v, _ := args[k].(lua.LNumber)
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return 0
		}
		return float64(v)
	}
	id := func(k string) uint32 {
		v := num(k)
		if v < 0 || v > math.MaxUint32 {
			return 0
		}
		return uint32(v)
	}
	d := types.DiagnosticDetails{QuestID: id("quest_id"), NPCEntry: id("npc_entry"), ItemEntry: id("item_entry"), SpellID: id("spell_id")}
	if d.QuestID == 0 {
		d.QuestID = id("quest")
	}
	if strings.HasPrefix(op, "spells.") || op == "act.cast" || op == "act.buff" {
		d.SpellID = id("id")
	}
	if d.ItemEntry == 0 {
		d.ItemEntry = id("item")
	}
	for _, k := range []string{"npc_guid", "guid", "unit", "target", "item"} {
		if s, ok := args[k].(lua.LString); ok && len(s) <= 32 && len(s) > 0 && strings.Trim(string(s), "0123456789") == "" {
			d.Target = string(s)
			break
		}
	}
	if _, x := args["x"].(lua.LNumber); x {
		if _, y := args["y"].(lua.LNumber); y {
			d.Destination = &types.DiagnosticDestination{X: num("x"), Y: num("y")}
			if _, ok := args["z"].(lua.LNumber); ok {
				z := num("z")
				d.Destination.Z = &z
			}
			if _, ok := args["map"].(lua.LNumber); ok {
				m := id("map")
				d.Destination.Map = &m
			}
		}
	}
	return d
}

func nativeDiagnosticArgs(op, sig string, L *lua.LState) types.DiagnosticDetails {
	contract, _ := ParseSignature(sig)
	args := map[string]lua.LValue{}
	for i, p := range contract.Parameters {
		if contract.Table {
			if table, ok := L.Get(1).(*lua.LTable); ok {
				args[p.Name] = table.RawGetString(p.Name)
			}
		} else {
			args[p.Name] = L.Get(i + 1)
		}
	}
	return diagnosticArgs(op, args)
}

func (e *Engine) registerDiagnostics() {
	e.set("task._action_begin", func(L *lua.LState) int {
		t := e.taskOf(L)
		if t == nil {
			L.RaiseError("action diagnostics require a task")
		}
		if len(t.calls) >= 128 {
			L.RaiseError("action nesting exceeds 128 calls")
		}
		op := L.CheckString(1)
		args := map[string]lua.LValue{}
		if opts, ok := L.Get(2).(*lua.LTable); ok {
			for _, k := range []string{"npc_guid", "guid", "unit", "target", "item", "id", "quest", "quest_id", "spell_id", "npc_entry", "item_entry", "x", "y", "z", "map"} {
				args[k] = opts.RawGetString(k)
			}
		} else {
			args["id"], args["guid"] = L.Get(2), L.Get(3)
		}
		c := e.beginDiagnostic(L, op, diagnosticArgs(op, args))
		t.calls = append(t.calls, c)
		L.Push(lua.LString(c.event.Call))
		return 1
	})
	e.set("task._action_end", func(L *lua.LState) int {
		t := e.taskOf(L)
		if t == nil || len(t.calls) == 0 {
			L.RaiseError("no active action")
		}
		c := t.calls[len(t.calls)-1]
		if c.event.Call != L.CheckString(1) {
			L.RaiseError("action stack mismatch")
		}
		r := L.CheckTable(2)
		code, attempts := "", 0
		if err, ok := r.RawGetString("error").(*lua.LTable); ok {
			code = lua.LVAsString(err.RawGetString("code"))
		}
		if data, ok := r.RawGetString("data").(*lua.LTable); ok {
			attempts = int(lua.LVAsNumber(data.RawGetString("attempts")))
		}
		e.endDiagnostic(c, r.RawGetString("ok") == lua.LTrue, code, 0, attempts)
		t.calls = t.calls[:len(t.calls)-1]
		return 0
	})
}
