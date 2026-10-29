package types

// Event data: one struct per event type (EventTypes maps each type to its struct and says when it happens).

type Damage struct {
	Src       UnitRef `json:"src"`
	Dst       UnitRef `json:"dst"`
	Amount    uint32  `json:"amount"`
	Spell     uint32  `json:"spell,omitempty" doc:"0 for melee"`
	SpellName string  `json:"spell_name,omitempty"`
	Taken     bool    `json:"taken" doc:"you're dst"`
}

type Kill struct {
	Victim UnitRef  `json:"victim"`
	Killer *UnitRef `json:"killer,omitempty" doc:"party_kill only: the party member who killed it"`
}

type Death struct {
	Killer *UnitRef `json:"killer,omitempty" doc:"who last hit you"`
}

type XPGain struct {
	Amount uint32   `json:"amount"`
	Source string   `json:"source" doc:"kill, or other (quests, exploring)"`
	Victim *UnitRef `json:"victim,omitempty"`
}

type LevelUp struct {
	Level uint32 `json:"level"`
}

type Cast struct {
	Spell       uint32 `json:"spell"`
	Name        string `json:"name"`
	CastMS      uint32 `json:"cast_ms,omitempty" doc:"cast_start: how long it takes"`
	Code        string `json:"code,omitempty" doc:"cast_failed: the reason (out_of_range, line_of_sight, not_ready, interrupted, ...)"`
	Hint        string `json:"hint,omitempty"`
	Interrupted bool   `json:"interrupted,omitempty"`
}

type AttackStart struct {
	Target GUID `json:"target"`
}

type AttackFailed struct {
	Code   string `json:"code" doc:"not_in_range (move closer), bad_facing (move.face), dead_target, cant_attack"`
	Target GUID   `json:"target"`
}

type ItemReceived struct {
	Item Item   `json:"item" doc:"entry, name, quality, count"`
	From string `json:"from" doc:"loot, or npc (quest rewards and purchases)"`
}

type Money struct {
	Amount uint32 `json:"amount" doc:"copper"`
	Source string `json:"source" doc:"loot"`
}

type QuestProgress struct {
	Quest uint32 `json:"quest"`
	Entry uint32 `json:"entry" doc:"the creature or object"`
	Have  uint32 `json:"have"`
	Need  uint32 `json:"need"`
}

type QuestDone struct {
	Quest uint32 `json:"quest"`
	Title string `json:"title,omitempty"`
	XP    uint32 `json:"xp,omitempty" doc:"quest_turned_in"`
	Money uint32 `json:"money,omitempty" doc:"quest_turned_in"`
}

type Learned struct {
	Spell uint32 `json:"spell"`
	Name  string `json:"name"`
}

type AchievementEarned struct {
	ID uint32 `json:"id"`
}

type GearBroken struct {
	Items   []Item `json:"items"`
	Message string `json:"message"`
}

type Chat struct {
	Kind    string `json:"kind" doc:"say, yell, party, whisper, whisper_sent (yours, echoed), channel, system, emote, npc_say, npc_yell, npc_whisper, npc_emote"`
	From    string `json:"from"`
	GUID    GUID   `json:"guid"`
	Channel string `json:"channel,omitempty"`
	Text    string `json:"text"`
}

type PartyInvite struct {
	From string `json:"from" doc:"accept with group.accept(), or group.decline()"`
}

type PartyChanged struct {
	InGroup bool     `json:"in_group"`
	Members []string `json:"members"`
}

type Notice struct {
	Message string `json:"message"`
}

