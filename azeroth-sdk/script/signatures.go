package script

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

type Parameter struct {
	Name     string
	Optional bool
	Default  string
}
type Signature struct {
	Parameters []Parameter
	Returns    string
	Table      bool
	Variadic   bool
}

// ParseSignature understands positional arguments, nested optional groups and table-shaped arguments.
// Return annotations are retained verbatim (including unions, multiple returns and generic result types).
func ParseSignature(sig string) (Signature, error) {
	var out Signature
	args, ret, ok := strings.Cut(sig, "→")
	args, out.Returns = strings.TrimSpace(args), strings.TrimSpace(ret)
	if !ok || out.Returns == "" {
		return out, fmt.Errorf("missing return annotation")
	}
	if len(args) < 2 || (args[0] != '(' && args[0] != '{') {
		return out, fmt.Errorf("expected an argument list or options table")
	}
	out.Table = args[0] == '{'
	close := byte(')')
	if out.Table {
		close = '}'
	}
	if args[len(args)-1] != close {
		return out, fmt.Errorf("unclosed argument list")
	}
	body := args[1 : len(args)-1]
	var token strings.Builder
	depth, optional, group := 0, false, 0
	var quote rune
	escaped := false
	flush := func() error {
		v := strings.TrimSpace(token.String())
		token.Reset()
		if v == "" {
			return nil
		}
		if out.Variadic {
			return fmt.Errorf("arguments after variadic tail")
		}
		if v == "..." {
			out.Variadic = true
			return nil
		}
		name, def, _ := strings.Cut(v, "=")
		if strings.HasPrefix(v, "{") {
			name, def = v, ""
		}
		name, def = strings.TrimSpace(name), strings.TrimSpace(def)
		if name == "" {
			return fmt.Errorf("empty argument name")
		}
		out.Parameters = append(out.Parameters, Parameter{name, optional, def})
		return nil
	}
	for _, c := range body {
		if quote != 0 {
			token.WriteRune(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			token.WriteRune(c)
			continue
		}
		if c == '{' || c == '(' {
			depth++
			token.WriteRune(c)
			continue
		}
		if c == '}' || c == ')' {
			depth--
			if depth < 0 {
				return out, fmt.Errorf("unbalanced argument shape")
			}
			token.WriteRune(c)
			continue
		}
		if depth == 0 {
			switch c {
			case '[':
				if err := flush(); err != nil {
					return out, err
				}
				group++
				optional = true
				continue
			case ']':
				if err := flush(); err != nil {
					return out, err
				}
				group--
				if group < 0 {
					return out, fmt.Errorf("unbalanced optional group")
				}
				optional = group > 0
				continue
			case ',':
				if err := flush(); err != nil {
					return out, err
				}
				continue
			}
		}
		token.WriteRune(c)
	}
	if depth != 0 || group != 0 || quote != 0 {
		return out, fmt.Errorf("unbalanced signature")
	}
	if err := flush(); err != nil {
		return out, err
	}
	return out, nil
}

func (s Signature) Required() int {
	if s.Table {
		return 1
	}
	n := 0
	for _, p := range s.Parameters {
		if !p.Optional {
			n++
		}
	}
	return n
}

// ValidateDocs is used by apigen against the live registry, not a second hand-maintained function list.
func ValidateDocs(docs []Doc) error {
	seen := map[string]bool{}
	for _, d := range docs {
		if d.Name == "" || seen[d.Name] {
			return fmt.Errorf("duplicate or empty function name %q", d.Name)
		}
		seen[d.Name] = true
		if strings.TrimSpace(d.Desc) == "" {
			return fmt.Errorf("%s: missing description", d.Name)
		}
		sig, err := ParseSignature(d.Sig)
		if err != nil {
			return fmt.Errorf("%s: %w", d.Name, err)
		}
		if strings.Contains(sig.Returns, "?") && !strings.Contains(strings.ToLower(d.Desc), "nil") {
			return fmt.Errorf("%s: nullable return must document its nil condition", d.Name)
		}
	}
	return nil
}

func scalarDefault(s string) lua.LValue {
	if s == "true" {
		return lua.LTrue
	}
	if s == "false" {
		return lua.LFalse
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return lua.LNumber(n)
	}
	if v, err := strconv.Unquote(s); err == nil {
		return lua.LString(v)
	}
	return lua.LNil // symbolic defaults are resolved by the operation itself
}

func checkNativeArgument(L *lua.LState, name, sig string, p Parameter, t reflect.Type, i int) {
	v := L.Get(i)
	bad := func(why string) { L.RaiseError("%s%s: argument %s %s", name, sig, p.Name, why) }
	if v == lua.LNil {
		if !p.Optional {
			bad("is required and cannot be nil")
		}
		v = scalarDefault(p.Default)
		if v == lua.LNil {
			return
		}
		for L.GetTop() < i {
			L.Push(lua.LNil)
		}
		L.Replace(i, v)
	}
	if p.Name == "item" && t.Kind() == reflect.Uint64 {
		switch value := v.(type) {
		case lua.LNumber:
			if value <= 0 || value >= 1<<32 || float64(value) != math.Trunc(float64(value)) {
				bad("must be a positive numeric item entry or a decimal instance GUID string")
			}
		case lua.LString:
			id, err := strconv.ParseUint(string(value), 10, 64)
			if err != nil || id < 1<<32 {
				bad("instance GUID strings must identify an item instance; use a number for an entry")
			}
		default:
			bad("must be a numeric item entry or a decimal instance GUID string")
		}
	}
	if p.Name == "guid" || strings.HasSuffix(p.Name, "_guid") {
		s, ok := v.(lua.LString)
		if !ok || len(s) == 0 {
			bad("must be a GUID string")
		}
		if _, err := strconv.ParseUint(string(s), 10, 64); err != nil {
			bad("must be a decimal GUID string")
		}
	}
	switch t.Kind() {
	case reflect.String:
		if _, ok := v.(lua.LString); !ok {
			bad("must be a string")
		}
	case reflect.Bool:
		if _, ok := v.(lua.LBool); !ok {
			bad("must be boolean")
		}
	case reflect.Uint64:
		if s, ok := v.(lua.LString); ok {
			if _, err := strconv.ParseUint(string(s), 10, 64); err != nil {
				bad("must be a decimal GUID string or exact nonnegative integer")
			}
			return
		}
		if table, ok := v.(*lua.LTable); ok {
			s, ok := table.RawGetString("guid").(lua.LString)
			if !ok {
				bad("table must contain a GUID string")
			}
			if _, err := strconv.ParseUint(string(s), 10, 64); err != nil {
				bad("table must contain a decimal GUID string")
			}
			return
		}
		fallthrough
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Float32, reflect.Float64:
		n, ok := v.(lua.LNumber)
		f := float64(n)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			bad("must be a finite number")
		}
		if t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64 {
			if t.Kind() == reflect.Float32 && math.Abs(f) > math.MaxFloat32 {
				bad("is outside float32 range")
			}
			return
		}
		if f != math.Trunc(f) {
			bad("must be an integer")
		}
		bits := t.Bits()
		if t.Kind() >= reflect.Uint && t.Kind() <= reflect.Uint64 {
			if f < 0 || f >= math.Exp2(float64(bits)) || (bits == 64 && f > 9007199254740991) {
				bad("is outside the exact unsigned range; use a GUID string for 64-bit identifiers")
			}
		} else if f < -math.Exp2(float64(bits-1)) || f >= math.Exp2(float64(bits-1)) {
			bad("is outside the signed range")
		}
	case reflect.Map, reflect.Slice:
		if _, ok := v.(*lua.LTable); !ok {
			bad("must be a table")
		}
		if strings.HasPrefix(p.Name, "{") {
			shape, err := ParseSignature(p.Name + " → table")
			if err != nil {
				bad("has an invalid options schema")
			}
			checkOptions(L, name, sig, v, shape.Parameters)
		}
	}
}

