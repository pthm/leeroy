// Package script is the Lua runtime for an agent's character: one gopher-lua state shared by the agent's live
// evaluations and its running scripts, a coroutine scheduler for tasks, the event stream and action requests.
// See docs/sdk.md.
package script

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/types"
	lua "github.com/yuin/gopher-lua"
)

//go:embed runtime.lua
var runtimeLua string

// Engine owns the Lua state. Everything that touches it holds mu: the game loop's Tick and the agent's Eval are
// serialized, so both always see the same world and the same script state.
type Engine struct {
	mu      sync.Mutex
	L       *lua.LState
	logf    func(format string, args ...any)
	docs    map[string]Doc
	natives map[string]reflect.Value // Func registrations, for Native
	Bus     *Bus
	Reqs    *Requests
	diag    *diagnosticStream

	tasks    map[int]*Task
	byThread map[*lua.LState]*Task
	nextTask int
	handlers map[int]*handler
	nextH    int

	inboxMu sync.Mutex
	inbox   []Event

	tickErrs      int
	tickErr       string
	cur           *Task                    // the task being resumed (nil on the main state)
	tickCost      map[string]time.Duration // the last tick: Lua time by task name (handlers by theirs)
	trampoline    *lua.LFunction
	workspace     *os.Root
	workspacePath string

	// Lifecycle hooks run with the engine lock held; they must not reenter the engine.
	nativeCall     *diagnosticCall // request currently being invoked, under mu
	OnTaskInactive func(taskID int, reason string)
	OnClose        func()
	TaskMovement   func(taskID int) []types.MovementOwner
}

const executionLimit = 3 * time.Second

type handler struct {
	id    int
	types []string
	fn    *lua.LFunction
	name  string
}

// Doc describes one function of the API: Sig is its arguments and result, e.g. "(x, y, z) → request".
type Doc struct {
	Name string `json:"name"`
	Sig  string `json:"sig"`
	Desc string `json:"desc"`
}

// NewEngine creates the runtime. logf receives log() and error lines; bus may be nil (an in-memory stream).
func NewEngine(logf func(format string, args ...any), bus *Bus) *Engine {
	if bus == nil {
		bus = NewBus("")
	}
	e := &Engine{logf: logf, docs: map[string]Doc{}, natives: map[string]reflect.Value{}, Bus: bus, tasks: map[int]*Task{}, byThread: map[*lua.LState]*Task{}, handlers: map[int]*handler{}}
	e.Reqs = NewRequests(bus)
	e.diag = newDiagnosticStream()
	bus.Listen(func(ev Event) {
		e.inboxMu.Lock()
		e.inbox = append(e.inbox, ev)
		e.inboxMu.Unlock()
	})
	e.L = lua.NewState(lua.Options{SkipOpenLibs: true})
	for _, open := range []lua.LGFunction{lua.OpenBase, lua.OpenTable, lua.OpenString, lua.OpenMath, lua.OpenCoroutine} {
		e.L.Push(e.L.NewFunction(open))
		e.L.Push(lua.LString(""))
		e.L.Call(1, 0)
	}
	// Existing scripts use os.time. Expose only clock functions, never the
	// process, environment, or filesystem operations from the OS library.
	e.L.Push(e.L.NewFunction(lua.OpenOs))
	e.L.Call(0, 1)
	osLib := e.L.Get(-1).(*lua.LTable)
	safeOS := e.L.NewTable()
	for _, name := range []string{"clock", "date", "difftime", "time"} {
		safeOS.RawSetString(name, osLib.RawGetString(name))
	}
	e.L.Pop(1)
	e.L.SetGlobal("os", safeOS)
	// Base includes unrestricted file loaders; replace them with workspace loaders.
	for _, name := range []string{"io", "package", "require", "module", "dofile", "loadfile"} {
		e.L.SetGlobal(name, lua.LNil)
	}
	e.installFileLoaders()
	// Nested coroutines (including runtime.lua's pcall wrapper) need the same
	// deadline as their parent whenever they are resumed.
	co := e.L.GetGlobal("coroutine").(*lua.LTable)
	resume := co.RawGetString("resume")
	co.RawSetString("resume", e.L.NewFunction(func(L *lua.LState) int {
		thread := L.CheckThread(1)
		if ctx := L.Context(); ctx != nil {
			thread.SetContext(ctx)
		}
		args := make([]lua.LValue, L.GetTop())
		for i := range args {
			args[i] = L.Get(i + 1)
		}
		base := L.GetTop()
		if err := L.CallByParam(lua.P{Fn: resume, NRet: lua.MultRet, Protect: true}, args...); err != nil {
			L.RaiseError("%v", err)
		}
		return L.GetTop() - base
	}))
	co.RawSetString("wrap", lua.LNil)
	e.registerRuntime()
	e.registerDiagnostics()
	e.registerCompat()
	for _, d := range runtimeDocs {
		e.doc(d[0], d[1], d[2])
	}
	e.set("doc", func(L *lua.LState) int {
		e.doc(L.CheckString(1), L.OptString(2, ""), L.OptString(3, ""))
		return 0
	})
	e.set("help", func(L *lua.LState) int {
		prefix := L.OptString(1, "")
		names := []string{}
		for n := range e.docs {
			if strings.HasPrefix(n, prefix) {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		out := L.CreateTable(len(names), 0)
		for _, n := range names {
			d := e.docs[n]
			out.Append(lua.LString(n + d.Sig + "  " + d.Desc))
		}
		L.Push(out)
		return 1
	})
	e.trampoline, _ = e.L.Load(strings.NewReader("local fn, n, args = ..., select('#', ...) - 1, {select(2, ...)} coroutine.yield() return fn(unpack(args, 1, n))"), "trampoline")
	if err := e.doChunk("runtime.lua", runtimeLua); err != nil {
		panic("runtime.lua: " + err.Error())
	}
	if err := e.L.DoString(`coroutine.wrap = function(fn)
    local co = coroutine.create(fn)
    return function(...)
      local result = {coroutine.resume(co, ...)}
      if not result[1] then error(result[2]) end
      return unpack(result, 2)
    end
  end`); err != nil {
		panic(err)
	}
	return e
}

// Emit records an event (any goroutine). Use it for everything scripts and the agent should be able to see.
func (e *Engine) Emit(typ string, data any) Event { return e.Bus.Emit(typ, data) }

// ---------- running code ----------

// DoString runs a chunk on the main state.
func (e *Engine) DoString(code string) error {
	return e.DoChunk("<string>", code)
}

// DoChunk runs code on the main state under a name, which its errors and tracebacks report (act.lua:120:).
func (e *Engine) DoChunk(name, code string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.deadline(e.L)()
	return e.doChunk(name, code)
}

func (e *Engine) doChunk(name, code string) error {
	fn, err := e.L.Load(strings.NewReader(code), name)
	if err != nil {
		return err
	}
	return e.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true})
}

