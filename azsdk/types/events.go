package types

// Event data: one struct per event type (EventTypes maps each type to its struct and says when it happens).

// Damage is a hit or heal involving you (damage, heal): melee, a spell, a DoT or HoT tick, or an attack that missed.
type Damage struct {
	Src       UnitRef `json:"src"`
	Dst       UnitRef `json:"dst"`
	Amount    uint32  `json:"amount" doc:"for heals: what it actually healed (overheal excluded)"`
	Overheal  uint32  `json:"overheal,omitempty" doc:"heals: the part that healed past full health"`
	Spell     uint32  `json:"spell,omitempty" doc:"0 for melee"`
	SpellName string  `json:"spell_name,omitempty"`
	Taken     bool    `json:"taken" doc:"you're dst"`
	Outcome   string  `json:"outcome,omitempty" doc:"melee: hit, crit, glancing, crushing, block (partly blocked: amount > 0); any: miss, dodge, parry, block, resist, absorb, evade (it's resetting: unreachable), immune, deflect, reflect, instakill; misses have amount 0"`
	Periodic  bool    `json:"periodic,omitempty" doc:"a damage or heal over time tick (DoT, HoT)"`
	Cause     string  `json:"cause,omitempty" doc:"environmental damage (no src): fall, drowning, lava, slime, fire, fatigue (swimming too far out)"`
}

// UnitCast is a unit starting a cast at you or a party member (unit_cast_start).
type UnitCast struct {
	Unit   UnitRef `json:"unit"`
	Spell  uint32  `json:"spell"`
	Name   string  `json:"name"`
	CastMS uint32  `json:"cast_ms"`
	Target UnitRef `json:"target"`
}

// Kill is a death you or a party member caused (kill, party_kill).
type Kill struct {
	Victim UnitRef  `json:"victim"`
	Killer *UnitRef `json:"killer,omitempty" doc:"party_kill only: who landed the killing blow (the credit, XP and loot, go to whoever tapped it)"`
}

// Death is your own death (death).
type Death struct {
	Killer *UnitRef `json:"killer,omitempty" doc:"who last hit you"`
	Cause  string   `json:"cause,omitempty" doc:"when the environment killed you: fall, drowning, lava, slime, fire, fatigue"`
}

// ResurrectOffer is another player casting a resurrection on you (resurrect_offered).
type ResurrectOffer struct {
	From     UnitRef `json:"from"`
	Sickness bool    `json:"sickness" doc:"accepting gives resurrection sickness (offers from NPCs)"`
}

// MirrorTimer is your breath (underwater), fatigue (deep water) or fire timer (mirror_timer, me.timers).
type MirrorTimer struct {
	Timer       string  `json:"timer" doc:"breath, fatigue or fire"`
	State       string  `json:"state" doc:"running (counting down: you take damage at 0), recovering (refilling), paused, stopped"`
	SecondsLeft float64 `json:"seconds_left"`
	MaxS        float64 `json:"max_s"`
}

// XPGain is XP you gained (xp).
type XPGain struct {
	Amount uint32   `json:"amount"`
	Source string   `json:"source" doc:"kill, or other (quests, exploring)"`
	Victim *UnitRef `json:"victim,omitempty"`
}

// LevelUp is the level you reached (level_up).
type LevelUp struct {
	Level uint32 `json:"level"`
}

// Cast is one of your casts, or your pet's, starting, going off or failing (cast_start, cast_done, cast_failed,
// pet_cast_done, pet_cast_failed; the pet.cast, pet.cast_at and pet.feed results).
type Cast struct {
	Count       uint8       `json:"count" doc:"protocol cast sequence; zero may be an unsolicited/server cast"`
	Spell       uint32      `json:"spell"`
	Name        string      `json:"name"`
	CastMS      uint32      `json:"cast_ms,omitempty" doc:"cast_start: how long it takes"`
	Code        string      `json:"code,omitempty" doc:"cast_failed: the reason (out_of_range, line_of_sight, not_ready, interrupted, ...)"`
	Detail      string      `json:"detail,omitempty" doc:"cast_failed: what the reason is about (the missing reagent or tool, the spell focus or area needed)"`
	Hint        string      `json:"hint,omitempty"`
	Interrupted bool        `json:"interrupted,omitempty"`
	Hits        int         `json:"hits,omitempty" doc:"cast_done: how many targets it landed on"`
	Misses      []SpellMiss `json:"misses,omitempty" doc:"cast_done: targets it went off at but didn't land on"`
}

