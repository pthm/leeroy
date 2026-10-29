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

// calm, before a session is stopped: get the character out of combat, then stop its scripts. A connection dropped in
// combat leaves the character in the world, where it can die with nobody logged in; and a character whose scripts
// are stopped mid-fight stands still until it dies. So while it's fighting its own scripts keep fighting, and they're
// stopped at the first moment it's out of combat (before they can pull again). If it's still in combat after a
// minute, its scripts aren't winning: they're stopped and it auto-attacks whatever attacks it until the fight ends.
// Call it after the agent is stopped, so nothing new is written to the session.
func calm(port int) {
	ctx := context.Background()
	const stop = `for _, t in ipairs(task.list()) do task.stop(t.id) end; on_tick = nil; move.stop()`
	inCombat := func() (bool, error) {
		r, err := eval(ctx, port, `return me.in_combat()`)
		return string(r) == "true", err
	}
	for end := time.Now().Add(calmFight); ; time.Sleep(calmPoll) {
		fighting, err := inCombat()
		if err != nil {
			return
		}
		if !fighting {
			eval(ctx, port, stop)
			return
		}
		if time.Now().After(end) {
			break
		}
	}
	eval(ctx, port, stop+`; task.spawn("calm", function()
		while me.in_combat() do
			local a = units.attackers(40)[1]
			if a then combat.attack(a.guid) end
			wait(1)
		end
	end)`)
	for end := time.Now().Add(calmFight); time.Now().Before(end); time.Sleep(calmPoll) {
		if fighting, err := inCombat(); err != nil || !fighting {
			break
		}
	}
	eval(ctx, port, stop)
}

// calm's timing (variables for the test)
var calmFight, calmPoll = time.Minute, 500 * time.Millisecond