// DoFile runs a file on the main state.
func (e *Engine) DoFile(path string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.deadline(e.L)()
	fn, err := e.loadFile(e.L, path)
	if err != nil {
		return err
	}
	return e.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true})
}

// Eval runs a chunk and returns its results as Go values (nil for none, the value for one, a slice for several).
// It runs between ticks on the shared state, so it sees exactly what scripts see.
func (e *Engine) Eval(code string) (any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.deadline(e.L)()
	fn, err := e.L.Load(strings.NewReader(code), "wow") // errors read wow:1:, files loaded by it their own names
	if err != nil {
		return nil, err
	}
	base := e.L.GetTop()
	if err := e.L.CallByParam(lua.P{Fn: fn, NRet: lua.MultRet, Protect: true}); err != nil {
		e.L.SetTop(base)
		return nil, explain(err)
	}
	n := e.L.GetTop() - base
	out := make([]any, n)
	for i := range out {
		out[i] = toGo(e.L.Get(base+i+1), 0)
	}
	e.L.SetTop(base)
	switch n {
	case 0:
		return nil, nil
	case 1:
		return out[0], nil
	}
	return out, nil
}

// Call runs an API function by name ("items.list", "move.to") with JSON-shaped arguments and returns its results
// like Eval. It's how typed clients call the API without writing Lua.
func (e *Engine) Call(name string, args []any) (any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.deadline(e.L)()
	var fn lua.LValue = e.L.GetGlobal(name)
	if ns, field, ok := strings.Cut(name, "."); ok {
		fn = lua.LNil
		if t, ok := e.L.GetGlobal(ns).(*lua.LTable); ok {
			fn = t.RawGetString(field)
		}
	}
	f, ok := fn.(*lua.LFunction)
	if !ok {
		return nil, fmt.Errorf("no function %s (help() lists them)", name)
	}
	largs := make([]lua.LValue, len(args))
	for i, a := range args {
		largs[i] = toLua(e.L, a)
	}
	base := e.L.GetTop()
	if err := e.L.CallByParam(lua.P{Fn: f, NRet: lua.MultRet, Protect: true}, largs...); err != nil {
		e.L.SetTop(base)
		return nil, err
	}
	n := e.L.GetTop() - base
	out := make([]any, n)
	for i := range out {
		out[i] = toGo(e.L.Get(base+i+1), 0)
	}
	e.L.SetTop(base)
	switch n {
	case 0:
		return nil, nil
	case 1:
		return out[0], nil
	}
	return out, nil
}

// Native calls a Go function registered with Func directly (no Lua round trip), returning its first result as
// the Go value it is (a types.* struct, typically). For composing queries in Go, e.g. me.status.
func (e *Engine) Native(name string, args ...any) any {
	fn, ok := e.natives[name]
	if !ok {
		return nil
	}
	in := make([]reflect.Value, fn.Type().NumIn())
	for i := range in {
		in[i] = reflect.New(fn.Type().In(i)).Elem()
		if i < len(args) {
			in[i] = reflect.ValueOf(args[i]).Convert(fn.Type().In(i))
		}
	}
	out := fn.Call(in)
	if len(out) == 0 {
		return nil
	}
	return out[0].Interface()
}

// CallTick is one step of the runtime: deliver events (wake waiting tasks, start handlers), poll requests, resume
// tasks that can run, then call on_tick.
func (e *Engine) CallTick() bool {
	e.Reqs.Poll()
	e.mu.Lock()
	defer e.mu.Unlock()
	done := e.deadline(e.L)
	e.tickCost = map[string]time.Duration{}
	e.dispatchEvents()
	e.runTasks()
	done()
	// on_tick gets its own deadline: _G's __index is Lua, so looking it up after the tasks used up the tick's
	// deadline raised outside any pcall and took the daemon down
	defer e.deadline(e.L)()
	fn, ok := e.L.GetGlobal("on_tick").(*lua.LFunction)
	if !ok {
		return false
	}
	if err := e.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}); err != nil {
		e.noteError("on_tick", err)
	}
	return true
}

// SlowestTask is the task that ran longest in the last tick, and for how long (for the session's slow-tick report).
func (e *Engine) SlowestTask() (string, time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	name, most := "", time.Duration(0)
	for n, d := range e.tickCost {
		if d > most {
			name, most = n, d
		}
	}
	return name, most
}

func (e *Engine) noteError(where string, err error) {
	e.tickErrs++
	e.tickErr = err.Error()
	e.logf("[Lua] %s error: %v", where, err)
	go e.Emit("script_error", types.ScriptError{Where: where, Error: err.Error()})
}

// TakeScriptErrors returns the script errors since the last call ({count, last}), or nil.
func (e *Engine) TakeScriptErrors() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.tickErrs == 0 {
		return nil
	}
	out := map[string]any{"count": e.tickErrs, "last": e.tickErr}
	e.tickErrs = 0
	return out
}

func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.OnClose != nil {
		e.OnClose()
	}
	for _, t := range e.tasks {
		e.endTaskDiagnostics(t, "session_closed")
	}
	e.Reqs.close()
	e.diag.emit(types.Diagnostic{Kind: "runtime_state", Details: types.DiagnosticDetails{Component: "session", State: "stop"}})
	e.L.Close()
	if e.workspace != nil {
		e.workspace.Close()
	}
}

// ---------- binding Go to Lua ----------

// set installs fn as a global or a namespaced function ("move.to" -> global table move, field to).
func (e *Engine) set(name string, fn lua.LGFunction) {
	ns, field, ok := strings.Cut(name, ".")
	if !ok {
		e.L.SetGlobal(name, e.L.NewFunction(fn))
		return
	}
	t, isT := e.L.GetGlobal(ns).(*lua.LTable)
	if !isT {
		t = e.L.NewTable()
		e.L.SetGlobal(ns, t)
	}
	e.L.SetField(t, field, e.L.NewFunction(fn))
}

// doc records a function's documentation for help() (and the generated reference).
func (e *Engine) doc(name, sig, desc string) { e.docs[name] = Doc{name, sig, desc} }