type ErrorEvent struct {
	Op      string `json:"op" doc:"items, vendor.buy, vendor.sell, trainer.learn, group, quests"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Log struct {
	Msg  string `json:"msg"`
	Task string `json:"task,omitempty"`
}

type TaskEvent struct {
	Task    int    `json:"task"`
	Name    string `json:"name"`
	State   string `json:"state,omitempty" doc:"task_finished: done, failed or stopped"`
	Seconds int    `json:"seconds,omitempty"`
	Error   string `json:"error,omitempty"`
}

type ScriptError struct {
	Where string `json:"where"`
	Error string `json:"error"`
}

type Attention struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Default  string   `json:"default,omitempty"`
	Task     string   `json:"task,omitempty"`
}

type Answer struct {
	ID     uint64 `json:"id"`
	Choice string `json:"choice"`
}

// FightSummary: one fight, from entering combat to leaving it (or dying).
type FightSummary struct {
	Outcome       string         `json:"outcome" doc:"won (you killed something and left combat), died, or ended (left combat without a kill: it fled, evaded or you got away)"`
	Seconds       float64        `json:"seconds"`
	Enemies       []UnitRef      `json:"enemies" doc:"everything that hit you or that you hit"`
	Kills         []UnitRef      `json:"kills"`
	DamageDealt   uint32         `json:"damage_dealt"`
	DamageTaken   uint32         `json:"damage_taken"`
	HealingDone   uint32         `json:"healing_done"`
	HealingTaken  uint32         `json:"healing_taken"`
	DPS           float64        `json:"dps" doc:"damage dealt per second of the fight"`
	HealthStart   float64        `json:"health_start" doc:"fraction 0-1"`
	HealthEnd     float64        `json:"health_end"`
	PowerStart    float64        `json:"power_start" doc:"fraction 0-1 (mana, rage, energy)"`
	PowerEnd      float64        `json:"power_end"`
	XP            uint32         `json:"xp"`
	Abilities     map[string]int `json:"abilities" doc:"casts that went off, by spell"`
	FailedCasts   map[string]int `json:"failed_casts,omitempty" doc:"by reason (out_of_range, no_power, not_ready...)"`
	SwingsRefused int            `json:"swings_refused,omitempty" doc:"melee swings the server refused (not_in_range, bad_facing)"`
}

type None struct{}

// EventType describes one event type for the generated references.
type EventType struct {
	Data any    // zero value of the data struct
	When string // when it happens
}

// EventTypes: every event the runtime emits.
var EventTypes = map[string]EventType{
	"damage":           {Damage{}, "a hit involving you (melee or spell)"},
	"heal":             {Damage{}, "a heal involving you"},
	"kill":             {Kill{}, "you killed something"},
	"party_kill":       {Kill{}, "a party member killed something"},
	"death":            {Death{}, "you died"},
	"resurrected":      {None{}, "you're alive again"},
	"combat_start":     {None{}, "you entered combat"},
	"combat_end":       {None{}, "you left combat"},
	"xp":               {XPGain{}, "you gained XP"},
	"level_up":         {LevelUp{}, "you reached a new level"},
	"cast_start":       {Cast{}, "a cast with a cast time began"},
	"cast_done":        {Cast{}, "a spell went off"},
	"cast_failed":      {Cast{}, "a cast failed or was interrupted"},
	"attack_start":     {AttackStart{}, "your auto-attack started"},
	"attack_failed":    {AttackFailed{}, "the server refused a swing"},
	"item_received":    {ItemReceived{}, "an item went into your bags"},
	"money":            {Money{}, "you looted money"},
	"quest_progress":   {QuestProgress{}, "a quest kill/object counter went up"},
	"quest_complete":   {QuestDone{}, "a quest's objectives are done: turn it in"},
	"quest_turned_in":  {QuestDone{}, "you turned a quest in"},
	"learned":          {Learned{}, "you learned a spell from a trainer"},
	"achievement":      {AchievementEarned{}, "you earned an achievement"},
	"area_changed":     {Location{}, "you entered another zone or area"},
	"teleported":       {Teleport{}, "you were teleported: a portal, hearthstone, summon, the graveyard, an instance"},
	"flight_start":     {Flight{}, "you took off: the server flies you, and move.to is refused until you land"},
	"flight_end":       {Flight{}, "you landed"},
	"flight_learned":   {FlightLearned{}, "you discovered a flight point"},
	"movement_changed": {MovementChanged{}, "who moves you changed: a flight, a teleport, fear, a stun or a root began or ended"},
	"gear_broken":      {GearBroken{}, "you revived with equipped items at 0 durability"},
	"chat":             {Chat{}, "a message to you, near you or in your channels"},
	"party_invite":     {PartyInvite{}, "someone invited you to their group"},
	"party_changed":    {PartyChanged{}, "your group changed"},
	"notice":           {Notice{}, "a server notice (\"Inventory is full\")"},
	"trade":            {Trade{}, "the trade window changed: someone asked (requested: trade.open), opened, accepted, completed or cancelled"},
	"error":            {ErrorEvent{}, "the server refused something"},
	"result":           {Result{}, "an action finished (its result)"},
	"log":              {Log{}, "log(msg) from a script"},
	"task_started":     {TaskEvent{}, "a task started"},
	"task_finished":    {TaskEvent{}, "a task ended (done, failed or stopped)"},
	"script_error":     {ScriptError{}, "a script raised an error"},
	"attention":        {Attention{}, "a script asks the agent (ask)"},
	"answer":           {Answer{}, "the agent answered (answer)"},
}

// Action results: the data of an ok result, per op (ops not listed have none).

type MoveDone struct {
	Distance float64 `json:"distance" doc:"yards short of the point"`
	Stopped  bool    `json:"stopped,omitempty" doc:"move.stop() ended it"`
	NoPath   bool    `json:"no_path,omitempty" doc:"no navmesh path: it walked a straight line"`
}

type CastDone struct {
	Spell uint32 `json:"spell"`
}

type Looted struct {
	Items []Item `json:"items"`
	Money uint32 `json:"money" doc:"copper"`
}

type Sold struct {
	Money uint32 `json:"money" doc:"copper gained"`
	Count int    `json:"count,omitempty" doc:"vendor.sell_junk: stacks sold"`
}

type Repaired struct {
	Fixed        int   `json:"fixed"`
	Cost         int64 `json:"cost" doc:"copper"`
	StillDamaged int   `json:"still_damaged"`
}

type TalentLearned struct {
	Rank int `json:"rank"`
}

type Attacking struct {
	Already bool `json:"already,omitempty" doc:"you were already attacking it"`
}

type Equipped struct {
	Slot string `json:"slot"`
}

var OpResults = map[string]any{
	"move.to": MoveDone{}, "move.stop": MoveDone{}, "spells.cast": CastDone{}, "spells.cast_at": CastDone{},
	"items.use": CastDone{}, "objects.use": CastDone{}, "items.loot": Looted{}, "items.equip": Equipped{},
	"vendor.buy": ItemReceived{}, "vendor.sell": Sold{}, "vendor.sell_junk": Sold{}, "vendor.repair": Repaired{},
	"trainer.learn": Learned{}, "quests.turn_in": QuestDone{}, "quests.accept": QuestDone{}, "talents.learn": TalentLearned{},
	"combat.attack": Attacking{}, "flights.map": FlightMap{}, "flights.take": Flight{},
}
