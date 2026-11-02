package run

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSession answers script.eval like a session: in combat for the first fights polls, and records every eval.
func fakeSession(t *testing.T, fights int) (port int, evals func() []string) {
	var mu sync.Mutex
	var got []string
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Params struct{ Code string } }
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		defer mu.Unlock()
		code := req.Params.Code
		got = append(got, code)
		result := "null"
		if code == `return me.in_combat()` {
			polls++
			result = strconv.FormatBool(polls <= fights)
		}
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"result":` + result + `}}`))
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ = strconv.Atoi(u.Port())
	return port, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }
}

// calm leaves the character's scripts fighting while it's in combat and stops them the moment it isn't.
func TestCalmStopsScriptsOutOfCombat(t *testing.T) {
	calmFight, calmPoll = 2*time.Second, 10*time.Millisecond
	port, evals := fakeSession(t, 3)
	calm(port)
	got := evals()
	stopAt := -1
	for i, c := range got {
		if strings.Contains(c, "task.stop") {
			stopAt = i
			break
		}
	}
	if stopAt != 4 { // three in-combat polls, the out-of-combat poll, then the stop
		t.Fatalf("scripts stopped at eval %d, want 4: %q", stopAt, got)
	}
	for _, c := range got {
		if strings.Contains(c, `task.spawn("calm"`) {
			t.Fatal("fell back to defending although the fight ended in time")
		}
	}
}

// A fight that outlasts calmFight: the scripts are stopped and it defends itself until the fight ends.
func TestCalmDefendsWhenFightDrags(t *testing.T) {
	calmFight, calmPoll = 50*time.Millisecond, 10*time.Millisecond
	port, evals := fakeSession(t, 1000)
	calm(port)
	defended := false
	for _, c := range evals() {
		if strings.Contains(c, `task.spawn("calm"`) && strings.Contains(c, "task.stop") {
			defended = true
		}
	}
	if !defended {
		t.Fatalf("no defend task after a fight that dragged on: %q", evals())
	}
}