// Func exposes a Go func to Lua as name ("ns.fn" or a global) with reflected argument and result conversion.
// Tables convert to map[string]any / []any parameters. sig and desc document it for help().
func (e *Engine) Func(name, sig, desc string, fn any) {
	v := reflect.ValueOf(fn)
	contract, err := ParseSignature(sig)
	if err != nil {
		panic(fmt.Sprintf("%s: invalid signature: %v", name, err))
	}
	count := len(contract.Parameters)
	if contract.Table {
		count = 1
	}
	if v.Kind() != reflect.Func || v.Type().IsVariadic() || contract.Variadic || v.Type().NumIn() != count {
		panic(fmt.Sprintf("%s%s: signature does not match Go function", name, sig))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.docs[name]; exists {
		panic("duplicate API registration: " + name)
	}
	e.doc(name, sig, desc)
	e.natives[name] = v
	need := contract.Required()
	e.set(name, func(L *lua.LState) int {
		var diagnostic *diagnosticCall
		if strings.Contains(sig, "→ Request") {
			diagnostic = e.beginDiagnostic(L, name, nativeDiagnosticArgs(name, sig, L))
			prior := e.nativeCall
			e.nativeCall = diagnostic
			defer func() { e.nativeCall = prior }()
			defer func() {
				if p := recover(); p != nil {
					e.endDiagnostic(diagnostic, false, "script_error", 0, 0)
					panic(p)
				}
			}()
		}
		// an argument left out would arrive as its zero value: spells.known() asking about spell 0 answers false
		if L.GetTop() < need {
			L.RaiseError("%s%s: called with %d argument(s), needs %d", name, sig, L.GetTop(), need)
		}
		if L.GetTop() > count {
			L.RaiseError("%s%s: called with %d argument(s), accepts at most %d", name, sig, L.GetTop(), count)
		}
		if contract.Table {
			checkNativeArgument(L, name, sig, Parameter{Name: "options"}, v.Type().In(0), 1)
			checkOptions(L, name, sig, L.Get(1), contract.Parameters)
		} else {
			for i, p := range contract.Parameters {
				checkNativeArgument(L, name, sig, p, v.Type().In(i), i+1)
			}
		}
		n := callReflect(L, v, 1)
		if diagnostic != nil && n > 0 {
			if h, ok := L.Get(-n).(*lua.LTable); ok {
				id := int(lua.LVAsNumber(h.RawGetString("id")))
				e.Reqs.Observe(id, func(r map[string]any) {
					ok, _ := r["ok"].(bool)
					code := ""
					if err, yes := r["error"].(map[string]any); yes {
						code, _ = err["code"].(string)
					}
					e.endDiagnostic(diagnostic, ok, code, id, 0)
				})
			}
		}
		return n
	})
}

// explain adds what Lua's terser errors usually mean here (it doesn't name what it tried to call)
func explain(err error) error {
	if strings.Contains(err.Error(), "attempt to call a non-function object") {
		return fmt.Errorf("%w (the name called isn't a function: misspelled, or not in the API; help(\"units\") lists a namespace, api-reference.md all of them)", err)
	}
	return err
}

// Methods exposes name("Method", ...) calling any exported method of target.
func (e *Engine) Methods(name, sig, desc string, target any) {
	v := reflect.ValueOf(target)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.doc(name, sig, desc)
	e.set(name, func(L *lua.LState) int {
		m := v.MethodByName(L.CheckString(1))
		if !m.IsValid() {
			L.RaiseError("no client method '%s'", L.CheckString(1))
		}
		return callReflect(L, m, 2)
	})
}

// CurrentTask is the name of the task running now ("" outside one); call it from a registered function.
func (e *Engine) CurrentTask() string {
	if e.cur != nil {
		return e.cur.Name
	}
	return ""
}

// CurrentTaskIdentity is available inside a registered function with the engine lock held.
// Task zero denotes an immediate evaluation, not a shared movement owner.
func (e *Engine) CurrentTaskIdentity() (id int, name, call string) {
	if c := e.nativeCall; c != nil {
		call = c.event.ParentCall
		if call == "" {
			call = c.event.Call
		}
		return c.event.TaskID, c.event.Task, call
	}
	if e.cur != nil {
		id, name = e.cur.ID, e.cur.Name
		if n := len(e.cur.calls); n > 0 {
			call = e.cur.calls[n-1].event.Call
		}
	}
	return
}

// TasksHeld lists the tasks; call it with the engine lock held (from a registered function).
func (e *Engine) TasksHeld() []types.TaskInfo {
	ids := []int{}
	for id, t := range e.tasks {
		if !t.Handler {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	out := []types.TaskInfo{}
	for _, id := range ids {
		t := e.tasks[id]
		ti := types.TaskInfo{ID: t.ID, Name: t.Name, State: t.State, Status: t.Status, Error: t.Err, Seconds: int(time.Since(t.Started).Seconds()),
			BusyMS: int(t.busy.Milliseconds())}
		if t.State == "waiting" {
			switch t.wait.kind {
			case "sleep":
				ti.WaitingFor = fmt.Sprintf("%.1fs", time.Until(t.wait.until).Seconds())
			case "await":
				ti.WaitingFor = fmt.Sprintf("request %d", t.wait.req)
			case "event":
				ti.WaitingFor = "event " + strings.Join(t.wait.types, ",")
			}
		}
		if e.TaskMovement != nil {
			ti.Movement = e.TaskMovement(t.ID)
		}
		out = append(out, ti)
	}
	return out
}

// Docs returns the documented API, sorted by name.
func (e *Engine) Docs() []Doc {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Doc, 0, len(e.docs))
	for _, d := range e.docs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var errType = reflect.TypeOf((*error)(nil)).Elem()

func callReflect(L *lua.LState, fn reflect.Value, first int) int {
	t := fn.Type()
	if t.IsVariadic() {
		L.RaiseError("variadic functions are not supported")
	}
	args := make([]reflect.Value, t.NumIn())
	for i := range args {
		args[i] = fromLua(L, L.Get(first+i), t.In(i))
	}
	results := fn.Call(args)
	for _, r := range results {
		if r.Type() == errType {
			if r.IsNil() {
				L.Push(lua.LNil)
			} else {
				L.Push(lua.LString(r.Interface().(error).Error()))
			}
			continue
		}
		L.Push(toLua(L, jsonish(r.Interface())))
	}
	return len(results)
}

func fromLua(L *lua.LState, lv lua.LValue, t reflect.Type) reflect.Value {
	v := reflect.New(t).Elem()
	switch t.Kind() {
	case reflect.String:
		if lv != lua.LNil {
			v.SetString(lua.LVAsString(lv))
		}
	case reflect.Bool:
		v.SetBool(lua.LVAsBool(lv))
	case reflect.Uint64:
		v.SetUint(guidOf(lv))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		v.SetUint(uint64(lua.LVAsNumber(lv)))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(lua.LVAsNumber(lv)))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(lua.LVAsNumber(lv)))
	case reflect.Slice, reflect.Map, reflect.Interface:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			v.SetBytes([]byte(lua.LVAsString(lv)))
			break
		}
		g := toGo(lv, 0)
		if g == nil {
			break
		}
		gv := reflect.ValueOf(g)
		if gv.Type().AssignableTo(t) {
			v.Set(gv)
		} else if t.Kind() == reflect.Slice && gv.Kind() == reflect.Map && gv.Len() == 0 { // {} is an empty list too
			v.Set(reflect.MakeSlice(t, 0, 0))
		} else {
			L.RaiseError("expected %s, got %s", t, lv.Type())
		}
	default:
		L.RaiseError("unsupported argument type %s", t)
	}
	return v
}

