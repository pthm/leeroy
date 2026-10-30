package types

// Diagnostic is a versioned, bounded observation. A session event is identified by (session, seq).
// A harness event instead uses (artifact path, seq). Agent claims never establish game outcomes.
type Diagnostic struct {
	Schema     int               `json:"schema"`
	Seq        uint64            `json:"seq"`
	T          int64             `json:"t"`
	Agent      string            `json:"agent"`
	Session    string            `json:"session,omitempty"`
	Task       string            `json:"task,omitempty"`
	TaskID     int               `json:"task_id,omitempty"`
	Call       string            `json:"call,omitempty"`
	ParentCall string            `json:"parent_call,omitempty"`
	Request    int               `json:"request,omitempty"`
	Kind       string            `json:"kind"`
	Op         string            `json:"op,omitempty"`
	Outcome    string            `json:"outcome,omitempty"`
	Code       string            `json:"code,omitempty"`
	DurationMS int64             `json:"duration_ms"`
	Details    DiagnosticDetails `json:"details"`
}

// DiagnosticDetails is an allowlist: arbitrary request arguments, results, error messages, commands,
// prompts and chat are never copied here. Status is explicitly agent-authored and limited to 512 bytes.
type DiagnosticDetails struct {
	Target      string                 `json:"target,omitempty"`
	Destination *DiagnosticDestination `json:"destination,omitempty"`
	SpellID     uint32                 `json:"spell_id,omitempty"`
	QuestID     uint32                 `json:"quest_id,omitempty"`
	NPCEntry    uint32                 `json:"npc_entry,omitempty"`
	ItemEntry   uint32                 `json:"item_entry,omitempty"`
	Attempts    int                    `json:"attempts,omitempty"`
	Status      string                 `json:"status,omitempty"`
	AgentClaim  bool                   `json:"agent_claim,omitempty"`
	Source      string                 `json:"source,omitempty"`
	Line        int                    `json:"line,omitempty"`
	State       string                 `json:"state,omitempty"`
	Component   string                 `json:"component,omitempty"`
	Runtime     string                 `json:"runtime,omitempty"`
	ExitCode    *int                   `json:"exit_code,omitempty"`
}

// DiagnosticDestination preserves omitted altitude/map rather than inventing a zero floor or map.
type DiagnosticDestination struct {
	X   float64  `json:"x"`
	Y   float64  `json:"y"`
	Z   *float64 `json:"z,omitempty"`
	Map *uint32  `json:"map,omitempty"`
}
