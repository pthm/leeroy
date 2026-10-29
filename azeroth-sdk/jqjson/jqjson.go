// Package jqjson reads and prints JSON the way jq -c does: object keys in their order, number literals as written
// (exponents as jq 1.8 writes them), no HTML escaping, plus jq-style truthiness and field access. The wow tool
// prints the session's replies with it, and azbench's standings the board.
package jqjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Obj is a JSON object that keeps its keys in order, like jq.
type Obj struct {
	keys []string
	vals map[string]any
}

func NewObj() *Obj { return &Obj{vals: map[string]any{}} }

func (o *Obj) Get(k string) any { return o.vals[k] }

// Keys in order.
func (o *Obj) Keys() []string { return o.keys }

// set replaces a value in place, or appends a new key (jq's `. + {k: v}`).
func (o *Obj) Set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// parse decodes JSON values into ordered objects, []any, json.Number, string, bool and nil, one per value in the
// input (jq's -s reads all of them).
func ParseAll(data []byte) ([]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var out []any
	for {
		v, err := parseValue(dec)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, v)
	}
}

func parseValue(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch d := t.(type) {
	case json.Delim:
		if d == '{' {
			o := NewObj()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(kt.(string), v)
			}
			_, err := dec.Token()
			return o, err
		}
		a := []any{}
		for dec.More() {
			v, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err := dec.Token()
		return a, err
	}
	return t, nil
}

// enc writes v compactly the way jq -c does: keys in order, number literals as given, strings with only ", \,
// control characters and DEL escaped.
func enc(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case json.Number:
		b.WriteString(jqNumber(string(x)))
	case float64:
		j, _ := json.Marshal(x) // shortest round-trip, as jq prints computed numbers
		b.Write(j)
	case int:
		b.WriteString(strconv.Itoa(x))
	case string:
		encString(b, x)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			enc(b, e)
		}
		b.WriteByte(']')
	case *Obj:
		b.WriteByte('{')
		for i, k := range x.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			encString(b, k)
			b.WriteByte(':')
			enc(b, x.vals[k])
		}
		b.WriteByte('}')
	default:
		panic(fmt.Sprintf("enc %T", v))
	}
}

func encString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

func Compact(v any) string {
	var b strings.Builder
	enc(&b, v)
	return b.String()
}

// truthy: jq's truth (everything but null and false).
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	}
	return true
}

// field is jq's .k on an object (null otherwise).
func Field(v any, k string) any {
	if o, ok := v.(*Obj); ok {
		return o.Get(k)
	}
	return nil
}

// interp is jq's string interpolation of a value: strings raw, everything else as JSON.
func Interp(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return Compact(v)
}

func Num(v any) float64 {
	switch x := v.(type) {
	case json.Number:
		f, _ := x.Float64()
		return f
	case float64:
		return x
	case int:
		return float64(x)
	}
	return 0
}

// jqNumber is how jq 1.8 prints a number literal it passes through: as written, except that one with an exponent
// is rewritten in decNumber's scientific string form (1e3 -> 1E+3, 1e-3 -> 0.001, 1.5e-7 -> 1.5E-7).
func jqNumber(lit string) string {
	i := strings.IndexAny(lit, "eE")
	if i < 0 {
		return lit
	}
	mant, exp := lit[:i], lit[i+1:]
	e, err := strconv.Atoi(strings.TrimPrefix(exp, "+"))
	if err != nil {
		return lit
	}
	sign := ""
	if strings.HasPrefix(mant, "-") {
		sign, mant = "-", mant[1:]
	}
	if j := strings.IndexByte(mant, '.'); j >= 0 { // coefficient digits and exponent
		e -= len(mant) - j - 1
		mant = mant[:j] + mant[j+1:]
	}
	if t := strings.TrimLeft(mant, "0"); t != "" {
		mant = t
	} else {
		mant = "0"
	}
	adj := e + len(mant) - 1
	if e <= 0 && adj >= -6 { // plain notation
		if e == 0 {
			return sign + mant
		}
		if pad := -e - len(mant); pad >= 0 {
			return sign + "0." + strings.Repeat("0", pad) + mant
		}
		return sign + mant[:len(mant)+e] + "." + mant[len(mant)+e:]
	}
	s := mant[:1]
	if len(mant) > 1 {
		s += "." + mant[1:]
	}
	if adj >= 0 {
		return sign + s + "E+" + strconv.Itoa(adj)
	}
	return sign + s + "E" + strconv.Itoa(adj)
}
