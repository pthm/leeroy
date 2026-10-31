// Package spelltext fills in the placeholders of spell descriptions and aura tooltips from the game data, as the
// client's tooltips do: "causes $s1 Fire damage over $d" -> "causes 14 to 22 Fire damage over 8 sec".
//
// Supported: $sN $mN $MN (effect values), $oN (periodic total), $tN (tick period), $d (duration), $aN (radius),
// $h (proc chance), $n (charges), $u (stacks), $xN (chain targets), $i (max targets), $r (range), $eN, $bN,
// another spell's value ($12345s1), scaling ($/10;s1, $*2;s1), ${expressions}, $<variables> where
// SpellDescriptionVariables.dbc defines them, conditionals ($?s123[a][b]: the default, last branch), plurals
// ($lpoint:points;) and gendered words ($gman:woman;: the first). Anything else is left as written.
package spelltext

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type spell struct {
	desc, tip                                  string
	base, die                                  [3]int32
	amp, radius, chain                         [3]uint32
	multiple, perCombo                         [3]float32
	duration, rng, varID                       uint32
	procChance, procCharges, stack, maxTargets uint32
}

// Book is the spells of a data directory's Spell.dbc with what their text refers to.
type Book struct {
	spells   map[uint32]*spell
	duration map[uint32]int32
	radius   map[uint32]float32
	ranges   map[uint32]float32
	vars     map[uint32]map[string]string
}

type dbc struct {
	b     []byte
	n, rs uint32
	strs  []byte
}

func readDBC(dir, name string) (*dbc, error) {
	b, err := os.ReadFile(filepath.Join(dir, "dbc", name))
	if err != nil {
		return nil, err
	}
	if len(b) < 20 || string(b[:4]) != "WDBC" {
		return nil, fmt.Errorf("%s: not a DBC", name)
	}
	le := binary.LittleEndian
	d := &dbc{b: b, n: le.Uint32(b[4:]), rs: le.Uint32(b[12:])}
	if 20+int(d.n*d.rs) > len(b) {
		return nil, fmt.Errorf("%s: truncated", name)
	}
	d.strs = b[20+d.n*d.rs:]
	return d, nil
}
func (d *dbc) u(r, f int) uint32 {
	return binary.LittleEndian.Uint32(d.b[20+uint32(r)*d.rs+uint32(f)*4:])
}
func (d *dbc) i(r, f int) int32 { return int32(d.u(r, f)) }
func (d *dbc) f(r, f int) float32 {
	return math.Float32frombits(d.u(r, f))
}
func (d *dbc) s(r, f int) string {
	off := d.u(r, f)
	if off >= uint32(len(d.strs)) {
		return ""
	}
	end := bytes.IndexByte(d.strs[off:], 0)
	if end < 0 {
		return ""
	}
	return string(d.strs[off : off+uint32(end)])
}

