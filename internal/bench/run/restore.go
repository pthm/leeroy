package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// charLua: the character's running tasks, whether it's alive, and where it is
// (by name: task.keep runs two tasks under one)
const charLua = `local p, l, names, seen = me.position(), me.location(), {}, {}
for _, t in ipairs(task.list()) do
	if t.state ~= "done" and t.state ~= "failed" and t.state ~= "stopped" and not seen[t.name] then
		seen[t.name] = true
		names[#names + 1] = t.name
	end
end
return {alive = me.alive(), ghost = me.ghost(), x = p.x, y = p.y, z = p.z, map = p.map, zone = l.zone, area = l.area,
	tasks = names}`

type charState struct {
	Alive, Ghost bool
	X, Y, Z      float64
	Map          int
	Zone, Area   string
	Tasks        []string
}

func (p *player) charState() (charState, error) {
	var st struct {
		charState
		Tasks json.RawMessage // an empty list comes back as {}
	}
	b, err := eval(context.Background(), p.port, charLua)
	if err == nil {
		err = json.Unmarshal(b, &st)
	}
	_ = json.Unmarshal(st.Tasks, &st.charState.Tasks) // {} for no tasks
	return st.charState, err
}

// saveTasks, before the session stops: the tasks it was running, in <agent>/tasks.json (a restart or resume tells
// the agent, since tasks don't survive the session)
func (p *player) saveTasks() {
	path := filepath.Join(p.dir, "tasks.json")
	_ = os.Remove(path) // there may be none
	p.mu.Lock()
	up := alive(p.session)
	p.mu.Unlock()
	if !up {
		return
	}
	if st, err := p.charState(); err == nil {
		b, _ := json.Marshal(st.Tasks)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			p.logf("tasks.json: %v", err)
		}
	}
}

// autorun runs the workspace's autorun.lua, if it has one, in the session that just logged in: "" when there's
// none, else what happened.
func (p *player) autorun() string {
	if _, err := os.Stat(filepath.Join(p.ws, "autorun.lua")); err != nil {
		return ""
	}
	if _, err := eval(context.Background(), p.port, `dofile(LIB .. "autorun.lua")`); err != nil {
		p.logf("autorun.lua: %v", err)
		return "autorun.lua failed: " + err.Error()
	}
	return "autorun.lua ran."
}

// restored runs autorun.lua after a restart or resume and returns the section its message ends with: the tasks
// that were running before, what autorun.lua did, and the character's state now.
func (p *player) restored() string {
	var before []string
	b, err := os.ReadFile(filepath.Join(p.dir, "tasks.json"))
	if err != nil || json.Unmarshal(b, &before) != nil {
		before = nil
	}
	ran := p.autorun()
	st, err := p.charState()
	if err != nil {
		p.logf("character state: %v", err)
	}
	return sessionNote(before, b != nil, ran, st, err == nil)
}

func sessionNote(before []string, known bool, ran string, st charState, now bool) string {
	var lines []string
	switch {
	case !known:
		lines = append(lines, "- Tasks running when it stopped: unknown.")
	case len(before) == 0:
		lines = append(lines, "- Tasks running when it stopped: none.")
	default:
		lines = append(lines, "- Tasks running when it stopped: "+strings.Join(before, ", ")+".")
	}
	if ran != "" {
		lines = append(lines, "- "+ran)
	}
	if now {
		tasks := "none"
		if len(st.Tasks) > 0 {
			tasks = strings.Join(st.Tasks, ", ")
		}
		lines = append(lines, "- Tasks running now: "+tasks+".")
		state := "alive"
		if st.Ghost {
			state = "a ghost"
		} else if !st.Alive {
			state = "dead"
		}
		lines = append(lines, fmt.Sprintf("- You are %s at %.1f %.1f %.1f, map %d (%s, %s).", state, st.X, st.Y, st.Z, st.Map, st.Zone, st.Area))
	}
	return "\n\nYOUR SESSION was restarted: Lua tasks and globals don't survive a restart; workspace files do, and the " +
		"session runs autorun.lua from your workspace after each login.\n" + strings.Join(lines, "\n") + "\n"
}
