package world

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// obj is a JSON object that keeps its keys in insertion order (like a JS object) and, when encoded, leaves out
// keys whose value is nil or "" (azworld's output convention: absent instead of null or empty).
type obj struct {
	keys []string
	vals map[string]any
}

func newObj(kv ...any) *obj {
	o := &obj{vals: map[string]any{}}
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

// set adds or replaces a key; a replaced key keeps its position.
func (o *obj) set(k string, v any) *obj {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
	return o
}

func (o *obj) get(k string) any {
	if o == nil {
		return nil
	}
	return o.vals[k]
}

func (o *obj) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, x := range o.keys {
		if x == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// merge copies every key of p into o (a JS spread: {...o, ...p}); p may be nil.
func (o *obj) merge(p *obj) *obj {
	if p != nil {
		for _, k := range p.keys {
			o.set(k, p.vals[k])
		}
	}
	return o
}

func (o *obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	n := 0
	for _, k := range o.keys {
		v := o.vals[k]
		if v == nil || v == "" {
			continue
		}
		if p, ok := v.(*obj); ok && p == nil {
			continue
		}
		if n > 0 {
			b.WriteByte(',')
		}
		n++
		b.Write(toJSON(k))
		b.WriteByte(':')
		b.Write(toJSON(v))
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// toJSON encodes without HTML escaping (< > & stay as they are, as JSON.stringify writes them).
func toJSON(v any) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return []byte("null")
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}

var numRe = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// cell turns a value as the mysql client prints it into a number (short numerals), a string, or nil (NULL).
func cell(raw []byte, null bool) any {
	if null {
		return nil
	}
	s := string(raw)
	if s != "" && len(s) < 16 && numRe.MatchString(s) {
		f, _ := strconv.ParseFloat(s, 64)
		return f + 0 // -0 -> 0
	}
	return s
}

// qerr is a failed query; azworld reports it as {"error": ...}.
type qerr struct{ error }

// q runs a read-only query on the world DB (wowbench.* for the derived tables): rows as ordered objects, numbers as
// float64. A repeated column name keeps its first position and takes the last value. Panics with qerr on failure.
func q(query string) []*obj {
	if strings.TrimSpace(query) == "" {
		return []*obj{}
	}
	rs, err := World().Query(query)
	if err != nil {
		panic(qerr{err})
	}
	defer rs.Close()
	cols, _ := rs.Columns()
	out := []*obj{}
	for rs.Next() {
		raw := make([]sql.RawBytes, len(cols))
		ptr := make([]any, len(cols))
		for i := range raw {
			ptr[i] = &raw[i]
		}
		if err := rs.Scan(ptr...); err != nil {
			panic(qerr{err})
		}
		o := newObj()
		for i, c := range cols {
			o.set(c, cell(raw[i], raw[i] == nil))
		}
		out = append(out, o)
	}
	if err := rs.Err(); err != nil {
		panic(qerr{err})
	}
	return out
}

// q1 is the first row, or nil.
func q1(query string) *obj {
	if r := q(query); len(r) > 0 {
		return r[0]
	}
	return nil
}

// JS-style coercions, so the queries and outputs match what the TypeScript azworld produced.

func jsNum(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case float64:
		return x
	case int:
		return float64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return math.NaN()
		}
		return f
	}
	return math.NaN()
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	case bool:
		return x
	case *obj:
		return x != nil
	}
	return true
}

// or is `v || undefined`.
func or(v any) any {
	if truthy(v) {
		return v
	}
	return nil
}

// jsStr is `${v}` for the values that end up in SQL and keys.
func jsStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case float64:
		if math.IsNaN(x) {
			return "NaN"
		}
		return strconv.FormatFloat(x+0, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// float64(x) keeps the compiler from fusing a caller's product into the +0.5 (FMA), which JS never does.
func jsRound(x float64) float64 { return math.Floor(float64(x)+0.5) + 0 }

func num(v, what string, has bool) float64 {
	if !has || !numRe.MatchString(v) {
		got := v
		if !has {
			got = "nothing"
		}
		panic(qerr{fmt.Errorf("%s: expected a number, got %s", what, got)})
	}
	return jsNum(v)
}

func sqlStr(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
}

func idsOf(rows []*obj, k string) []float64 {
	var out []float64
	for _, r := range rows {
		if n := jsNum(r.get(k)); n != 0 && !math.IsNaN(n) {
			out = append(out, n)
		}
	}
	return out
}

func inList(a []float64) string {
	if len(a) == 0 {
		return "NULL"
	}
	s := make([]string, len(a))
	for i, v := range a {
		s[i] = jsStr(v)
	}
	return strings.Join(s, ",")
}
