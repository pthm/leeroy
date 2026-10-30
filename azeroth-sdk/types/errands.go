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
