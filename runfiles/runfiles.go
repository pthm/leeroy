// Package runfiles is a run directory's layout: where the harness writes each file, and where the judge, the report,
// publish and the viewer read it. One run, bench/runs/<time>-<task>[-<model>]/:
//
//	run.json              the manifest: task, players, benchmark version, SDK revision, start, end, status (when
//	                      each player played, its pauses and outages: the harness's runtime_state diagnostics)
//	scenario.yaml         the run as made from its task (resume reads it)
//	preflight.json        what the world looked like for the task before it started
//	supervisor.log        everything the supervisor said, alerts marked ALERT
//	report.json, team.json   the report card and the team's numbers, at the end
//	board/                the judges' live standings (the agents' standings tool reads it)
//	agents/<agent>/       the agent: transcript.jsonl (the runtime's own), trajectory.json (ATIF), meta.json,
//	                      prompt.md, usage.json, the notes it kept, its kit
//	game/<char>/          the character: events.jsonl, its game log (the events its agent saw, and its state each
//	                      second, nearby creatures, transports, API calls: gamelog reads it), session.log
//	judge/<char>/         samples.jsonl (every 15 s), result.json (the final score)
//	diagnostics/<agent>.jsonl   harness and session diagnostics, one stream
//	.live/                the running supervisor's files: control.json, pids (gone when the run ends)
//
// Every JSONL line has t, unix milliseconds. The trajectory is a JSON document (ATIF), its timestamps ISO 8601.
package runfiles

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Run is a run directory.
type Run string

func (r Run) join(p ...string) string { return filepath.Join(append([]string{string(r)}, p...)...) }

func (r Run) Manifest() string      { return r.join("run.json") }
func (r Run) Scenario() string      { return r.join("scenario.yaml") }
func (r Run) Preflight() string     { return r.join("preflight.json") }
func (r Run) SupervisorLog() string { return r.join("supervisor.log") }
func (r Run) Report() string        { return r.join("report.json") }
func (r Run) Team() string          { return r.join("team.json") }
func (r Run) Board() string         { return r.join("board") }
func (r Run) CodexQuota() string    { return r.join("codex-quota.json") }
func (r Run) Live(name string) string {
	return r.join(".live", name)
}

// Agent is an agent's directory; AgentFile one of its files.
func (r Run) Agent(agent string) string           { return r.join("agents", agent) }
func (r Run) AgentFile(agent, name string) string { return r.join("agents", agent, name) }
func (r Run) Transcript(agent string) string      { return r.AgentFile(agent, "transcript.jsonl") }
func (r Run) Trajectory(agent string) string      { return r.AgentFile(agent, "trajectory.json") }
func (r Run) Meta(agent string) string            { return r.AgentFile(agent, "meta.json") }
func (r Run) Diagnostics(agent string) string     { return r.join("diagnostics", agent+".jsonl") }
func (r Run) Game(char string) string             { return r.join("game", char) }
func (r Run) Events(char string) string           { return r.join("game", char, "events.jsonl") }
func (r Run) SessionLog(char string) string       { return r.join("game", char, "session.log") }
func (r Run) Judge(char string) string            { return r.join("judge", char) }
func (r Run) Samples(char string) string          { return r.join("judge", char, "samples.jsonl") }
func (r Run) Result(char string) string           { return r.join("judge", char, "result.json") }
func (r Run) EventsGlob() string                  { return r.join("game", "*", "events.jsonl") }
func (r Run) CharOfEvents(path string) string     { return filepath.Base(filepath.Dir(path)) }
func (r Run) AgentsGlob() string                  { return r.join("agents", "*") }
func (r Run) IsRun() bool                         { return exists(r.Manifest()) }

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Manifest is run.json: what the run was, and how it went.
type Manifest struct {
	Task     string   `json:"task"`     // the task it was made from
	Scenario string   `json:"scenario"` // the run's own name (<task>-<model>)
	Version  string   `json:"version"`  // the benchmark version (bench/versions.yaml)
	SDKRev   string   `json:"sdk_rev"`  // the SDK revision it started on
	Goal     string   `json:"goal"`
	Minutes  int      `json:"minutes"`
	Hardcore bool     `json:"hardcore,omitempty"`
	Players  []Player `json:"players"`
	Started  int64    `json:"started,omitempty"` // unix ms: the clock started (all sessions up)
	Ended    int64    `json:"ended,omitempty"`   // unix ms: the run finished
	Status   string   `json:"status"`            // starting, running, paused, finished
}

// Player is one agent and the character it played, as configured (agents/<agent>/meta.json has what the runtime
// reported).
type Player struct {
	Agent   string `json:"agent"`
	Char    string `json:"char"`
	Race    string `json:"race"`
	Class   string `json:"class"`
	Spec    string `json:"spec,omitempty"`
	Runtime string `json:"runtime"`
	Version string `json:"version,omitempty"` // the runtime's, its --version when the agent started
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Kit     string `json:"kit,omitempty"` // a starting kit's level, as "level 60", when it had one
}

// ReadManifest reads run.json.
func (r Run) ReadManifest() (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(r.Manifest())
	if err == nil {
		err = json.Unmarshal(b, &m)
	}
	return m, err
}

// WriteManifest writes run.json.
func (r Run) WriteManifest(m Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.Manifest(), append(b, '\n'), 0o644)
}