// Load reads <dataDir>/dbc: Spell, SpellDuration, SpellRadius, SpellRange and SpellDescriptionVariables (the
// last four optional: their placeholders then stay as written).
func Load(dataDir string) (*Book, error) {
	sp, err := readDBC(dataDir, "Spell.dbc")
	if err != nil {
		return nil, err
	}
	b := &Book{spells: map[uint32]*spell{}, duration: map[uint32]int32{}, radius: map[uint32]float32{},
		ranges: map[uint32]float32{}, vars: map[uint32]map[string]string{}}
	// field indices per AzerothCore DBCStructure.h SpellEntry (3.3.5a)
	for r := 0; r < int(sp.n); r++ {
		s := &spell{desc: sp.s(r, 170), tip: sp.s(r, 187), duration: sp.u(r, 40), rng: sp.u(r, 46), varID: sp.u(r, 232),
			procChance: sp.u(r, 35), procCharges: sp.u(r, 36), stack: sp.u(r, 49), maxTargets: sp.u(r, 212)}
		for k := 0; k < 3; k++ {
			s.die[k], s.base[k], s.radius[k] = sp.i(r, 74+k), sp.i(r, 80+k), sp.u(r, 92+k)
			s.amp[k], s.multiple[k], s.chain[k], s.perCombo[k] = sp.u(r, 98+k), sp.f(r, 101+k), sp.u(r, 104+k), sp.f(r, 119+k)
		}
		b.spells[sp.u(r, 0)] = s
	}
	if d, err := readDBC(dataDir, "SpellDuration.dbc"); err == nil {
		for r := 0; r < int(d.n); r++ {
			b.duration[d.u(r, 0)] = d.i(r, 1)
		}
	}
	if d, err := readDBC(dataDir, "SpellRadius.dbc"); err == nil {
		for r := 0; r < int(d.n); r++ {
			b.radius[d.u(r, 0)] = d.f(r, 1)
		}
	}
	if d, err := readDBC(dataDir, "SpellRange.dbc"); err == nil {
		for r := 0; r < int(d.n); r++ {
			b.ranges[d.u(r, 0)] = d.f(r, 3) // max range against hostile targets
		}
	}
	if d, err := readDBC(dataDir, "SpellDescriptionVariables.dbc"); err == nil {
		for r := 0; r < int(d.n); r++ {
			vs := map[string]string{}
			for _, line := range strings.Split(strings.ReplaceAll(d.s(r, 1), "\r", ""), "\n") {
				if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && strings.HasPrefix(k, "$") {
					vs[k[1:]] = v
				}
			}
			b.vars[d.u(r, 0)] = vs
		}
	}
	return b, nil
}

// Description is the spell's description with its placeholders filled in ("" for an unknown spell).
func (b *Book) Description(id uint32) string {
	if s := b.spells[id]; s != nil {
		return b.resolve(id, s.desc)
	}
	return ""
}

// Tooltip is what the spell's aura says while it's on you, filled in.
func (b *Book) Tooltip(id uint32) string {
	if s := b.spells[id]; s != nil {
		return b.resolve(id, s.tip)
	}
	return ""
}

func (b *Book) resolve(id uint32, text string) string {
	r := &resolver{b: b, id: id, last: math.NaN()}
	return r.text(text, 0)
}

type resolver struct {
	b    *Book
	id   uint32
	last float64 // the last number written, for plurals
}

// value is an effect value: base+1, or a range when the die rolls more than 1 (lo != hi)
func (r *resolver) value(s *spell, k int) (lo, hi float64) {
	lo = float64(s.base[k]) + 1
	hi = lo
	if s.die[k] > 1 {
		hi = float64(s.base[k]) + float64(s.die[k])
	}
	return math.Abs(lo), math.Abs(hi)
}