// guidOf accepts a GUID as a string (exact) or a number, or anything with a guid field (a unit, an item, an
// object), so passing the whole table where a GUID is wanted does what it says.
func guidOf(lv lua.LValue) uint64 {
	switch x := lv.(type) {
	case *lua.LTable:
		return guidOf(x.RawGetString("guid"))
	case lua.LString:
		g, _ := strconv.ParseUint(strings.TrimSpace(string(x)), 10, 64)
		return g
	case lua.LNumber:
		return uint64(x)
	}
	return 0
}

// toGo converts a Lua value: tables become map[string]any, or []any when they're a sequence.
func toGo(lv lua.LValue, depth int) any {
	switch x := lv.(type) {
	case lua.LBool:
		return bool(x)
	case lua.LNumber:
		return float64(x)
	case lua.LString:
		return string(x)
	case *lua.LTable:
		if depth > 12 {
			return "<table>"
		}
		if x.Len() == 0 && x.Metatable == arrayMT(nil) && arrayMT(nil) != lua.LNil {
			return []any{} // an empty list (it came from Go as one)
		}
		if n := x.Len(); n > 0 {
			count := 0
			x.ForEach(func(_, _ lua.LValue) { count++ })
			if count == n {
				arr := make([]any, n)
				for i := 1; i <= n; i++ {
					arr[i-1] = toGo(x.RawGetInt(i), depth+1)
				}
				return arr
			}
		}
		m := map[string]any{}
		x.ForEach(func(k, v lua.LValue) { m[lua.LVAsString(k)] = toGo(v, depth+1) })
		return m
	case *lua.LNilType:
		return nil
	}
	return "<" + lv.Type().String() + ">"
}

// arrayMT marks empty tables that are lists, so they convert back to [] rather than {} (one per process: gopher-lua
// tables don't belong to a state). arrayMT(nil) returns it if it exists.
var (
	arrayMeta lua.LValue = lua.LNil
	arrayOnce sync.Once
)

func arrayMT(L *lua.LState) lua.LValue {
	if L != nil {
		arrayOnce.Do(func() {
			t := L.NewTable()
			t.RawSetString("__name", lua.LString("list"))
			arrayMeta = t
		})
	}
	return arrayMeta
}

// toLua converts JSON-shaped Go values (after jsonish) to Lua.
func toLua(L *lua.LState, v any) lua.LValue {
	switch x := v.(type) {
	case nil:
		return lua.LNil
	case string:
		return lua.LString(x)
	case bool:
		return lua.LBool(x)
	case float64:
		return lua.LNumber(x)
	case int:
		return lua.LNumber(x)
	case int64:
		return lua.LNumber(x)
	case uint64:
		return lua.LNumber(x)
	case uint32:
		return lua.LNumber(x)
	case float32:
		return lua.LNumber(x)
	case map[string]any:
		t := L.NewTable()
		for k, vv := range x {
			t.RawSetString(k, toLua(L, vv))
		}
		return t
	case []any:
		t := L.CreateTable(len(x), 0)
		for i, vv := range x {
			t.RawSetInt(i+1, toLua(L, vv))
		}
		if len(x) == 0 {
			L.SetMetatable(t, arrayMT(L))
		}
		return t
	case lua.LValue:
		return x
	}
	return toLua(L, jsonish(v))
}

// jsonish flattens structs, slices and maps via JSON; integers beyond 2^53 (GUIDs) become strings.
func jsonish(v any) any {
	switch v.(type) {
	case nil, string, bool, float64:
		return v
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	var out any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&out) != nil {
		return nil
	}
	return fixNumbers(out)
}

func fixNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if s := x.String(); len(s) > 15 && !strings.ContainsAny(s, ".eE") {
			return s
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		for k, vv := range x {
			x[k] = fixNumbers(vv)
		}
	case []any:
		for i, vv := range x {
			x[i] = fixNumbers(vv)
		}
	}
	return v
}

// ---------- tasks ----------

// Task is a coroutine the scheduler runs.
type Task struct {
	ID      int
	Name    string
	co      *lua.LState
	fn      *lua.LFunction
	args    []lua.LValue
	State   string // new, running, waiting, paused, done, failed, stopped
	Status  string // what the task says it's doing (task.status)
	wait    waitCond
	resume  []lua.LValue // values to resume with once the wait is over
	Err     string
	Started time.Time
	Ended   time.Time
	Handler bool
	paused  string        // the state task.pause took it from (new or waiting)
	started bool          // resumed at least once (past the trampoline)
	trace   string        // where it was when it failed (panicWithTrace)
	busy    time.Duration // Lua time so far: the game waits while a task runs
	calls   []*diagnosticCall
}

type waitCond struct {
	kind  string // sleep, await, event
	until time.Time
	req   int
	types []string
	since uint64
	got   *Event
}

func (e *Engine) spawn(name string, fn *lua.LFunction, args []lua.LValue, isHandler bool) *Task {
	e.nextTask++
	co, _ := e.L.NewThread()
	t := &Task{ID: e.nextTask, Name: name, co: co, fn: fn, args: args, State: "new", Started: time.Now(), Handler: isHandler}
	e.tasks[t.ID] = t
	e.byThread[co] = t
	if !isHandler {
		go e.Emit("task_started", types.TaskEvent{Task: t.ID, Name: name})
	}
	return t
}

