package types

// VendorOperation identifies one requested step, including the purchase that failed or remains unattempted.
type VendorOperation struct {
	Op        string `json:"op"`
	ItemEntry uint32 `json:"item_entry,omitempty"`
	Count     uint32 `json:"count,omitempty"`
}

// VendorErrand preserves completed work on failure. Remaining starts with the failed operation; retrying it is the caller's decision.
type VendorErrand struct {
	Sold      *Sold             `json:"sold,omitempty"`
	Repaired  *Repaired         `json:"repaired,omitempty"`
	Bought    []ItemReceived    `json:"bought"`
	Completed []VendorOperation `json:"completed"`
	Remaining []VendorOperation `json:"remaining"`
}

// TrainErrand lists spell IDs. AlreadyKnown is observed before training; Remaining is unattempted work after a failure.
type TrainErrand struct {
	Learned      []uint32 `json:"learned"`
	AlreadyKnown []uint32 `json:"already_known"`
	Failed       []uint32 `json:"failed"`
	Remaining    []uint32 `json:"remaining"`
}

// Consumed identifies the selected item and preserves the underlying use/destroy result data.
// Only ok=true confirms the item was used or destroyed.
type Consumed struct {
	Item   Item `json:"item"`
	Action any  `json:"action,omitempty"`
}

// ObjectUse counts confirmed uses separately from attempted uses; quest progress is observed from the log.
type ObjectUse struct {
	Used     int    `json:"used"`
	Attempts int    `json:"attempts"`
	Status   string `json:"status,omitempty" doc:"already_complete, quest_complete, quest_progress, count_reached"`
}

// Rested reports the observed fractions even when the requested thresholds were not reached.
type Rested struct {
	HP   float64 `json:"hp"`
	Mana float64 `json:"mana"`
}
