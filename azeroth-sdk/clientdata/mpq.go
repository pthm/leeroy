// Package clientdata reads the game client's own data (a 3.3.5a install's MPQ archives): the minimap images and
// the terrain's texture layers, for the viewer's map and the road map. Read-only, and only what we need: MPQ v1/v2
// archives with zlib or bzip2 compressed files, BLP2 images, and ADT texture layers.
package clientdata

import (
	"bytes"
	"compress/bzip2"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var cryptTable = func() [0x500]uint32 {
	var t [0x500]uint32
	seed := uint32(0x00100001)
	for i := 0; i < 0x100; i++ {
		for j, idx := 0, i; j < 5; j, idx = j+1, idx+0x100 {
			seed = (seed*125 + 3) % 0x2AAAAB
			hi := (seed & 0xFFFF) << 16
			seed = (seed*125 + 3) % 0x2AAAAB
			t[idx] = hi | (seed & 0xFFFF)
		}
	}
	return t
}()

func hashString(s string, typ uint32) uint32 {
	s1, s2 := uint32(0x7FED7FED), uint32(0xEEEEEEEE)
	for _, c := range []byte(strings.ToUpper(strings.ReplaceAll(s, "/", `\`))) {
		s1 = cryptTable[typ*0x100+uint32(c)] ^ (s1 + s2)
		s2 = uint32(c) + s1 + s2 + (s2 << 5) + 3
	}
	return s1
}

func decrypt(data []uint32, key uint32) {
	seed := uint32(0xEEEEEEEE)
	for i := range data {
		seed += cryptTable[0x400+(key&0xFF)]
		ch := data[i] ^ (key + seed)
		key = ((^key << 0x15) + 0x11111111) | (key >> 0x0B)
		seed = ch + seed + (seed << 5) + 3
		data[i] = ch
	}
}

type hashEntry struct {
	name1, name2 uint32
	locale       uint16
	platform     uint16
	block        uint32
}

type blockEntry struct{ offset, csize, size, flags uint32 }

const (
	flagImplode    = 0x00000100
	flagCompress   = 0x00000200
	flagEncrypted  = 0x00010000
	flagFixKey     = 0x00020000
	flagSingleUnit = 0x01000000
	flagSectorCRC  = 0x04000000
	flagExists     = 0x80000000
)

// Archive is one open MPQ file.
type Archive struct {
	f          *os.File
	mu         sync.Mutex
	sectorSize uint32
	hashes     []hashEntry
	blocks     []blockEntry
}

func readU32s(f *os.File, off int64, n uint32) ([]uint32, error) {
	buf := make([]byte, n*4)
	if _, err := f.ReadAt(buf, off); err != nil {
		return nil, err
	}
	out := make([]uint32, n)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(buf[i*4:])
	}
	return out, nil
}

// OpenArchive reads an MPQ's header, hash and block tables.
func OpenArchive(path string) (*Archive, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var h [32]byte
	if _, err := f.ReadAt(h[:], 0); err != nil || string(h[:4]) != "MPQ\x1a" {
		f.Close()
		return nil, fmt.Errorf("%s: not an MPQ archive", path)
	}
	le := binary.LittleEndian
	a := &Archive{f: f, sectorSize: 512 << le.Uint16(h[14:])}
	hashPos, blockPos, hashN, blockN := le.Uint32(h[16:]), le.Uint32(h[20:]), le.Uint32(h[24:]), le.Uint32(h[28:])
	ht, err := readU32s(f, int64(hashPos), hashN*4)
	if err != nil {
		return nil, err
	}
	decrypt(ht, hashString("(hash table)", 3))
	for i := uint32(0); i < hashN; i++ {
		e := ht[i*4:]
		a.hashes = append(a.hashes, hashEntry{e[0], e[1], uint16(e[2]), uint16(e[2] >> 16), e[3]})
	}
	bt, err := readU32s(f, int64(blockPos), blockN*4)
	if err != nil {
		return nil, err
	}
	decrypt(bt, hashString("(block table)", 3))
	for i := uint32(0); i < blockN; i++ {
		e := bt[i*4:]
		a.blocks = append(a.blocks, blockEntry{e[0], e[1], e[2], e[3]})
	}
	return a, nil
}

func (a *Archive) find(name string) (blockEntry, bool) {
	n := uint32(len(a.hashes))
	if n == 0 {
		return blockEntry{}, false
	}
	i, n1, n2 := hashString(name, 0)%n, hashString(name, 1), hashString(name, 2)
	for start := i; ; {
		e := a.hashes[i]
		if e.block == 0xFFFFFFFF {
			return blockEntry{}, false
		}
		if e.name1 == n1 && e.name2 == n2 && e.block < uint32(len(a.blocks)) {
			if b := a.blocks[e.block]; b.flags&flagExists != 0 {
				return b, true
			}
		}
		if i = (i + 1) % n; i == start {
			return blockEntry{}, false
		}
	}
}

// Has reports whether the archive holds name.
func (a *Archive) Has(name string) bool { _, ok := a.find(name); return ok }

func decompress(data []byte, size uint32) ([]byte, error) {
	if uint32(len(data)) >= size {
		return data[:size], nil
	}
	mask, body := data[0], data[1:]
	var r io.Reader
	switch mask {
	case 0x02:
		zr, err := zlib.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		r = zr
	case 0x10:
		r = bzip2.NewReader(bytes.NewReader(body))
	default:
		return nil, fmt.Errorf("unsupported compression 0x%02x", mask)
	}
	out := make([]byte, size)
	_, err := io.ReadFull(r, out)
	return out, err
}

// Read returns a file's contents.
func (a *Archive) Read(name string) ([]byte, error) {
	b, ok := a.find(name)
	if !ok {
		return nil, os.ErrNotExist
	}
	if b.flags&flagImplode != 0 {
		return nil, fmt.Errorf("%s: imploded files aren't supported", name)
	}
	a.mu.Lock()
	raw := make([]byte, b.csize)
	_, err := a.f.ReadAt(raw, int64(b.offset))
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	key := uint32(0)
	if b.flags&flagEncrypted != 0 {
		key = hashString(filepath.Base(strings.ReplaceAll(name, `\`, "/")), 3)
		if b.flags&flagFixKey != 0 {
			key = (key + b.offset) ^ b.size
		}
	}
	dec := func(buf []byte, k uint32) {
		words := make([]uint32, len(buf)/4)
		for i := range words {
			words[i] = binary.LittleEndian.Uint32(buf[i*4:])
		}
		decrypt(words, k)
		for i, w := range words {
			binary.LittleEndian.PutUint32(buf[i*4:], w)
		}
	}
	if b.flags&flagSingleUnit != 0 || b.flags&flagCompress == 0 {
		if b.flags&flagEncrypted != 0 {
			dec(raw, key)
		}
		if b.flags&flagCompress != 0 {
			return decompress(raw, b.size)
		}
		return raw[:b.size], nil
	}
	sectors := (b.size + a.sectorSize - 1) / a.sectorSize
	nOff := sectors + 1
	if b.flags&flagSectorCRC != 0 {
		nOff++
	}
	offs := raw[:nOff*4]
	if b.flags&flagEncrypted != 0 {
		dec(offs, key-1)
	}
	out := make([]byte, 0, b.size)
	for s := uint32(0); s < sectors; s++ {
		lo, hi := binary.LittleEndian.Uint32(offs[s*4:]), binary.LittleEndian.Uint32(offs[s*4+4:])
		if hi > uint32(len(raw)) || lo > hi {
			return nil, fmt.Errorf("%s: bad sector table", name)
		}
		sec := append([]byte(nil), raw[lo:hi]...)
		if b.flags&flagEncrypted != 0 {
			dec(sec, key+s)
		}
		want := min(a.sectorSize, b.size-s*a.sectorSize)
		d, err := decompress(sec, want)
		if err != nil {
			return nil, fmt.Errorf("%s sector %d: %w", name, s, err)
		}
		out = append(out, d...)
	}
	return out, nil
}

// Client is a game install's archives, newest patch first (the client's own lookup order).
type Client struct{ archives []*Archive }

// OpenClient opens <dir>/Data/*.MPQ and the locale's archives, patches first.
func OpenClient(dir string) (*Client, error) {
	data := filepath.Join(dir, "Data")
	order := []string{"patch-3.MPQ", "patch-2.MPQ", "patch.MPQ", "lichking.MPQ", "expansion.MPQ", "common-2.MPQ", "common.MPQ"}
	locale, _ := filepath.Glob(filepath.Join(data, "*", "patch-*-3.MPQ"))
	var paths []string
	if len(locale) > 0 {
		l := filepath.Dir(locale[0])
		code := filepath.Base(l)
		for _, n := range []string{"patch-" + code + "-3", "patch-" + code + "-2", "patch-" + code, "lichking-locale-" + code,
			"expansion-locale-" + code, "locale-" + code, "base-" + code} {
			paths = append(paths, filepath.Join(l, n+".MPQ"))
		}
	}
	for _, n := range order {
		paths = append(paths, filepath.Join(data, n))
	}
	c := &Client{}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		a, err := OpenArchive(p)
		if err != nil {
			return nil, err
		}
		c.archives = append(c.archives, a)
	}
	if len(c.archives) == 0 {
		return nil, fmt.Errorf("no MPQ archives under %s", data)
	}
	return c, nil
}

// Read returns a file from the first archive (newest patch first) that has it.
func (c *Client) Read(name string) ([]byte, error) {
	for _, a := range c.archives {
		if a.Has(name) {
			return a.Read(name)
		}
	}
	return nil, os.ErrNotExist
}