// runTasks resumes every task whose wait is over (called with mu held).
func (e *Engine) runTasks() {
	ids := make([]int, 0, len(e.tasks))
	for id := range e.tasks {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	now := time.Now()
	for _, id := range ids {
		t := e.tasks[id]
		if t == nil {
			continue
		}
		var args []lua.LValue
		switch t.State {
		case "new":
			args = t.args
		case "waiting":
			w := &t.wait
			switch w.kind {
			case "sleep":
				if now.Before(w.until) {
					continue
				}
			case "await":
				res := e.Reqs.Result(w.req)
				if res != nil && res["pending"] == nil {
					args = []lua.LValue{toLua(e.L, res)}
				} else if now.After(w.until) {
					op := "unknown"
					if res != nil {
						op, _ = res["op"].(string)
					}
					args = []lua.LValue{toLua(e.L, map[string]any{"id": w.req, "op": op, "ok": false,
						"error": map[string]any{"code": "wait_timeout", "message": "stopped waiting; the request may still finish"}})}
				} else {
					continue
				}
			case "event":
				if w.got != nil {
					args = []lua.LValue{eventToLua(e.L, *w.got)}
				} else if !w.until.IsZero() && now.After(w.until) {
					args = []lua.LValue{lua.LNil}
				} else {
					continue
				}
			}
		default:
			continue
		}
		e.resume(t, args)
	}
}

func (e *Engine) resume(t *Task, args []lua.LValue) {
	t.State, t.wait, t.trace = "running", waitCond{}, ""
	e.cur = t
	start := time.Now()
	st, err := e.safeResume(t, args)
	cost := time.Since(start)
	t.busy += cost
	if e.tickCost != nil {
		e.tickCost[t.Name] += cost
	}
	e.cur = nil
	switch {
	case err != nil:
		msg := err.Error()
		if strings.Contains(msg, "context deadline exceeded") {
			msg = strings.Replace(msg, "context deadline exceeded", fmt.Sprintf("ran %v without waiting: a loop in a task must wait(), await() or events.wait() on every pass", executionLimit), 1)
		}
		msg = explain(errors.New(msg)).Error()
		if t.trace != "" {
			msg += "\n" + t.trace
		}
		t.State, t.Err, t.Ended = "failed", msg, time.Now()
		e.finish(t)
		e.noteError("task "+t.Name, errors.New(msg))
	case st == lua.ResumeOK:
		t.State, t.Ended = "done", time.Now()
		e.finish(t)
	default: // yielded: wait was set by the Go function that yielded
		if t.State == "running" {
			t.State = "waiting"
		}
	}
}

// safeResume resumes a task's coroutine; a Lua VM panic (e.g. yielding where gopher-lua can't) fails that task
// instead of taking the whole session down.
func (e *Engine) safeResume(t *Task, args []lua.LValue) (st lua.ResumeState, err error) {
	defer e.deadline(t.co)()
	defer func() {
		if r := recover(); r != nil {
			st, err = lua.ResumeError, fmt.Errorf("%v", r)
		}
	}()
	if !t.started {
		// gopher-lua starts a coroutine with a Panic that drops the traceback. Start it on a trampoline that
		// yields at once, then give it one that keeps it.
		t.started = true
		if st, err, _ = e.L.Resume(t.co, e.trampoline, append([]lua.LValue{t.fn}, args...)...); err != nil || st != lua.ResumeYield {
			return st, err
		}
		t.co.Panic, args = e.panicWithTrace, nil
	}
	st, err, _ = e.L.Resume(t.co, t.fn, args...)
	return st, err
}

// panicWithTrace is a task's Panic: gopher-lua's own, but the failing task keeps where it was (file:line, innermost
// first), which the error that reaches the scheduler no longer has.
func (e *Engine) panicWithTrace(L *lua.LState) {
	if t := e.byThread[L]; t != nil {
		t.trace = traceback(L)
	}
	panic(&lua.ApiError{Type: lua.ApiErrorRun, Object: L.Get(-1)})
}

// traceback: the Lua frames of L's stack, innermost first ("  main.lua:22 in pull"), at most 8.
func traceback(L *lua.LState) string {
	var lines []string
	for i := 0; len(lines) < 8; i++ {
		dbg, ok := L.GetStack(i)
		if !ok {
			break
		}
		if _, err := L.GetInfo("nSl", dbg, lua.LNil); err != nil || dbg.CurrentLine <= 0 || dbg.Source == "trampoline" {
			continue
		}
		line := fmt.Sprintf("  %s:%d", dbg.Source, dbg.CurrentLine)
		if dbg.Name != "" && !strings.HasPrefix(dbg.Name, "<") && dbg.Name != "corountine" { // gopher-lua's names for unnamed ones
			line += " in " + dbg.Name
		}
		if len(lines) > 0 && lines[len(lines)-1] == line { // past a tail call it repeats the outermost frame
			break
		}
		lines = append(lines, line)
	}
	if len(lines) < 2 { // the error's own position says as much
		return ""
	}
	return "traceback:\n" + strings.Join(lines, "\n")
}

func (e *Engine) deadline(L *lua.LState) func() {
	ctx, cancel := context.WithTimeout(context.Background(), executionLimit)
	L.SetContext(ctx)
	return func() { L.RemoveContext(); cancel() }
}

// SetWorkspace grants Lua read access only to regular files within dir.
func (e *Engine) SetWorkspace(dir string) error {
	path, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	e.workspace, e.workspacePath = root, path
	return nil
}

func (e *Engine) loadFile(L *lua.LState, path string) (*lua.LFunction, error) {
	if e.workspace == nil {
		return nil, fmt.Errorf("Lua file access requires a workspace")
	}
	if filepath.IsAbs(path) {
		var err error
		path, err = filepath.Rel(e.workspacePath, path)
		if err != nil {
			return nil, err
		}
	}
	f, err := e.workspace.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return nil, fmt.Errorf("Lua source must be a regular file of at most 4 MiB")
	}
	code, err := io.ReadAll(io.LimitReader(f, 4<<20+1))
	if err != nil {
		return nil, err
	}
	return L.Load(bytes.NewReader(code), filepath.ToSlash(path)) // errors read main.lua:22:, not <string>:22:
}

