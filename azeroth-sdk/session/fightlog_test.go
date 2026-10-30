package session

import (
	"strings"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// a fight made up second by second: you walk north towards Kobold A (pulled at once), B joins from beside A at 6 s,
// Rowena (party) drifts 45+ yd away, and you die at 10 s
func syntheticFight(t *testing.T) types.Fight {
	t.Helper()
	t0 := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	const me, a, b, rowena = 1, 2, 3, 4
	var frames []frameRec
	for s := -3; s <= 10; s++ {
		at := t0.Add(time.Duration(s) * time.Second)
		my := float64(max(s, 0)) * 2 // walking north 2 yd/s
		spots := []spotRec{{guid: me, name: "Wulfric", level: 6, kind: "you", x: my, hp: 1 - float64(max(s, 0))*0.1, alive: s < 10}}
		spots = append(spots, spotRec{guid: a, name: "Kobold Tunneler", level: 6, kind: "hostile", x: 24 - float64(max(s, 0)), y: 0, hp: 1, alive: true, target: map[bool]uint64{true: me}[s >= 0]})
		if s >= 4 {
			spots = append(spots, spotRec{guid: b, name: "Kobold Tunneler", level: 6, kind: "hostile", x: 22, y: -8, hp: 1, alive: true, target: map[bool]uint64{true: me}[s >= 6]})
		}
		spots = append(spots, spotRec{guid: rowena, name: "Rowena", level: 5, kind: "party", x: -10 - float64(max(s, 0))*5, y: 0, hp: 1, alive: true})
		frames = append(frames, frameRec{at, spots})
	}
	ref := func(g uint64, name string) types.UnitRef {
		return types.UnitRef{GUID: types.GUID(g), Name: name, Me: g == me}
	}
	entries := []entryRec{
		{t: t0.Add(500 * time.Millisecond), kind: "damage", src: ref(me, ""), dst: ref(a, "Kobold Tunneler"), spell: "Judgement", amount: 30},
		{t: t0.Add(6400 * time.Millisecond), kind: "damage", src: ref(b, "Kobold Tunneler"), dst: ref(me, ""), amount: 46, outcome: "crushing"},
		{t: t0.Add(10 * time.Second), kind: "death", dst: ref(me, "")},
	}
	sum := &types.FightSummary{Outcome: "died", Seconds: 10, DamageDealt: 30, DamageTaken: 46,
		Enemies: []types.UnitRef{ref(a, "Kobold Tunneler"), ref(b, "Kobold Tunneler")}}
	return buildFight(14, sum, t0, t0.Add(10*time.Second), frames, entries)
}

func TestBuildFightPositioning(t *testing.T) {
	f := syntheticFight(t)
	labels := ""
	for _, a := range f.Actors {
		labels += a.Label + ":" + a.Name + " "
	}
	if labels != "@:Wulfric A:Kobold Tunneler B:Kobold Tunneler P:Rowena " {
		t.Fatalf("actors = %q", labels)
	}
	p := f.Summary.Positioning
	if p == nil || len(p.Joins) != 2 {
		t.Fatalf("joins: %+v", p)
	}
	if j := p.Joins[0]; j.Label != "A" || j.S != 0 || j.Distance != 24 || j.From != "N" {
		t.Errorf("the pull: %+v", j)
	}
	if j := p.Joins[1]; j.Label != "B" || j.S != 6 || j.Near != "A" || j.From != "NE" {
		t.Errorf("the add: %+v, want B at 6 s from the NE, near A", j)
	}
	if p.MovedYd != 18 { // 2 yd/s from 0 to 9 s (dead at 10)
		t.Errorf("moved %g yd, want 18", p.MovedYd)
	}
	if len(p.Apart) != 1 || p.Apart[0].Label != "P" || p.Apart[0].Seconds < 4 {
		t.Errorf("apart: %+v, want Rowena out of range for several seconds", p.Apart)
	}
	if p.Died == nil || p.Died.EnemiesWithin10 != 2 { // A 3 yd away, B 9
		t.Errorf("died: %+v, want two enemies within 10 yd", p.Died)
	}
	if f.Summary.ID != 14 {
		t.Errorf("summary id %d", f.Summary.ID)
	}
}

func TestRenderFight(t *testing.T) {
	f := syntheticFight(t)
	out := renderFight(f, false, nil)
	for _, want := range []string{
		"fight 14 ·",
		"A Kobold Tunneler pulled at 0.0 s, 24 yd N of you",
		"B Kobold Tunneler joined at 6.0 s from", "(near A: its pack), an add",
		"P Rowena was more than 40 yd from you",
		"you died with 2 enemies within 10 yd",
		"key moments:", "B Kobold Tunneler joins from", "you're below 50% health",
		"+6.4  B Kobold Tunneler → @  melee 46 (crushing)",
		"+10.0  @ died",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// the movement map: your trail (o) north of where you started (*), you (@) at its end, A's trail (a)
	movement := out[strings.Index(out, "movement ("):strings.Index(out, "key moments:")]
	for _, ch := range []string{"@", "o", "*", "a", "A", "B", "p", "P"} {
		if !strings.Contains(movement, ch) {
			t.Errorf("movement map has no %q:\n%s", ch, movement)
		}
	}
	if at := renderFight(f, false, ptr(6.0)); !strings.Contains(at, "at +6.0 s") || strings.Contains(at, "timeline") {
		t.Errorf("--at 6:\n%s", at)
	}
	list := renderFightList([]types.FightHeader{header(f)})
	if !strings.Contains(list, "died") || !strings.Contains(list, "Kobold Tunneler ×2") || !strings.Contains(list, "   1  ") {
		t.Errorf("list:\n%s", list)
	}
}

func ptr(v float64) *float64 { return &v }

// fights go to <workspace>/fights and come back from there after a restart: the list and the numbering carry on
func TestFightLogOnDisk(t *testing.T) {
	ws := t.TempDir()
	fl := newFightLog(ws)
	logged := make(chan string, 1)
	t0 := time.Now().Add(-10 * time.Second)
	fl.frames = []frameRec{{t0, []spotRec{{guid: 1, name: "Wulfric", kind: "you", alive: true}}}}
	sum := &types.FightSummary{Outcome: "won", Seconds: 5}
	fl.finish(sum, t0, t0.Add(5*time.Second), func(f string, a ...any) { logged <- f })
	select {
	case <-logged:
	case <-time.After(2 * time.Second):
		t.Fatal("no fight file written")
	}
	if sum.ID != 1 || sum.Positioning == nil {
		t.Fatalf("summary id %d, positioning %v", sum.ID, sum.Positioning)
	}
	again := newFightLog(ws) // a restarted session
	if again.next != 2 {
		t.Errorf("next id after a restart = %d, want 2", again.next)
	}
	if hs := again.list(20); len(hs) != 1 || hs[0].ID != 1 || hs[0].Outcome != "won" {
		t.Errorf("list from disk: %+v", hs)
	}
	if f, ok := again.fight(1); !ok || f.Summary.Outcome != "won" {
		t.Errorf("fight 1 from disk: %v %+v", ok, f.Summary)
	}
}
