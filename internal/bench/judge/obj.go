package judge

import (
	"bytes"
	"encoding/json"
)

// obj is a JSON object that keeps its keys in order (samples, the board and the result line read like the TS judge's).
type obj []kv

type kv struct {
	k string
	v any
}

func (o obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, e := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(e.k)
		v, err := json.Marshal(e.v)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func (o obj) get(k string) float64 {
	for _, e := range o {
		if e.k == k {
			f, _ := e.v.(float64)
			return f
		}
	}
	return 0
}

func (o obj) m() map[string]float64 {
	m := make(map[string]float64, len(o))
	for _, e := range o {
		m[e.k], _ = e.v.(float64)
	}
	return m
}

// round1 rounds to one decimal like JS Math.round(v * 10) / 10 (halves go up).
func round1(v float64) float64 { return floor(v*10+0.5) / 10 }