// SpellMiss is a target a spell went off at but didn't land on.
type SpellMiss struct {
	Unit    UnitRef `json:"unit"`
	Outcome string  `json:"outcome" doc:"miss, resist, dodge, parry, block, evade, immune, deflect, absorb, reflect"`
}

// Energize is power (mana, rage, energy, runic power) given to you, or by you to another (energize).
type Energize struct {
	Src       UnitRef `json:"src"`
	Dst       UnitRef `json:"dst"`
	Spell     uint32  `json:"spell"`
	SpellName string  `json:"spell_name,omitempty"`
	Power     string  `json:"power"`
	Amount    uint32  `json:"amount"`
	Periodic  bool    `json:"periodic,omitempty"`
}

// Dispel is auras removed (or stolen) from you, or by you (dispel).
type Dispel struct {
	Src       UnitRef  `json:"src" doc:"the dispeller"`
	Dst       UnitRef  `json:"dst" doc:"who lost the auras"`
	Spell     uint32   `json:"spell"`
	SpellName string   `json:"spell_name,omitempty"`
	Removed   []string `json:"removed" doc:"the auras' names"`
	Stolen    bool     `json:"stolen,omitempty" doc:"spellsteal: the dispeller has them now"`
}

// AttackStart is your auto-attack starting on a target (attack_start).
type AttackStart struct {
	Target GUID `json:"target"`
}

// AttackFailed is a melee swing the server refused (attack_failed).
type AttackFailed struct {
	Code   string `json:"code" doc:"not_in_range (move closer), bad_facing (move.face), dead_target, cant_attack"`
	Target GUID   `json:"target"`
}

// ItemReceived is an item going into your bags (item_received; the vendor.buy result).
type ItemReceived struct {
	Item Item   `json:"item" doc:"entry, name, quality, count"`
	From string `json:"from" doc:"loot, or npc (quest rewards and purchases)"`
}

// Money is copper you looted (money).
type Money struct {
	Amount uint32 `json:"amount" doc:"copper"`
	Source string `json:"source" doc:"loot"`
}

// QuestProgress is a quest's kill or object counter going up (quest_progress).
type QuestProgress struct {
	Quest uint32 `json:"quest"`
	Entry uint32 `json:"entry" doc:"the creature or object"`
	Have  uint32 `json:"have"`
	Need  uint32 `json:"need"`
}

// QuestDone is a quest whose objectives are done, that you turned in or that failed (quest_complete,
// quest_turned_in, quest_failed; the quests.accept and quests.turn_in results).
type QuestDone struct {
	Quest  uint32 `json:"quest"`
	Title  string `json:"title,omitempty"`
	XP     uint32 `json:"xp,omitempty" doc:"quest_turned_in"`
	Money  uint32 `json:"money,omitempty" doc:"quest_turned_in"`
	Reason string `json:"reason,omitempty" doc:"quest_failed: failed (an escort died, you died on a stay-alive quest, ...) or timer (a timed quest ran out)"`
}

// Learned is a spell you learned from a trainer (learned; the trainer.learn result).
type Learned struct {
	Spell uint32 `json:"spell"`
	Name  string `json:"name"`
}

// AchievementEarned is an achievement you earned (achievement).
type AchievementEarned struct {
	ID uint32 `json:"id"`
}

// GearBroken is equipped items left at 0 durability when you revived (gear_broken).
type GearBroken struct {
	Items   []Item `json:"items"`
	Message string `json:"message"`
}

