package run

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// a restart's note: the tasks from before (tasks.json), autorun.lua's outcome, and the character now (a ghost here;
// an empty task list comes back from Lua as {})
func TestRestored(t *testing.T) {
	dir := t.TempDir()
	var evals []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var q struct{ Params struct{ Code string } }
		json.NewDecoder(req.Body).Decode(&q)
		evals = append(evals, q.Params.Code)
		res := `null`
		if strings.Contains(q.Params.Code, "task.list") {
			res = `{"alive":false,"ghost":true,"x":-9460.25,"y":62.5,"z":56,"map":0,"zone":"Elwynn Forest","area":"Goldshire","tasks":{}}`
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"result":%s}}`, res)
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	p := &player{r: &Runner{dir: dir}, dir: dir, ws: dir, port: port}
	os.WriteFile(filepath.Join(dir, "tasks.json"), []byte(`["main","healer"]`), 0o644)
	os.WriteFile(filepath.Join(dir, "autorun.lua"), []byte(`task.keep("main", main)`), 0o644)

	note := p.restored()
	for _, want := range []string{"Tasks running when it stopped: main, healer.", "- autorun.lua ran.", "Tasks running now: none.",
		"You are a ghost at -9460.2 62.5 56.0, map 0 (Elwynn Forest, Goldshire)."} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if len(evals) != 2 || !strings.Contains(evals[0], `dofile(LIB .. "autorun.lua")`) {
		t.Fatalf("evals: %q", evals)
	}
	if note := sessionNote(nil, false, "", charState{}, false); !strings.Contains(note, "stopped: unknown.") || strings.Contains(note, "You are") {
		t.Fatalf("note without state: %s", note)
	}
}
