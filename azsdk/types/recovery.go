package types

// Recovery is the bounded helper report, including partial work on failure.
// Lifecycle cancellation may only provide state and the last observed attempt count.
type Recovery struct {
	State         string                `json:"state" doc:"terminal: done, attention, needs_repair"`
	Attempts      int                   `json:"attempts" doc:"resurrection requests attempted, at most 2; includes immediate refusals"`
	Alive         *bool                 `json:"alive,omitempty" doc:"observed at helper completion; absent on coordinator cancellation"`
	Steps         []RecoveryStep        `json:"steps,omitempty"`
	FailedStep    string                `json:"failed_step,omitempty"`
	LastError     *Error                `json:"last_error,omitempty" doc:"underlying failure preserved when the terminal code is retry_exhausted"`
	Action        any                   `json:"action,omitempty" doc:"underlying failure data"`
	Control       any                   `json:"control,omitempty" doc:"ownership failure details when acquisition or scope release failed"`
	Health        *Resource             `json:"health,omitempty"`
	Power         *Resource             `json:"power,omitempty"`
	Gear          *Gear                 `json:"gear,omitempty"`
	Money         *uint32               `json:"money,omitempty"`
	RepairOptions []Creature            `json:"repair_options,omitempty" doc:"at most 20 observed repair NPCs; no automatic travel"`
	SpiritOption  *RecoverySpiritOption `json:"spirit_option,omitempty"`
}
type RecoveryStep struct {
	Op        string `json:"op"`
	OK        bool   `json:"ok"`
	RequestID int    `json:"request_id,omitempty"`
	Error     *Error `json:"error,omitempty"`
}
type RecoverySpiritOption struct {
	NPCGUID            GUID `json:"npc_guid"`
	DurabilityLoss     bool `json:"durability_loss" doc:"spirit healing has a durability penalty"`
	CostKnown          bool `json:"cost_known" doc:"currently false: the exact server-configured cost is not exposed"`
	RequiresPermission bool `json:"requires_permission,omitempty"`
}
