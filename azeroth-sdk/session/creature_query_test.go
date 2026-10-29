package session

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"
)

// As WorldSession::HandleCreatureQueryOpcode writes it.
func TestReadCreatureQuery(t *testing.T) {
	le := binary.LittleEndian
	p := le.AppendUint32(nil, 2938)
	p = append(p, "Kobold Vermin\x00\x00\x00\x00Tunnel Rat\x00\x00"...)
	for _, v := range []uint32{0, 7, 0, 1, 0, 40, 0, 0, 0, 0} { // type flags, type, family, rank, kill credit x2, models x4
		p = le.AppendUint32(p, v)
	}
	entry, n, ci, ok := readCreatureQuery(bytes.NewReader(p))
	if !ok || entry != 2938 || n != "Kobold Vermin" || ci.subname != "Tunnel Rat" || ci.typ != 7 || ci.rank != 1 || !slices.Equal(ci.killCredit, []uint32{40}) {
		t.Fatalf("got %d %q %+v %v", entry, n, ci, ok)
	}
	if _, _, _, ok := readCreatureQuery(bytes.NewReader(le.AppendUint32(nil, 99|0x80000000))); ok {
		t.Fatal("unknown entry must not parse")
	}
}
