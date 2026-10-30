package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func TestHelperDiagnosticsRecordedAcrossSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a1.record.jsonl")
	r, err := OpenRecorder(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.f.Close()
	for range 2 {
		e := script.NewEngine(func(string, ...any) {}, nil)
		e.SetDiagnosticSink("a1", r.writeDiagnostic)
		if err := e.DoString(actBase + `units.has_aura = function() return false end`); err != nil {
			t.Fatal(err)
		}
		e.Func("spells.cast", "(id, guid) → Request", "", func(int, string) map[string]any {
			return e.Reqs.Now(script.Spec{Op: "spells.cast"}, script.Fail("out_of_range", "too far", nil))
		})
		if err := e.DoChunk("act.lua", actLua); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Eval(`task.spawn("main", function() result = act.buff(133, "42"); assert(not result.ok and result.id == nil) end)`); err != nil {
			t.Fatal(err)
		}
		e.CallTick()
		if ts := e.TasksHeld(); len(ts) != 1 || ts[0].State != "done" {
			t.Fatalf("tasks: %+v", ts)
		}
		e.Close()
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []types.Diagnostic
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var d types.Diagnostic
		if err := json.Unmarshal(sc.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		events = append(events, d)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 10 {
		t.Fatalf("events: %+v", events)
	}
	for _, offset := range []int{0, 5} {
		raw, cast, buff := events[offset+1], events[offset+2], events[offset+3]
		if raw.Op != "spells.cast" || raw.Request != 1 || raw.ParentCall != cast.Call || cast.ParentCall != buff.Call || buff.ParentCall != "" || buff.Code != "out_of_range" {
			t.Fatalf("nested calls: %+v", events[offset:offset+5])
		}
	}
	if events[0].Session == events[5].Session {
		t.Fatal("reconnect reused session identity")
	}
}