// Chat is a message to you, near you or in your channels (chat).
type Chat struct {
	Kind    string `json:"kind" doc:"say, yell, party, raid, raid_warning, battleground, guild, officer, whisper, channel, system, emote, npc_say, npc_yell, npc_whisper, npc_emote, battleground_system, achievement, loot, money, xp, reputation... (match one with chat:<kind>; a leader's party, raid and battleground chat is that kind)"`
	From    string `json:"from"`
	GUID    GUID   `json:"guid"`
	Channel string `json:"channel,omitempty"`
	Text    string `json:"text"`
	Self    bool   `json:"self,omitempty" doc:"you sent it: the server's echo of your own say, yell, party, raid, guild, channel or emote chat (from is you), or of a whisper you sent (kind whisper_sent, from is who you whispered). Your own chat doesn't wake events.wait"`
}

// TravelRefused is the server turning a portal or an instance down (travel_refused).
type TravelRefused struct {
	Map     uint32 `json:"map,omitempty" doc:"the map you were going to (instance refusals)"`
	Reason  string `json:"reason" doc:"requirement (a portal's level, item, quest or group need: see message), instance_full, too_many_instances, encounter_in_progress, difficulty_unavailable, need_group, map_not_allowed, not_found, error..."`
	Message string `json:"message"`
}

// ReadyCheck is your group leader starting a ready check (ready_check).
type ReadyCheck struct {
	From UnitRef `json:"from" doc:"answer with group.ready(true) or group.ready(false)"`
}

// SummonRequest is a player summoning you (summon_request).
type SummonRequest struct {
	From        UnitRef `json:"from"`
	Zone        string  `json:"zone" doc:"where they'd take you"`
	SecondsLeft float64 `json:"seconds_left" doc:"summon.accept() or summon.decline() before it expires"`
}

// Battleground is a change to a battleground or arena queue (battleground).
type Battleground struct {
	Slot        uint32  `json:"slot" doc:"queue slot (0-2)"`
	Status      string  `json:"status" doc:"none (left the queue), queued, invited (enter before seconds_left runs out), in_progress, leaving"`
	Type        uint32  `json:"type,omitempty" doc:"battleground type id"`
	Map         uint32  `json:"map,omitempty"`
	Arena       uint8   `json:"arena,omitempty" doc:"arenas: players per side"`
	Rated       bool    `json:"rated,omitempty"`
	SecondsLeft float64 `json:"seconds_left,omitempty"`
}

// PartyInvite is an invitation to someone's group (party_invite).
type PartyInvite struct {
	From string `json:"from" doc:"accept with group.accept(), or group.decline()"`
}

// PartyChanged is your group after it changed (party_changed).
type PartyChanged struct {
	InGroup bool     `json:"in_group"`
	Members []string `json:"members"`
}

// Notice is a server message: the message of the day, or a notice such as "Inventory is full" (motd, notice).
type Notice struct {
	Message string `json:"message"`
}

// GoalMet is the benchmark's judge recording the run's goal for this character (goal_met, sent by the run's
// supervisor).
type GoalMet struct {
	Goal    string `json:"goal" doc:"the run's goal, e.g. achievement:628"`
	Message string `json:"message"`
	EndsAt  int64  `json:"ends_at" doc:"unix ms: when your agent and session stop"`
}

// Maintenance is an operator notice about the run (maintenance, sent by the run's supervisor).
type Maintenance struct {
	At      int64  `json:"at" doc:"unix ms: when it takes effect"`
	Message string `json:"message" doc:"what happens then, e.g. the run pauses"`
}

// FailureLoop is a task's top-level call failing the same way many times in a row (failure_loop: at 25, then
// every 250 more).
type FailureLoop struct {
	Task  string `json:"task"`
	Op    string `json:"op" doc:"the call, e.g. act.make_room"`
	Code  string `json:"code" doc:"its error code, e.g. no_disposable_item"`
	Count int    `json:"count" doc:"consecutive identical failures so far"`
}

// ErrorEvent is the server refusing something (error).
type ErrorEvent struct {
	Op      string `json:"op" doc:"items, items.loot, vendor.buy, vendor.sell, trainer.learn, group, quests, chat.whisper, chat.channel, chat.join"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Log is a log(msg [, data]) call from a script (log).
type Log struct {
	Msg  string `json:"msg"`
	Data any    `json:"data,omitempty"`
	Task string `json:"task,omitempty"`
}

// TaskEvent is a script task starting or ending (task_started, task_finished).
type TaskEvent struct {
	Task    int    `json:"task"`
	Name    string `json:"name"`
	State   string `json:"state,omitempty" doc:"task_finished: done, failed or stopped"`
	Seconds int    `json:"seconds,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ScriptError is an error a script raised (script_error).
type ScriptError struct {
	Where string `json:"where"`
	Error string `json:"error"`
}

// Attention is a script's question for the agent, asked with ask (attention).
type Attention struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Default  string   `json:"default,omitempty"`
	Task     string   `json:"task,omitempty"`
}