func checkOptions(L *lua.LState, name, sig string, v lua.LValue, fields []Parameter) {
	t, ok := v.(*lua.LTable)
	if !ok {
		L.RaiseError("%s%s: an options table is required", name, sig)
	}
	allowed := map[string]bool{}
	for _, p := range fields {
		allowed[p.Name] = true
		if t.RawGetString(p.Name) == lua.LNil && !p.Optional {
			L.RaiseError("%s%s: missing required option %s; accepted fields are shown in the signature", name, sig, p.Name)
		}
		v := t.RawGetString(p.Name)
		if v != lua.LNil {
			switch scalarDefault(p.Default).(type) {
			case lua.LBool:
				if _, ok := v.(lua.LBool); !ok {
					L.RaiseError("%s%s: option %s must be boolean", name, sig, p.Name)
				}
			case lua.LNumber:
				f, ok := v.(lua.LNumber)
				if !ok || math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
					L.RaiseError("%s%s: option %s must be a finite number", name, sig, p.Name)
				}
			}
		}
	}
	t.ForEach(func(k, v lua.LValue) {
		if !allowed[lua.LVAsString(k)] {
			L.RaiseError("%s%s: unknown option %s; accepted fields are shown in the signature", name, sig, k.String())
		}
	})
}

// DocumentActionPostconditions prevents new raw actions from silently bypassing
// the contract review. Called after the session has registered all namespaces.
func (e *Engine) DocumentActionPostconditions(postconditions map[string]string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for name, d := range e.docs {
		sig, err := ParseSignature(d.Sig)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(sig.Returns, "Request") {
			continue
		}
		condition, ok := postconditions[name]
		if !ok || condition == "" {
			return fmt.Errorf("%s: missing action postcondition", name)
		}
		d.Desc += ". Completion: " + condition + "."
		e.docs[name] = d
	}
	return nil
}