func (e *Engine) installFileLoaders() {
	e.set("loadfile", func(L *lua.LState) int {
		fn, err := e.loadFile(L, L.CheckString(1))
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(fn)
		return 1
	})
	e.set("dofile", func(L *lua.LState) int {
		fn, err := e.loadFile(L, L.CheckString(1))
		if err != nil {
			L.RaiseError("%v", err)
		}
		if err := L.CallByParam(lua.P{Fn: fn, NRet: lua.MultRet, Protect: true}); err != nil {
			L.RaiseError("%v", err)
		}
		return L.GetTop() - 1
	})
}

func (e *Engine) finish(t *Task) {
	code := "task_stopped"
	if t.State == "failed" {
		code = "script_error"
	}
	if e.OnTaskInactive != nil {
		reason := code
		if t.State == "done" {
			reason = "task_finished"
		}
		e.OnTaskInactive(t.ID, reason)
	}
	e.endTaskDiagnostics(t, code)
	delete(e.byThread, t.co)
	if !t.Handler {
		go e.Emit("task_finished", types.TaskEvent{Task: t.ID, Name: t.Name, State: t.State,
			Seconds: int(t.Ended.Sub(t.Started).Seconds()), Error: t.Err})
	}
	// keep finished tasks around briefly for task.list(); handlers go at once
	if t.Handler {
		delete(e.tasks, t.ID)
	}
	var old []int
	for id, x := range e.tasks {
		if (x.State == "done" || x.State == "failed" || x.State == "stopped") && time.Since(x.Ended) > 10*time.Minute {
			old = append(old, id)
		}
	}
	for _, id := range old {
		delete(e.tasks, id)
	}
}

// dispatchEvents wakes tasks waiting for events and starts handlers (called with mu held).
func (e *Engine) dispatchEvents() {
	e.inboxMu.Lock()
	evs := e.inbox
	e.inbox = nil
	e.inboxMu.Unlock()
	for _, ev := range evs {
		for _, t := range e.tasks {
			waiting := t.State == "waiting" || t.State == "paused" && t.paused == "waiting"
			if waiting && t.wait.kind == "event" && t.wait.got == nil && ev.Seq > t.wait.since && Matches(ev, t.wait.types) {
				evc := ev
				t.wait.got = &evc
			}
		}
		hs := make([]*handler, 0, len(e.handlers))
		for _, h := range e.handlers {
			hs = append(hs, h)
		}
		sort.Slice(hs, func(i, j int) bool { return hs[i].id < hs[j].id })
		for _, h := range hs {
			if Matches(ev, h.types) && taskOfEvent(ev) != h.name { // not its own output (a "*" handler that logs)
				e.spawn(h.name, h.fn, []lua.LValue{eventToLua(e.L, ev)}, true)
			}
		}
	}
}

// taskOfEvent: the task that produced an event (log, script events), "" if none
func taskOfEvent(ev Event) string {
	switch d := ev.Data.(type) {
	case types.Log:
		return d.Task
	case map[string]any:
		s, _ := d["task"].(string)
		return s
	}
	return ""
}

func eventToLua(L *lua.LState, ev Event) lua.LValue {
	return toLua(L, map[string]any{"seq": float64(ev.Seq), "t": float64(ev.T), "type": ev.Type, "data": jsonish(ev.Data)})
}

// taskOf is the task L belongs to, or nil on the main state.
func (e *Engine) taskOf(L *lua.LState) *Task { return e.byThread[L] }

func strList(lv lua.LValue) []string {
	switch x := lv.(type) {
	case lua.LString:
		return []string{string(x)}
	case *lua.LTable:
		out := []string{}
		x.ForEach(func(_, v lua.LValue) { out = append(out, lua.LVAsString(v)) })
		return out
	}
	return nil
}