func num(v float64) string {
	if math.Abs(v-math.Round(v)) < 0.005 {
		return strconv.FormatInt(int64(math.Round(v)), 10)
	}
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

func duration(ms int32) string {
	switch {
	case ms <= 0:
		return "until cancelled"
	case ms < 60000:
		return num(float64(ms)/1000) + " sec"
	case ms < 3600000:
		return num(float64(ms)/60000) + " min"
	}
	return num(float64(ms)/3600000) + " hrs"
}

// token reads one placeholder after its '$' at text[i:]: its numeric value (if it has one), its text, and where it
// ends; ok false if it isn't one we know (the caller then keeps it as written)
func (r *resolver) token(text string, i, depth int) (v float64, out string, end int, ok bool) {
	scale := 1.0
	if i < len(text) && (text[i] == '/' || text[i] == '*') {
		semi := strings.IndexByte(text[i:], ';')
		if semi < 0 {
			return 0, "", i, false
		}
		f, err := strconv.ParseFloat(text[i+1:i+semi], 64)
		if err != nil || f == 0 {
			return 0, "", i, false
		}
		if text[i] == '/' {
			scale = 1 / f
		} else {
			scale = f
		}
		i += semi + 1
	}
	id := r.id
	j := i
	for j < len(text) && text[j] >= '0' && text[j] <= '9' {
		j++
	}
	if j > i {
		n, _ := strconv.ParseUint(text[i:j], 10, 32)
		id, i = uint32(n), j
	}
	s := r.b.spells[id]
	if s == nil || i >= len(text) {
		return 0, "", i, false
	}
	c := text[i]
	i++
	k := 0
	if i < len(text) && text[i] >= '1' && text[i] <= '3' {
		k = int(text[i] - '1')
		i++
	}
	single := func(x float64) (float64, string, int, bool) { x *= scale; return x, num(x), i, true }
	switch unicode.ToLower(rune(c)) {
	case 's', 'm':
		lo, hi := r.value(s, k)
		if c == 'M' {
			lo = hi
		}
		if c == 's' && hi != lo {
			return lo * scale, num(lo*scale) + " to " + num(hi*scale), i, true
		}
		return single(lo)
	case 'o':
		lo, hi := r.value(s, k)
		ticks := 0.0
		if d := r.b.duration[s.duration]; s.amp[k] > 0 && d > 0 {
			ticks = math.Floor(float64(d) / float64(s.amp[k]))
		}
		if hi != lo {
			return lo * ticks * scale, num(lo*ticks*scale) + " to " + num(hi*ticks*scale), i, true
		}
		return single(lo * ticks)
	case 't':
		return single(float64(s.amp[k]) / 1000)
	case 'd':
		d := r.b.duration[s.duration]
		return float64(d) / 1000 * scale, duration(d), i, true
	case 'a':
		return single(float64(r.b.radius[s.radius[k]]))
	case 'h':
		return single(float64(s.procChance))
	case 'n':
		return single(float64(s.procCharges))
	case 'u':
		return single(float64(s.stack))
	case 'x':
		return single(float64(s.chain[k]))
	case 'i':
		return single(float64(s.maxTargets))
	case 'r':
		return single(float64(r.b.ranges[s.rng]))
	case 'e':
		return single(float64(s.multiple[k]))
	case 'b':
		return single(float64(s.perCombo[k]))
	}
	return 0, "", i, false
}

// text resolves every placeholder in text
func (r *resolver) text(text string, depth int) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '$' || i+1 >= len(text) || depth > 4 {
			out.WriteByte(text[i])
			i++
			continue
		}
		start := i
		i++
		switch c := text[i]; {
		case c == '{': // ${expression}
			end := matching(text, i, '{', '}')
			if end < 0 {
				out.WriteString(text[start:])
				return out.String()
			}
			if v, ok := r.expr(text[i+1:end], depth+1); ok {
				out.WriteString(num(v))
				r.last = v
			} else {
				out.WriteString(text[start : end+1])
			}
			i = end + 1
		case c == '<': // $<variable>
			end := strings.IndexByte(text[i:], '>')
			if end < 0 {
				out.WriteString(text[start:])
				return out.String()
			}
			name := text[i+1 : i+end]
			i += end + 1
			if v, ok := r.variable(name, depth+1); ok {
				out.WriteString(num(v))
				r.last = v
			} else {
				out.WriteString(text[start:i])
			}
		case c == '?': // $?s123[a][b]...: the default, last branch
			alt, end := lastBranch(text, i)
			if end < 0 {
				out.WriteString(text[start:])
				return out.String()
			}
			out.WriteString(r.text(alt, depth+1))
			i = end
		case c == 'l' || c == 'L' || c == 'g' || c == 'G': // $lsingular:plural; / $gmale:female;
			semi := strings.IndexByte(text[i:], ';')
			colon := strings.IndexByte(text[i:], ':')
			if semi < 0 || colon < 0 || colon > semi {
				out.WriteString(text[start : i+1])
				i++
				continue
			}
			one, many := text[i+1:i+colon], text[i+colon+1:i+semi]
			if (c == 'l' || c == 'L') && r.last != 1 {
				out.WriteString(many)
			} else {
				out.WriteString(one)
			}
			i += semi + 1
		default:
			v, s, end, ok := r.token(text, i, depth)
			if !ok {
				out.WriteString(text[start:i])
				continue
			}
			out.WriteString(s)
			r.last = v
			i = end
		}
	}
	return out.String()
}

