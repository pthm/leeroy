package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// eval runs Lua in a session (the daemon on port) and returns its result field.
func eval(ctx context.Context, port int, code string) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "script.eval", "params": map[string]any{"code": code}})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://127.0.0.1:%d/rpc", port), bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r struct {
		Result struct {
			Result json.RawMessage `json:"result"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if r.Error != nil {
		return nil, fmt.Errorf("%s", r.Error.Message)
	}
	return r.Result.Result, nil
}

// calm, before a session is stopped: stop its scripts and wait up to 90 s for it to leave combat. A connection
// dropped in combat leaves the character in the world, where it can die with nobody logged in. Call it after the
// agent is stopped, so nothing starts a new fight.
func calm(port int) {
	ctx := context.Background()
	if _, err := eval(ctx, port, `for _, t in ipairs(task.list()) do task.stop(t.id) end; on_tick = nil; move.stop()`); err != nil {
		return
	}
	for range 45 {
		r, err := eval(ctx, port, `return me.in_combat()`)
		if err != nil || string(r) != "true" {
			return
		}
		time.Sleep(2 * time.Second)
	}
}
