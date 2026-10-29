package run

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/pthm/leeroy/azbench/config"
)

// noteOpenrouter keeps the key's spend at the first start and the last finish, and only for runs that use it.
func TestNoteOpenrouter(t *testing.T) {
	usage := 10.5
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"usage": usage}})
	}))
	defer srv.Close()
	openrouterURL = srv.URL
	t.Setenv("OPENROUTER_API_KEY", "sk-test")
	dir := t.TempDir()
	r := &Runner{dir: dir, players: []*player{{a: config.Agent{Provider: "openrouter"}}}}
	r.noteOpenrouter(false)
	usage = 11
	r.noteOpenrouter(false) // a resume: the start stays
	usage = 14.25
	r.noteOpenrouter(true)
	b, _ := os.ReadFile(filepath.Join(dir, "openrouter.json"))
	if string(b) != `{"end":14.25,"start":10.5}` {
		t.Fatalf("openrouter.json = %s", b)
	}
	other := t.TempDir()
	(&Runner{dir: other, players: []*player{{a: config.Agent{Runtime: "claude"}}}}).noteOpenrouter(false)
	if _, err := os.Stat(filepath.Join(other, "openrouter.json")); err == nil {
		t.Fatal("wrote openrouter.json for a run that doesn't use OpenRouter")
	}
}
