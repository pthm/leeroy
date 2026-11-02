package types

// Game log records: what a session keeps in its event stream file beside the events (lines with no seq; the agent
// never sees them), for the viewer and the run's report. Each is the data of one line, its type in the comment.

// State ("state"): the character, once a second.
type State struct {
	Position
	Level    uint32    `json:"level"`
	XP       uint32    `json:"xp" doc:"into this level"`
	Health   Resource  `json:"health"`
	Power    Resource  `json:"power"`
	Money    uint32    `json:"money" doc:"copper"`
	Group    uint32    `json:"group,omitempty" doc:"the server's group id (the low half of its GUID); none: not in a group"`
	Alive    bool      `json:"alive"`
	Ghost    bool      `json:"ghost,omitempty"`
	InCombat bool      `json:"in_combat,omitempty"`
	Broken   []string  `json:"broken,omitempty" doc:"gear slots at 0 durability (main_hand, chest, ...)"`
	Target   *UnitRef  `json:"target,omitempty"`
	Pet      *PetState `json:"pet,omitempty" doc:"the pet out: a hunter's beast, a warlock's demon, a water elemental"`
}

// PetState is a pet in its master's State.
type PetState struct {
	UnitRef
	X      float64  `json:"x"`
	Y      float64  `json:"y"`
	Z      float64  `json:"z"`
	Health Resource `json:"health"`
	Alive  bool     `json:"alive"`
}

// Nearby ("units"): the creatures within 60 yd, every 2 s. Players' pets are in their master's State; totems are here.
type Nearby struct {
	Map   uint32       `json:"map"`
	Units []NearbyUnit `json:"units"`
}

// NearbyUnit is [entry, level, x, y, health %, state]: state 0 hostile, 1 neutral, 2 friendly, +4 dead. A row, not an
// object: a week's run logs tens of millions.
type NearbyUnit [6]float64

// TransportState ("transports", a list of them): a boat or zeppelin on the character's map, every 4 s.
type TransportState struct {
	GUID  GUID    `json:"guid"`
	Entry uint32  `json:"entry"`
	Name  string  `json:"name"`
	Map   uint32  `json:"map"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	O     float64 `json:"o"`
}

// Login ("login"): the character entered the world.
type Login struct {
	Race  string `json:"race"`
	Class string `json:"class"`
	Level uint32 `json:"level"`
}

// GroupState ("group"): the character's party changed (the server resends it unchanged often: only changes).
type GroupState struct {
	Group   uint32   `json:"group,omitempty" doc:"the server's group id; none: left"`
	Leader  string   `json:"leader,omitempty"`
	Members []string `json:"members" doc:"names, the character's own included"`
}

// APICall ("api"): a daemon API call (not read-only or waiting ones).
type APICall struct {
	Method string `json:"method"`
	Params string `json:"params" doc:"raw JSON, cut at 500 characters"`
}

// RecordTypes: every game log record, as EventTypes lists the events (records aren't events: the agent never sees
// them, and the API references leave them out).
var RecordTypes = map[string]EventType{
	"state":      {State{}, "the character, every second"},
	"units":      {Nearby{}, "the creatures around it, every 2 s"},
	"transports": {[]TransportState{}, "the boats and zeppelins on its map, every 4 s"},
	"login":      {Login{}, "it entered the world"},
	"logout":     {None{}, "it left"},
	"group":      {GroupState{}, "its party changed"},
	"api":        {APICall{}, "a daemon API call"},
}