// Answer is the agent's answer to a script's question (answer).
type Answer struct {
	ID     uint64 `json:"id"`
	Choice string `json:"choice"`
}

// FightSummary is one fight (fight_end), from you or a party member in sight entering combat until all of you are
// out of it (or
// you die). Your numbers are yours; group has everyone's.
type FightSummary struct {
	Outcome       string            `json:"outcome" doc:"won (you or your party killed something), died, or ended (combat ended without a kill: it fled, evaded or you got away)"`
	Seconds       float64           `json:"seconds"`
	Enemies       []UnitRef         `json:"enemies" doc:"everything that hit you or that you hit"`
	Kills         []UnitRef         `json:"kills" doc:"yours and your party's"`
	DamageDealt   uint32            `json:"damage_dealt"`
	DamageTaken   uint32            `json:"damage_taken"`
	HealingDone   uint32            `json:"healing_done"`
	HealingTaken  uint32            `json:"healing_taken"`
	Overhealing   uint32            `json:"overhealing,omitempty" doc:"your heals' part past full health (wasted mana)"`
	DPS           float64           `json:"dps" doc:"damage dealt per second of the fight"`
	HealthStart   float64           `json:"health_start" doc:"fraction 0-1"`
	HealthEnd     float64           `json:"health_end"`
	PowerStart    float64           `json:"power_start" doc:"fraction 0-1 (mana, rage, energy)"`
	PowerEnd      float64           `json:"power_end"`
	XP            uint32            `json:"xp"`
	Abilities     map[string]int    `json:"abilities" doc:"casts that went off, by spell"`
	FailedCasts   map[string]int    `json:"failed_casts,omitempty" doc:"by reason (out_of_range, no_power, not_ready...)"`
	AurasApplied  map[string]int    `json:"auras_applied,omitempty" doc:"buffs and debuffs you put on others, by name"`
	AurasSuffered map[string]int    `json:"auras_suffered,omitempty" doc:"debuffs that landed on you, by name"`
	SwingsRefused int               `json:"swings_refused,omitempty" doc:"melee swings the server refused (not_in_range, bad_facing)"`
	Outcomes      map[string]int    `json:"outcomes,omitempty" doc:"how your attacks landed: hit, crit, miss, dodge, parry, block, resist, evade, immune..."`
	Group         []Meter           `json:"group,omitempty" doc:"in a party: you and each member in sight over this fight (zeros for whoever did nothing), from the combat log: damage dealt, healing done, damage taken (taken), healing received (healed), dps, hps, kills"`
	DamageShare   *float64          `json:"damage_share,omitempty" doc:"in a party: your fraction (0-1) of the group's damage this fight"`
	HealingShare  *float64          `json:"healing_share,omitempty" doc:"in a party: your fraction (0-1) of the group's healing this fight"`
	ID            int               `json:"id,omitempty" doc:"its number in the fight log: wow fight <id>, combat.fight(id)"`
	Positioning   *FightPositioning `json:"positioning,omitempty" doc:"how everyone moved: who joined from where, who drifted out of range, where you died"`
}

// FightPositioning is what movement did in a fight, from the fight log's positions.
type FightPositioning struct {
	MovedYd float64      `json:"moved_yd" doc:"how far you walked during the fight"`
	Joins   []FightJoin  `json:"joins" doc:"each enemy, in the order it joined: the first is the one you pulled, the rest are adds"`
	Apart   []FightApart `json:"apart,omitempty" doc:"party members who spent time more than 40 yd from you (out of heal range of each other)"`
	Died    *FightDeath  `json:"died,omitempty"`
}