func (e *Engine) registerRuntime() {
	L := e.L
	tbl := func(name string, fns map[string]lua.LGFunction) {
		t := L.NewTable()
		for k, f := range fns {
			L.SetField(t, k, L.NewFunction(f))
		}
		L.SetGlobal(name, t)
	}
	needTask := func(L *lua.LState, what string) *Task {
		t := e.taskOf(L)
		if t == nil {
			L.RaiseError("%s only works inside a task: run your code with task.spawn(name, function() ... end)", what)
		}
		return t
	}
	// wait(seconds)
	L.SetGlobal("wait", L.NewFunction(func(L *lua.LState) int {
		t := needTask(L, "wait()")
		t.wait = waitCond{kind: "sleep", until: time.Now().Add(time.Duration(float64(L.OptNumber(1, 0)) * float64(time.Second)))}
		return L.Yield()
	}))
	// await(request or id [, timeout_s]) -> ActionResult; use result(id) to inspect outside a task.
	L.SetGlobal("await", L.NewFunction(func(L *lua.LState) int {
		t := needTask(L, "await()")
		id := 0
		switch x := L.Get(1).(type) {
		case lua.LNumber:
			id = int(x)
		case *lua.LTable:
			id = int(lua.LVAsNumber(x.RawGetString("id")))
			if x.RawGetString("pending") == lua.LNil { // already a result
				L.Push(x)
				return 1
			}
		}
		if res := e.Reqs.Result(id); res != nil && res["pending"] == nil {
			L.Push(toLua(L, res))
			return 1
		}
		t.wait = waitCond{kind: "await", req: id, until: time.Now().Add(time.Duration(float64(L.OptNumber(2, 30)) * float64(time.Second)))}
		return L.Yield()
	}))
	e.set("result", func(L *lua.LState) int {
		L.Push(toLua(L, e.Reqs.Result(int(L.CheckNumber(1)))))
		return 1
	})
	tbl("task", map[string]lua.LGFunction{
		// task.spawn([name,] fn, ...) -> id
		"spawn": func(L *lua.LState) int {
			name, fi := "task", 1
			if s, ok := L.Get(1).(lua.LString); ok {
				name, fi = string(s), 2
			}
			fn := L.CheckFunction(fi)
			var args []lua.LValue
			for i := fi + 1; i <= L.GetTop(); i++ {
				args = append(args, L.Get(i))
			}
			L.Push(lua.LNumber(e.spawn(name, fn, args, false).ID))
			return 1
		},
		"list": func(L *lua.LState) int {
			L.Push(toLua(L, jsonish(e.TasksHeld())))
			return 1
		},
		// task.status(text): what the current task is doing (shown in task.list and wow tasks)
		"status": func(L *lua.LState) int {
			if t := e.taskOf(L); t != nil {
				status := bounded(L.OptString(1, ""), 512)
				if status != t.Status {
					t.Status = status
					d := types.DiagnosticDetails{Status: status, AgentClaim: true}
					d.Source, d.Line = source(L)
					e.diag.emit(types.Diagnostic{Kind: "task_status", Task: bounded(t.Name, 128), TaskID: t.ID, Details: d})
				}
			}
			return 0
		},
		// task._adopt(co) / task._release(co): a coroutine run inside a task (runtime.lua's pcall) waits on the
		// task's behalf
		"_adopt": func(L *lua.LState) int {
			if t := e.taskOf(L); t != nil {
				e.byThread[L.CheckThread(1)] = t
			}
			return 0
		},
		"_release": func(L *lua.LState) int {
			co := L.CheckThread(1)
			if t := e.byThread[co]; t != nil && t.co != co {
				delete(e.byThread, co)
			}
			return 0
		},
		"current": func(L *lua.LState) int {
			if t := e.taskOf(L); t != nil {
				L.Push(lua.LNumber(t.ID))
				return 1
			}
			return 0
		},
		"stop": func(L *lua.LState) int {
			// by name or with no argument (every task) the caller is skipped: a script that stops its own name to
			// restart cleanly carries on; by id, stopping yourself is an error (return from the task instead)
			self := e.taskOf(L)
			ids := []int{}
			switch x := L.Get(1).(type) {
			case lua.LString:
				for id, t := range e.tasks {
					if t.Name == string(x) && t != self {
						ids = append(ids, id)
					}
				}
			case *lua.LNilType:
				for id, t := range e.tasks {
					if t != self {
						ids = append(ids, id)
					}
				}
			default:
				id := int(L.CheckNumber(1))
				if t := e.tasks[id]; t != nil && t == self {
					L.RaiseError("a task can't stop itself by id; return from it instead")
				}
				ids = append(ids, id)
			}
			n := 0
			for _, id := range ids {
				if t := e.tasks[id]; t != nil && t.State != "done" && t.State != "failed" && t.State != "stopped" {
					t.State, t.Ended = "stopped", time.Now()
					e.finish(t)
					n++
				}
			}
			L.Push(lua.LNumber(n))
			return 1
		},
		// pause takes a task off the schedule where it waits; resume puts it back on the same wait (an action's
		// result or an event that came in meanwhile is still delivered; a sleep that ran out ends at once)
		"pause": func(L *lua.LState) int {
			t := e.tasks[int(L.CheckNumber(1))]
			ok := t != nil && (t.State == "waiting" || t.State == "new")
			if ok {
				t.paused, t.State = t.State, "paused"
				if e.OnTaskInactive != nil {
					e.OnTaskInactive(t.ID, "task_paused")
				}
			}
			L.Push(lua.LBool(ok))
			return 1
		},
		"resume": func(L *lua.LState) int {
			t := e.tasks[int(L.CheckNumber(1))]
			ok := t != nil && t.State == "paused"
			if ok {
				t.State = t.paused
			}
			L.Push(lua.LBool(ok))
			return 1
		},
	})
	tbl("events", map[string]lua.LGFunction{
		// events.on(type or {types}, fn [, name]) -> id: fn(event) runs as its own task for each matching event.
		// A handler with the same name (default "on <types>") is replaced, so reloading a script doesn't stack them.
		"on": func(L *lua.LState) int {
			name := L.OptString(3, "on "+strings.Join(strList(L.Get(1)), ","))
			id := 0
			for hid, h := range e.handlers {
				if h.name == name {
					id = hid
				}
			}
			if id == 0 {
				e.nextH++
				id = e.nextH
			}
			e.handlers[id] = &handler{id: id, types: strList(L.Get(1)), fn: L.CheckFunction(2), name: name}
			L.Push(lua.LNumber(id))
			return 1
		},
		// events.off(id or name): remove a handler by the id events.on returned or the name it was given
		"off": func(L *lua.LState) int {
			if name, ok := L.Get(1).(lua.LString); ok {
				for id, h := range e.handlers {
					if h.name == string(name) {
						delete(e.handlers, id)
					}
				}
				return 0
			}
			delete(e.handlers, int(L.CheckNumber(1)))
			return 0
		},
		"handlers": func(L *lua.LState) int {
			out := []any{}
			for _, h := range e.handlers {
				out = append(out, map[string]any{"id": h.id, "types": h.types, "name": h.name})
			}
			L.Push(toLua(L, out))
			return 1
		},
		// events.wait(type or {types} [, timeout_s]) -> the next matching event, or nil on timeout (in a task)
		"wait": func(L *lua.LState) int {
			t := needTask(L, "events.wait()")
			w := waitCond{kind: "event", types: strList(L.Get(1)), since: e.Bus.Seq()}
			if L.GetTop() >= 2 && L.Get(2) != lua.LNil {
				w.until = time.Now().Add(time.Duration(float64(L.CheckNumber(2)) * float64(time.Second)))
			}
			t.wait = w
			return L.Yield()
		},
		// events.query{since, types, match, last, limit} -> events (history; nothing is consumed)
		"query": func(L *lua.LState) int {
			if L.GetTop() > 1 {
				L.RaiseError("events.query accepts one options table")
			}
			if L.Get(1) != lua.LNil {
				checkOptions(L, "events.query", "{[since=0, types=nil, match=nil, last=50, limit=nil]}", L.Get(1), []Parameter{{Name: "since", Optional: true}, {Name: "types", Optional: true}, {Name: "match", Optional: true}, {Name: "last", Optional: true}, {Name: "limit", Optional: true}})
			}
			q := Query{Last: 50}
			if t, ok := L.Get(1).(*lua.LTable); ok {
				for _, key := range []string{"since", "last", "limit"} {
					if v := t.RawGetString(key); v != lua.LNil {
						n, ok := v.(lua.LNumber)
						if !ok || float64(n) < 0 || float64(n) > 9007199254740991 || math.Trunc(float64(n)) != float64(n) {
							L.RaiseError("events.query: %s must be an exact nonnegative integer", key)
						}
					}
				}
				q.Since = uint64(lua.LVAsNumber(t.RawGetString("since")))
				q.Types = strList(t.RawGetString("types"))
				q.Match = lua.LVAsString(t.RawGetString("match"))
				if v := t.RawGetString("last"); v != lua.LNil {
					q.Last = int(lua.LVAsNumber(v))
				}
				if v := t.RawGetString("limit"); v != lua.LNil {
					q.Limit, q.Last = int(lua.LVAsNumber(v)), 0
				}
			}
			out := L.NewTable()
			for _, ev := range e.Bus.Query(q) {
				out.Append(eventToLua(L, ev))
			}
			L.Push(out)
			return 1
		},
		"seq": func(L *lua.LState) int { L.Push(lua.LNumber(e.Bus.Seq())); return 1 },
		// events.emit(name, data): a script's own event, as type "script.<name>"
		"emit": func(L *lua.LState) int {
			data, _ := toGo(L.Get(2), 0).(map[string]any)
			if data == nil {
				data = map[string]any{}
			}
			if t := e.taskOf(L); t != nil {
				data["task"] = t.Name
			}
			name := L.CheckString(1)
			if !strings.HasPrefix(name, "attention") && name != "answer" {
				name = "script." + name
			}
			ev := e.Emit(name, data)
			L.Push(lua.LNumber(ev.Seq))
			return 1
		},
	})
	// log(msg): a log event (tagged with the task) and a line in session.log
	e.set("now", func(L *lua.LState) int { L.Push(lua.LNumber(time.Now().UnixMilli())); return 1 })
	e.set("log", func(L *lua.LState) int {
		msg := L.ToString(1)
		data := types.Log{Msg: msg}
		if t := e.taskOf(L); t != nil {
			data.Task = t.Name
		}
		if data.Task != "" {
			e.logf("[Lua %s] %s", data.Task, msg)
		} else {
			e.logf("[Lua] %s", msg)
		}
		go e.Emit("log", data)
		return 0
	})
}

