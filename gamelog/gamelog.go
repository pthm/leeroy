// Package gamelog reads a character's game log, game/<char>/events.jsonl: the events its agent saw (lines with seq:
// the SDK's event types, azsdk/types/events.go) and the records its session kept beside them (no seq:
// azsdk/types/gamelog.go). Nothing is renamed: an Ev is a line's data, its fields beside t (unix ms), type and
// char (the character), or under data when the data isn't an object (transports, a list).
package gamelog

import (
	"encoding/json"
	"strings"
)

// Ev is one line.
type Ev = map[string]any

type line struct {
	T    float64         `json:"t"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Line reads one line (nil if it isn't one).
func Line(b []byte, char string) Ev {
	var l line
	if json.Unmarshal(b, &l) != nil || l.Type == "" {
		return nil
	}
	e := Ev{}
	if len(l.Data) > 0 && l.Data[0] == '{' {
		_ = json.Unmarshal(l.Data, &e) // bad data: the line without it
	} else if len(l.Data) > 0 && string(l.Data) != "null" {
		var d any
		_ = json.Unmarshal(l.Data, &d)
		e["data"] = d
	}
	e["t"], e["type"], e["char"] = l.T, l.Type, char
	return e
}

// Lines reads lines of text (a file, or what it gained).
func Lines(text, char string) []Ev {
	var out []Ev
	for _, l := range strings.Split(text, "\n") {
		if e := Line([]byte(l), char); e != nil {
			out = append(out, e)
		}
	}
	return out
}

// Samples reads the judge's samples (judge/<char>/samples.jsonl) as Evs of type score.
func Samples(text, char string) []Ev {
	var out []Ev
	for _, l := range strings.Split(text, "\n") {
		var m map[string]any
		if l == "" || json.Unmarshal([]byte(l), &m) != nil || m["t"] == nil {
			continue
		}
		delete(m, "signals")
		m["type"], m["char"] = "score", char
		out = append(out, m)
	}
	return out
}
