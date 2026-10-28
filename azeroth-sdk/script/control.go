package script

// Live control: evaluate Lua against the running engine (serialized with on_tick),
// and expose arbitrary Go funcs/methods to Lua via reflection.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	lua "github.com/Shopify/go-lua"
)

// Eval runs a Lua chunk and returns its results converted to Go values
// (nil for none, the value for one, a slice for several).
func (e *Engine) Eval(code string) (any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	L := e.state
	base := L.Top()
	defer L.SetTop(base)
	if err := lua.LoadString(L, code); err != nil {
		return nil, err
	}
	if err := L.ProtectedCall(0, lua.MultipleReturns, 0); err != nil {
		return nil, err
	}
	n := L.Top() - base
	out := make([]any, n)
	for i := range out {
		out[i] = toGo(L, base+i+1, 0)
	}
	switch n {
	case 0:
		return nil, nil
	case 1:
		return out[0], nil
	}
	return out, nil
}

// TakeScriptErrors returns the on_tick errors since the last call ({count, last}), or nil if there were none.
// Script failures otherwise only reach the log, so the API reports them with the next eval.
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

// noteTickError is called from CallTick with e.mu held.
func (e *Engine) noteTickError(err error) {
	e.tickErrs++
	e.tickErr = err.Error()
}

func toGo(L *lua.State, idx, depth int) any {
	switch L.TypeOf(idx) {
	case lua.TypeBoolean:
		return L.ToBoolean(idx)
	case lua.TypeNumber:
		n, _ := L.ToNumber(idx)
		return n
	case lua.TypeString:
		s, _ := L.ToString(idx)
		return s
	case lua.TypeTable:
		if depth > 8 {
			return "<table>"
		}
		idx = L.AbsIndex(idx)
		m := map[string]any{}
		L.PushNil()
		for L.Next(idx) {
			L.PushValue(-2) // copy key: ToString would mutate a numeric key mid-iteration
			k, _ := L.ToString(-1)
			m[k] = toGo(L, -2, depth+1)
			L.Pop(2)
		}
		if n := L.RawLength(idx); n > 0 && n == len(m) { // sequence -> array
			arr := make([]any, n)
			for i := range arr {
				arr[i] = m[strconv.Itoa(i+1)]
			}
			return arr
		}
		return m
	case lua.TypeNil, lua.TypeNone:
		return nil
	}
	return "<" + lua.TypeNameOf(L, idx) + ">"
}

// RegisterFunc exposes a Go func as bot.<name>(...) with reflected arg/result conversion.
func (e *Engine) RegisterFunc(name string, fn any) {
	v := reflect.ValueOf(fn)
	e.register(name, func(l *lua.State) int { return callReflect(l, v, 1) })
}

// RegisterCall exposes bot.<name>("Method", ...) calling any exported method of target.
func (e *Engine) RegisterCall(name string, target any) {
	v := reflect.ValueOf(target)
	e.register(name, func(l *lua.State) int {
		method, _ := l.ToString(1)
		m := v.MethodByName(method)
		if !m.IsValid() {
			lua.Errorf(l, "no client method '%s'", method)
		}
		return callReflect(l, m, 2)
	})
}

func (e *Engine) register(name string, fn lua.Function) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state.Global("bot")
	e.state.PushGoFunction(fn)
	e.state.SetField(-2, name)
	e.state.Pop(1)
}

var errType = reflect.TypeOf((*error)(nil)).Elem()

func callReflect(l *lua.State, fn reflect.Value, first int) int {
	t := fn.Type()
	if t.IsVariadic() {
		lua.Errorf(l, "variadic functions are not supported")
	}
	args := make([]reflect.Value, t.NumIn())
	for i := range args {
		args[i] = fromLua(l, first+i, t.In(i))
	}
	results := fn.Call(args)
	for _, r := range results {
		if r.Type() == errType {
			if r.IsNil() {
				l.PushNil()
			} else {
				l.PushString(r.Interface().(error).Error())
			}
			continue
		}
		pushValue(l, jsonish(r.Interface()))
	}
	return len(results)
}

func fromLua(l *lua.State, idx int, t reflect.Type) reflect.Value {
	v := reflect.New(t).Elem()
	switch t.Kind() {
	case reflect.String:
		s, _ := l.ToString(idx)
		v.SetString(s)
	case reflect.Bool:
		v.SetBool(l.ToBoolean(idx))
	case reflect.Uint64:
		v.SetUint(parseGUID(l, idx)) // string-safe for 64-bit GUIDs
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		n, _ := l.ToNumber(idx)
		v.SetUint(uint64(n))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, _ := l.ToNumber(idx)
		v.SetInt(int64(n))
	case reflect.Float32, reflect.Float64:
		n, _ := l.ToNumber(idx)
		v.SetFloat(n)
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			s, _ := l.ToString(idx)
			v.SetBytes([]byte(s))
			break
		}
		fallthrough
	default:
		lua.Errorf(l, "unsupported argument type %s", t)
	}
	return v
}

// jsonish flattens structs/slices/maps via JSON so pushValue can handle them.
// Integers beyond 2^53 (GUIDs) become strings to keep precision.
func jsonish(v any) any {
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
