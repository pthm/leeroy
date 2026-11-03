package clientdata

import (
	"bytes"
	"encoding/binary"
)

// DBCStrings returns a string field of every record in a DBC file, in record order ("" where empty).
func DBCStrings(b []byte, field int) []string {
	if len(b) < 20 || string(b[:4]) != "WDBC" {
		return nil
	}
	le := binary.LittleEndian
	n, size := int(le.Uint32(b[4:])), int(le.Uint32(b[12:]))
	strs := b[20+n*size:]
	out := make([]string, n)
	for i := range out {
		off := int(le.Uint32(b[20+i*size+field*4:]))
		if off <= 0 || off >= len(strs) {
			continue
		}
		if end := bytes.IndexByte(strs[off:], 0); end > 0 {
			out[i] = string(strs[off : off+end])
		}
	}
	return out
}