// FightJoin is an enemy joining a fight: when, how far from you and from which side.
type FightJoin struct {
	Label    string  `json:"label" doc:"its letter on the fight's map"`
	Name     string  `json:"name"`
	Level    uint32  `json:"level,omitempty"`
	S        float64 `json:"s" doc:"seconds into the fight"`
	Distance float64 `json:"distance" doc:"yards from you when it joined"`
	From     string  `json:"from" doc:"compass direction from you: N, NE, E..."`
	Near     string  `json:"near,omitempty" doc:"an enemy already in the fight within 15 yd of it when it joined (its pack)"`
}

// FightApart is a party member who spent part of a fight more than 40 yd from you.
type FightApart struct {
	Label   string  `json:"label"`
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds" doc:"seconds more than 40 yd from you"`
	MaxYd   float64 `json:"max_yd"`
}

// FightDeath is what was around you when you died in a fight.
type FightDeath struct {
	EnemiesWithin10 int      `json:"enemies_within_10" doc:"living enemies within 10 yd of you when you died"`
	NearestPartyYd  *float64 `json:"nearest_party_yd,omitempty"`
}

// FightHeader is one line of the fight log (combat.fights, wow fights).
type FightHeader struct {
	ID          int      `json:"id"`
	Start       int64    `json:"start" doc:"unix ms"`
	Seconds     float64  `json:"seconds"`
	Outcome     string   `json:"outcome"`
	Enemies     []string `json:"enemies"`
	DamageDealt uint32   `json:"damage_dealt"`
	DamageTaken uint32   `json:"damage_taken"`
	Adds        int      `json:"adds" doc:"enemies that joined after the first"`
}

// Fight is one fight as the fight log keeps it (combat.fight, wow fight): its summary, where everyone was each second,
// and every hit, heal, cast and death in order. The map letters: @ you, A-N enemies, P-Z party members and other
// players.
type Fight struct {
	ID       int          `json:"id"`
	Start    int64        `json:"start" doc:"unix ms"`
	Summary  FightSummary `json:"summary"`
	Actors   []FightActor `json:"actors"`
	Frames   []FightFrame `json:"frames" doc:"once a second, from 3 s before the fight: where each actor was"`
	Timeline []FightEntry `json:"timeline"`
}

// FightActor is a unit in a fight, with its letter on the fight's map.
type FightActor struct {
	Label string `json:"label"`
	GUID  GUID   `json:"guid"`
	Name  string `json:"name"`
	Level uint32 `json:"level,omitempty"`
	Kind  string `json:"kind" doc:"you, enemy, party or player"`
}

// FightFrame is where each actor was at one moment of a fight.
type FightFrame struct {
	S  float64     `json:"s" doc:"seconds into the fight (negative: before it)"`
	At []FightSpot `json:"at"`
}

