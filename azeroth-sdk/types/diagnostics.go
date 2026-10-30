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
	Route           *DiagnosticRoute `json:"route,omitempty"`
	Movement        *MovementOwner   `json:"movement,omitempty"`
	Alive           *bool            `json:"alive,omitempty"`
	SampleAvailable *bool            `json:"sample_available,omitempty"`

	Gear          *DiagnosticGear        `json:"gear,omitempty"`
	Target        string                 `json:"target,omitempty"`
	Destination   *DiagnosticDestination `json:"destination,omitempty"`
	SpellID       uint32                 `json:"spell_id,omitempty"`
	QuestID       uint32                 `json:"quest_id,omitempty"`
	NPCEntry      uint32                 `json:"npc_entry,omitempty"`
	ItemEntry     uint32                 `json:"item_entry,omitempty"`
	Attempts      int                    `json:"attempts,omitempty"`
	Status        string                 `json:"status,omitempty"`
	AgentClaim    bool                   `json:"agent_claim,omitempty"`
	Source        string                 `json:"source,omitempty"`
	Line          int                    `json:"line,omitempty"`
	State         string                 `json:"state,omitempty"`
	Component     string                 `json:"component,omitempty"`
	Runtime       string                 `json:"runtime,omitempty"`
	ExitCode      *int                   `json:"exit_code,omitempty"`
	Tool          string                 `json:"tool,omitempty"`
	CommandFamily string                 `json:"command_family,omitempty"`
	ToolCoverage  bool                   `json:"tool_coverage,omitempty"`
	DurationKnown *bool                  `json:"duration_known,omitempty"`
	QuestLog      *DiagnosticQuestLog    `json:"quest_log,omitempty"`
	Group         *DiagnosticGroup       `json:"group,omitempty"`
}

// DiagnosticQuestLog is a complete observed client quest log, at most 25 entries. Removal from the log
// does not establish turn-in: a quest can also be abandoned. Available=false means character fields are absent.
type DiagnosticQuestLog struct {
	Available bool              `json:"available"`
	Quests    []DiagnosticQuest `json:"quests"`
}
type DiagnosticQuest struct {
	ID       uint32    `json:"id"`
	Complete bool      `json:"complete"`
	Failed   bool      `json:"failed"`
	Counters [4]uint32 `json:"counters" doc:"raw objective counters from character fields; inventory objectives require separate item observations"`
}

// DiagnosticGroup records the server's roster, at most 40 members including self. It does not establish
// a shared plan, readiness, visibility or fresh positions. Names and chat are deliberately excluded.
type DiagnosticGroup struct {
	InGroup bool   `json:"in_group"`
	ID      GUID   `json:"id"`
	Leader  GUID   `json:"leader"`
	Members []GUID `json:"members"`
}

// DiagnosticDestination preserves omitted altitude/map rather than inventing a zero floor or map.
type DiagnosticDestination struct {
	X   float64  `json:"x"`
	Y   float64  `json:"y"`
	Z   *float64 `json:"z,omitempty"`
	Map *uint32  `json:"map,omitempty"`
}

// DiagnosticGear reports observed equipment condition, not a repair plan.
type DiagnosticGear struct {
	Money          uint32   `json:"money"`
	MainHandBroken bool     `json:"main_hand_broken"`
	BrokenSlots    []string `json:"broken_slots"`
}

// DiagnosticRoute omits the potentially large waypoint arrays while retaining
// the geometric evidence needed to explain a terminal route result.
type DiagnosticRoute struct {
	Source        string   `json:"source"`
	Status        string   `json:"status"`
	From          Position `json:"from"`
	Destination   Position `json:"destination"`
	Distance      float64  `json:"distance"`
	Gap           *float64 `json:"gap,omitempty"`
	FloorValid    *bool    `json:"floor_valid,omitempty"`
	CompletedLegs int      `json:"completed_legs,omitempty"`
	BlockedLeg    int      `json:"blocked_leg,omitempty"`
}