// runtimeDocs documents the runtime's own functions (the Go side of task, events, wait...).
var runtimeDocs = [][3]string{
	{"wait", "(seconds) → nil", "pause the current task (tasks only)"},
	{"await", "(request [, timeout_s = 30]) → ActionResult", "wait for an action's result (tasks only); wait_timeout does not cancel the request"},
	{"result", "(id) → (ActionResult | Request)?", "an action's result, its Request while pending, or nil for an unknown or expired ID"},
	{"log", "(msg) → nil", "a log event (tagged with the task) and a line in session.log"},
	{"now", "() → ms", "wall-clock time in milliseconds"},
	{"help", "([prefix]) → {lines}", "this reference: every function whose name starts with prefix (\"items\", \"act.go\")"},
	{"doc", "(name, sig, desc) → nil", "document a function of your own so help() lists it"},
	{"task.spawn", "([name,] fn, ...) → id", "run fn as a task: it can wait without blocking the game"},
	{"task.list", "() → TaskInfo[]", "your tasks (finished ones stay listed for 10 minutes)"},
	{"task.status", "(text) → nil", "what the current task claims it is doing; limited to 512 bytes, shown in task.list and recorded when changed as a task_status diagnostic"},
	{"task.current", "() → id?", "the current task's id (nil outside a task)"},
	{"task.stop", "([id or name]) → count", "stop tasks: by id, by name, or every task with no argument. By name or with none it skips the task calling it; a task waiting on a walk stops walking"},
	{"task.pause", "(id) → bool", "take a waiting task off the schedule until task.resume; false if it isn't waiting (a task can't pause itself)"},
	{"task.resume", "(id) → bool", "put a paused task back on its wait"},
	{"events.on", "(type or {types}, fn [, name]) → id", "run fn(event) as a task for each matching event; the same name replaces a handler"},
	{"events.off", "(id or name) → nil", "remove a handler: by the id events.on returned, or the name you gave it"},
	{"events.handlers", "() → {{id, types, name}}", "your event handlers"},
	{"events.wait", "(type or {types} [, timeout_s]) → event", "the next matching event, nil on timeout (tasks only)"},
	{"events.query", "{[since = 0, types = nil, match = nil, last = 50, limit = nil]} → {events}", "past events; types may end in * (\"quest_*\"), match searches the data; options may be omitted"},
	{"events.seq", "() → seq", "the latest event's sequence number"},
	{"events.emit", "(name, data) → seq", "an event of your own, type \"script.<name>\""},
	{"ask", "(question, {options} [, {[default = nil, timeout = nil]}]) → choice, \"answered\"|\"timeout\"", "ask the agent and wait for answer() (tasks only); timeout is positive seconds, nil waits indefinitely"},
	{"answer", "(id, choice) → bool", "answer an ask(): id is the attention event's seq; returns true after emitting the answer"},
}

// registerCompat adds the Lua 5.2 pieces scripts were written against (gopher-lua is 5.1): bit32, table.unpack,
// load(string). Lua side in runtime.lua.
func (e *Engine) registerCompat() {
	L := e.L
	u := func(L *lua.LState, i int) uint32 { return uint32(int64(L.OptNumber(i, 0))) }
	fold := func(op func(a, b uint32) uint32, init uint32) lua.LGFunction {
		return func(L *lua.LState) int {
			r := init
			for i := 1; i <= L.GetTop(); i++ {
				r = op(r, u(L, i))
			}
			L.Push(lua.LNumber(r))
			return 1
		}
	}
	shift := func(left bool) lua.LGFunction {
		return func(L *lua.LState) int {
			x, n := u(L, 1), int(L.CheckNumber(2))
			if left == (n >= 0) {
				x = x << uint(max(n, -n))
			} else {
				x = x >> uint(max(n, -n))
			}
			if n >= 32 || n <= -32 {
				x = 0
			}
			L.Push(lua.LNumber(x))
			return 1
		}
	}
	t := L.NewTable()
	for name, fn := range map[string]lua.LGFunction{
		"band":   fold(func(a, b uint32) uint32 { return a & b }, 0xFFFFFFFF),
		"bor":    fold(func(a, b uint32) uint32 { return a | b }, 0),
		"bxor":   fold(func(a, b uint32) uint32 { return a ^ b }, 0),
		"bnot":   func(L *lua.LState) int { L.Push(lua.LNumber(^u(L, 1))); return 1 },
		"btest":  func(L *lua.LState) int { L.Push(lua.LBool(u(L, 1)&u(L, 2) != 0)); return 1 },
		"lshift": shift(true),
		"rshift": shift(false),
		"extract": func(L *lua.LState) int {
			w := int(L.OptNumber(3, 1))
			L.Push(lua.LNumber((u(L, 1) >> uint(L.CheckNumber(2))) & (1<<uint(w) - 1)))
			return 1
		},
	} {
		L.SetField(t, name, L.NewFunction(fn))
	}
	L.SetGlobal("bit32", t)
}