// FightSpot is one actor's position, health and target in a frame.
type FightSpot struct {
	Label  string  `json:"label"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Z      float64 `json:"z"`
	HP     float64 `json:"hp" doc:"health fraction 0-1"`
	Alive  bool    `json:"alive"`
	Target string  `json:"target,omitempty" doc:"the label of who it was targeting"`
}

// FightEntry is one hit, heal, cast, kill, death or join in a fight's timeline.
type FightEntry struct {
	S       float64 `json:"s"`
	Kind    string  `json:"kind" doc:"damage, heal, cast, kill, death or join"`
	Src     string  `json:"src,omitempty" doc:"label, or a name for someone not on the map"`
	Dst     string  `json:"dst,omitempty"`
	Spell   string  `json:"spell,omitempty"`
	Amount  uint32  `json:"amount,omitempty"`
	Outcome string  `json:"outcome,omitempty"`
}

// AuraChange is an aura on you, or one you cast on another unit, beginning or ending (aura_gained, aura_faded).
type AuraChange struct {
	Unit UnitRef `json:"unit" doc:"who it's on (me = true for you)"`
	Aura Aura    `json:"aura"`
}

// None is the data of events that carry none.
type None struct{}

// EventType describes one event type for the generated references.
type EventType struct {
	Data any    // zero value of the data struct
	When string // when it happens
}

// Stall is no XP for a while, or the XP rate falling below your threshold (stall).
type Stall struct {
	Reason    string  `json:"reason" doc:"no_xp: none for stall_s seconds (time eating, drinking or dead doesn't count); slow: the rate over window_s fell below below"`
	Seconds   int     `json:"seconds,omitempty" doc:"no_xp: seconds without XP, not counting time eating, drinking or dead"`
	XPPerHour float64 `json:"xp_per_hour" doc:"over the last window_s"`
	Below     float64 `json:"below,omitempty"`
	WindowS   float64 `json:"window_s,omitempty"`
}

// SlowTick is a game tick that took over 300 ms (slow_tick).
type SlowTick struct {
	MS     int    `json:"ms" doc:"how long the tick took (the game waits for it: movement, combat and replies stall)"`
	Task   string `json:"task,omitempty" doc:"the task that ran longest in it"`
	TaskMS int    `json:"task_ms,omitempty" doc:"how long that task ran"`
	Count  int    `json:"count" doc:"slow ticks since the last slow_tick event (they're at most 30 s apart; each is a line in session.log)"`
}

// Progress is a periodic digest of your progress (progress, with events.watch{every_s = ...}).
type Progress struct {
	XP        uint32  `json:"xp" doc:"XP gained in the period"`
	XPPerHour float64 `json:"xp_per_hour"`
	Kills     int     `json:"kills" doc:"yours and your party's"`
	Deaths    int     `json:"deaths"`
	Seconds   int     `json:"seconds" doc:"the period"`
}

// EventTypes is every event the runtime emits, by name.
var EventTypes = map[string]EventType{
	"damage":               {Damage{}, "a hit involving you (melee, spell, DoT tick or damage shield), or an attack that missed"},
	"heal":                 {Damage{}, "a heal involving you"},
	"energize":             {Energize{}, "power (mana, rage, energy...) given to you, or by you"},
	"dispel":               {Dispel{}, "auras dispelled or stolen from you, or by you"},
	"kill":                 {Kill{}, "you killed something"},
	"party_kill":           {Kill{}, "a party member killed something"},
	"death":                {Death{}, "you died"},
	"fight_end":            {FightSummary{}, "a fight ended (you or a party member in sight left combat, or you died): its summary, with the group's numbers when you're in a party"},
	"resurrected":          {None{}, "you're alive again"},
	"resurrect_offered":    {ResurrectOffer{}, "a player is resurrecting you: death.accept_res() (works dead or as a ghost)"},
	"mirror_timer":         {MirrorTimer{}, "your breath, fatigue or fire timer started, paused or stopped: surface or turn back before it runs out (me.timers)"},
	"combat_start":         {None{}, "you entered combat"},
	"combat_end":           {None{}, "you left combat"},
	"aura_gained":          {AuraChange{}, "a buff or debuff landed on you, or one you cast landed on another unit"},
	"aura_faded":           {AuraChange{}, "a buff or debuff on you, or one you cast on another unit, ended (expired, dispelled, cancelled)"},
	"xp":                   {XPGain{}, "you gained XP"},
	"level_up":             {LevelUp{}, "you reached a new level"},
	"cast_start":           {Cast{}, "a cast with a cast time began"},
	"cast_done":            {Cast{}, "a spell went off"},
	"cast_failed":          {Cast{}, "a cast failed or was interrupted"},
	"pet_cast_done":        {Cast{}, "your pet's ability went off"},
	"pet_cast_failed":      {Cast{}, "your pet couldn't use an ability (code: the reason)"},
	"unit_cast_start":      {UnitCast{}, "an enemy started casting at you or a party member (an interrupt or a stun stops it)"},
	"attack_start":         {AttackStart{}, "your auto-attack started"},
	"attack_failed":        {AttackFailed{}, "the server refused a swing"},
	"item_received":        {ItemReceived{}, "an item went into your bags"},
	"money":                {Money{}, "you looted money"},
	"quest_progress":       {QuestProgress{}, "a quest kill/object counter went up"},
	"quest_complete":       {QuestDone{}, "a quest's objectives are done: turn it in"},
	"quest_turned_in":      {QuestDone{}, "you turned a quest in"},
	"quest_failed":         {QuestDone{}, "a quest in your log failed: abandon it (quests.abandon) and take it again"},
	"learned":              {Learned{}, "you learned a spell from a trainer"},
	"achievement":          {AchievementEarned{}, "you earned an achievement"},
	"area_changed":         {Location{}, "you entered another zone or area"},
	"teleported":           {Teleport{}, "you were teleported: a portal, hearthstone, summon, the graveyard, an instance"},
	"flight_start":         {Flight{}, "you took off: the server flies you, and move.to is refused until you land"},
	"flight_end":           {Flight{}, "you landed"},
	"flight_learned":       {FlightLearned{}, "you discovered a flight point"},
	"home_changed":         {Home{}, "your hearthstone now takes you here (innkeeper.bind)"},
	"movement_changed":     {MovementChanged{}, "who moves you changed: a flight, a teleport, fear, a stun or a root began or ended"},
	"gear_broken":          {GearBroken{}, "you revived with equipped items at 0 durability"},
	"chat":                 {Chat{}, "a message to you, near you or in your channels"},
	"party_invite":         {PartyInvite{}, "someone invited you to their group"},
	"ready_check":          {ReadyCheck{}, "your group leader started a ready check: group.ready(true|false)"},
	"summon_request":       {SummonRequest{}, "a player is summoning you: summon.accept() or summon.decline()"},
	"travel_refused":       {TravelRefused{}, "the server refused a portal or an instance (level, group, full, lockout...)"},
	"battleground":         {Battleground{}, "a battleground queue changed: queued, invited, entered, left"},
	"motd":                 {Notice{}, "the server's message of the day (at login)"},
	"party_changed":        {PartyChanged{}, "your group changed"},
	"notice":               {Notice{}, "a server notice (\"Inventory is full\")"},
	"trade":                {Trade{}, "the trade window changed: someone asked (requested: trade.open), opened, accepted, completed or cancelled"},
	"loot_roll_start":      {LootRoll{}, "group loot: an item is up for rolls; items.roll(roll, choice) before the countdown ends, or you pass"},
	"loot_roll":            {LootRollResult{}, "a group member (or you) chose need, greed, disenchant or pass, or rolled a number"},
	"loot_roll_won":        {LootRollResult{}, "a group roll ended: player won the item"},
	"loot_roll_all_passed": {LootRollResult{}, "a group roll ended with everyone passing"},
	"error":                {ErrorEvent{}, "the server refused something"},
	"result":               {ActionResult{}, "an action finished (its result)"},
	"log":                  {Log{}, "log(msg [, data]) from a script"},
	"task_started":         {TaskEvent{}, "a task started"},
	"task_finished":        {TaskEvent{}, "a task ended (done, failed or stopped)"},
	"script_error":         {ScriptError{}, "a script raised an error"},
	"attention":            {Attention{}, "a script asks the agent (ask)"},
	"stall":                {Stall{}, "no XP for a while, or the XP rate fell below your threshold (events.watch); once until progress resumes"},
	"failure_loop":         {FailureLoop{}, "a task's top-level call failed with the same code 25 times in a row, and again every 250 more; from 25, act.* helpers wait min(5, n/25) s before returning such a failure"},
	"slow_tick":            {SlowTick{}, "a tick took over 300 ms: a script's work held the game up (wow tasks shows each task's busy_ms)"},
	"progress":             {Progress{}, "a periodic digest of your progress (events.watch{every_s = ...}; off by default)"},
	"answer":               {Answer{}, "the agent answered (answer)"},
	"goal_met":             {GoalMet{}, "the judge recorded the run's goal for you: your score is final"},
	"maintenance":          {Maintenance{}, "an operator notice: what happens to the run (a pause, a server restart) and when"},
}

// Action results: the data of an ok result, per op (ops not listed have none).

// MoveDone is the result of move.to and move.stop.
type MoveDone struct {
	Distance float64 `json:"distance" doc:"yards short of the point"`
	Stopped  bool    `json:"stopped,omitempty" doc:"move.stop() ended it"`
	NoPath   bool    `json:"no_path,omitempty" doc:"no navmesh path: it walked a straight line"`
}

// CastDone is the result of a cast that went off (spells.cast, spells.cast_at, items.use).
type CastDone struct {
	Spell uint32 `json:"spell"`
}

// LootRoll is a group loot roll waiting for your choice (loot_roll_start, me.status).
type LootRoll struct {
	Roll        GUID     `json:"roll" doc:"pass it to items.roll"`
	Item        Item     `json:"item"`
	Choices     []string `json:"choices" doc:"what you may choose: need, greed, disenchant, pass"`
	SecondsLeft float64  `json:"seconds_left"`
}

// LootRollResult is a choice or roll in a group loot roll, or its end (loot_roll, loot_roll_won,
// loot_roll_all_passed; the items.roll result).
type LootRollResult struct {
	Roll   GUID    `json:"roll"`
	Item   Item    `json:"item"`
	Player UnitRef `json:"player,omitempty"`
	Choice string  `json:"choice,omitempty" doc:"need, greed, disenchant or pass"`
	Number uint8   `json:"number,omitempty" doc:"the 1-100 roll, once everyone has chosen"`
	Auto   bool    `json:"auto,omitempty" doc:"passed automatically: they can't loot it"`
}

// Looted is what you took from a corpse, chest or container (items.loot, items.open).
type Looted struct {
	Items []Item `json:"items"`
	Money uint32 `json:"money" doc:"copper"`
}

// Sold is what selling to a vendor got you (vendor.sell, vendor.sell_junk).
type Sold struct {
	Money  uint32 `json:"money" doc:"copper gained"`
	Count  int    `json:"count,omitempty" doc:"vendor.sell_junk: stacks sold"`
	Failed int    `json:"failed,omitempty" doc:"vendor.sell_junk: stacks the vendor refused"`
}

// Repaired is the result of vendor.repair.
type Repaired struct {
	Fixed        int   `json:"fixed"`
	Cost         int64 `json:"cost" doc:"copper"`
	StillDamaged int   `json:"still_damaged"`
}

// TalentLearned is the result of talents.learn: the talent's rank now.
type TalentLearned struct {
	Rank int `json:"rank"`
}

// Attacking is the result of combat.attack.
type Attacking struct {
	Already bool `json:"already,omitempty" doc:"you were already attacking it"`
}

// Equipped is the result of items.equip: the slot the item went into.
type Equipped struct {
	Slot string `json:"slot"`
}

// OpResults maps each op to the data of its ok result (ops not listed have none).
var OpResults = map[string]any{
	"death.corpse":  Corpse{},
	"items.compare": Comparison{}, "world.path": PathCheck{}, "world.los_spot": LOSSpot{}, "world.route": RouteCheck{}, "world.danger": DangerCheck{}, "trade.offer": Trade{}, "trade.cancel": Trade{}, "trade.start": Trade{}, "trade.open": Trade{}, "trade.accept": Trade{}, "trainer.list": Trainer{}, "items.info": Item{}, "quests.info": QuestInfo{}, "me.achievements": Achievements{}, "vendor.items": []VendorItem{},
	"gossip.open": GossipWindow{}, "gossip.select": GossipWindow{},
	"move.to": MoveDone{}, "move.stop": MoveDone{}, "spells.cast": CastDone{}, "spells.cast_at": CastDone{},
	"items.use": CastDone{}, "objects.use": ObjectActivation{}, "transports.board": Boarding{}, "transports.leave": Position{}, "items.loot": Looted{}, "items.open": Looted{}, "items.split": Item{}, "items.equip": Equipped{}, "items.roll": LootRollResult{},
	"vendor.buy": ItemReceived{}, "vendor.sell": Sold{}, "vendor.sell_junk": Sold{}, "vendor.repair": Repaired{},
	"trainer.learn": Learned{}, "quests.turn_in": QuestDone{}, "quests.accept": QuestDone{}, "talents.learn": TalentLearned{},
	"pet.cast": Cast{}, "pet.cast_at": Cast{}, "pet.feed": Cast{},
	"combat.attack": Attacking{}, "move.unstuck": Teleport{}, "summon.accept": Teleport{}, "flights.map": FlightMap{}, "flights.take": Flight{}, "innkeeper.bind": Home{},
}
