package metrics

import (
	"encoding/json"
	"reflect"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// Event is one event from a character's stream: the session's in-process events, or a line of azsdk -events
// (<Char>.events.jsonl). Data is the typed payload from types.EventTypes (types.Damage, types.XPGain, ...).
type Event struct {
	T    int64 // unix ms
	Type string
	Data any
}

// Decode parses one line of an events file. Unknown types keep their data as a map; ok is false for bad lines.
func Decode(line []byte) (e Event, ok bool) {
	var raw struct {
		T    int64           `json:"t"`
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(line, &raw) != nil || raw.Type == "" {
		return e, false
	}
	e = Event{T: raw.T, Type: raw.Type}
	if et, known := types.EventTypes[raw.Type]; known && et.Data != nil && len(raw.Data) > 0 {
		p := reflect.New(reflect.TypeOf(et.Data))
		if json.Unmarshal(raw.Data, p.Interface()) == nil {
			e.Data = p.Elem().Interface()
			return e, true
		}
	}
	var m map[string]any
	json.Unmarshal(raw.Data, &m)
	e.Data = m
	return e, true
}