// variable evaluates a SpellDescriptionVariables entry of the spell
func (r *resolver) variable(name string, depth int) (float64, bool) {
	s := r.b.spells[r.id]
	if s == nil || depth > 4 {
		return 0, false
	}
	def, ok := r.b.vars[s.varID][name]
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(r.text(def, depth)), 64)
	return v, err == nil
}

// expr evaluates an arithmetic expression after resolving its placeholders: + - * / and parentheses
func (r *resolver) expr(e string, depth int) (float64, bool) {
	// placeholders become numbers (a range's low end)
	var b strings.Builder
	for i := 0; i < len(e); {
		if e[i] != '$' {
			b.WriteByte(e[i])
			i++
			continue
		}
		if i+1 < len(e) && e[i+1] == '<' {
			end := strings.IndexByte(e[i:], '>')
			if end < 0 {
				return 0, false
			}
			v, ok := r.variable(e[i+2:i+end], depth+1)
			if !ok {
				return 0, false
			}
			b.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
			i += end + 1
			continue
		}
		v, _, end, ok := r.token(e, i+1, depth)
		if !ok {
			return 0, false
		}
		b.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
		i = end
	}
	p := &parser{s: strings.ReplaceAll(b.String(), " ", "")}
	v, ok := p.sum()
	return v, ok && p.i == len(p.s)
}

type parser struct {
	s string
	i int
}

func (p *parser) sum() (float64, bool) {
	v, ok := p.product()
	for ok && p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
		op := p.s[p.i]
		p.i++
		var w float64
		if w, ok = p.product(); op == '+' {
			v += w
		} else {
			v -= w
		}
	}
	return v, ok
}
func (p *parser) product() (float64, bool) {
	v, ok := p.unary()
	for ok && p.i < len(p.s) && (p.s[p.i] == '*' || p.s[p.i] == '/') {
		op := p.s[p.i]
		p.i++
		var w float64
		if w, ok = p.unary(); op == '*' {
			v *= w
		} else if w != 0 {
			v /= w
		} else {
			return 0, false
		}
	}
	return v, ok
}
func (p *parser) unary() (float64, bool) {
	if p.i < len(p.s) && p.s[p.i] == '-' {
		p.i++
		v, ok := p.unary()
		return -v, ok
	}
	if p.i < len(p.s) && p.s[p.i] == '(' {
		p.i++
		v, ok := p.sum()
		if !ok || p.i >= len(p.s) || p.s[p.i] != ')' {
			return 0, false
		}
		p.i++
		return v, true
	}
	j := p.i
	for j < len(p.s) && (p.s[j] == '.' || (p.s[j] >= '0' && p.s[j] <= '9')) {
		j++
	}
	v, err := strconv.ParseFloat(p.s[p.i:j], 64)
	p.i = j
	return v, err == nil
}

// matching: the index of the bracket closing the one at text[i], or -1
func matching(text string, i int, open, close byte) int {
	depth := 0
	for j := i; j < len(text); j++ {
		switch text[j] {
		case open:
			depth++
		case close:
			if depth--; depth == 0 {
				return j
			}
		}
	}
	return -1
}

// lastBranch: for "$?cond[a]?cond[b][c]" at text[i] ('?'), the last bracketed branch and the index after it
func lastBranch(text string, i int) (string, int) {
	alt, end := "", -1
	for j := i; j < len(text); {
		switch text[j] {
		case '[':
			k := matching(text, j, '[', ']')
			if k < 0 {
				return "", -1
			}
			alt, end = text[j+1:k], k+1
			j = k + 1
			if j >= len(text) || (text[j] != '[' && text[j] != '?') {
				return alt, end
			}
		case '?':
			j++
			for j < len(text) && text[j] != '[' {
				j++
			}
		default:
			return alt, end
		}
	}
	return alt, end
}
