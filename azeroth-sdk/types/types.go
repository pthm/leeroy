// Package types is the data model of the agent API: every value the Lua API returns, every action result and
// every event's data is one of these structs, with the same field names everywhere. Lua sees them as tables
// (through their json names) and the RPC API as JSON; cmd/apigen writes docs/api-reference.md from this file, so
// the two stay in sync.
//
// Field docs live in `doc` tags: they're what the generated references show.
package types

import (
	"encoding/json"
	"strconv"
)

// GUID identifies a unit, object or item. It's a string in Lua and JSON: 64-bit GUIDs exceed a double's
// precision. Zero is null ("none").
type GUID uint64

func (g GUID) MarshalJSON() ([]byte, error) {
	if g == 0 {
		return []byte("null"), nil
	}
	return []byte(`"` + strconv.FormatUint(uint64(g), 10) + `"`), nil
}

func (g *GUID) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		var n uint64
		if err := json.Unmarshal(b, &n); err != nil {
			return nil // null
		}
		*g = GUID(n)
		return nil
	}
	n, err := strconv.ParseUint(s, 10, 64)
	*g = GUID(n)
	return err
}

// ---------- the world ----------

type Position struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Z   float64 `json:"z"`
	O   float64 `json:"o,omitempty" doc:"facing, radians"`
	Map uint32  `json:"map" doc:"0 Eastern Kingdoms, 1 Kalimdor, 530 Outland, 571 Northrend"`
}

type Location struct {
	Zone   string `json:"zone" doc:"e.g. Elwynn Forest"`
	Area   string `json:"area" doc:"e.g. Goldshire"`
	ZoneID uint32 `json:"zone_id"`
	AreaID uint32 `json:"area_id"`
}

// Place is where something is in the world, measured from you: anything around you in me.status, units.* and
// objects.* has one.
type Place struct {
	X            float64  `json:"x"`
	Y            float64  `json:"y"`
	Z            float64  `json:"z"`
	Distance     float64  `json:"distance" doc:"yards from you in a straight line"`
	InSight      *bool    `json:"in_sight,omitempty" doc:"nothing solid (rock, walls) between you and it: a mob in a cave under you can be close but not in sight"`
	PathDistance *float64 `json:"path_distance,omitempty" doc:"how far you'd walk to reach it (often much more than distance, across cliffs, floors and caves); worked out nearest first within a time budget, so the farthest may lack it"`
	Reachable    *bool    `json:"reachable,omitempty" doc:"with path_distance: whether a walking route gets you there"`
	Bearing      float64  `json:"bearing" doc:"degrees from where you face: 0 ahead, 90 to your right, -90 to your left, ±180 behind"`
	Clock        int      `json:"clock" doc:"the same as a clock face: 12 ahead, 3 right, 6 behind, 9 left"`
	DZ           float64  `json:"dz" doc:"yards above you (negative: below; a mob in a cave under you, or on a ledge above)"`
}

// Unit is what creatures and players share.
type Unit struct {
	GUID      GUID   `json:"guid"`
	Name      string `json:"name" doc:"empty for a moment after a creature first comes into view"`
	Level     uint32 `json:"level"`
	Health    uint32 `json:"health"`
	MaxHealth uint32 `json:"max_health"`
	Place
	Alive    bool     `json:"alive"`
	Reaction string   `json:"reaction" doc:"toward you: hostile (attacks on sight), neutral (fights back) or friendly"`
	Target   GUID     `json:"target" doc:"who it's targeting; null for nobody"`
	InCombat bool     `json:"in_combat"`
	Casting  *Casting `json:"casting,omitempty" doc:"the spell it's casting or channelling now (interrupt it, or get out of the way)"`
	States   []string `json:"states,omitempty" doc:"the game's aura states: health_below_20 (Execute, Hammer of Wrath), health_below_35, defense (it dodged, parried or blocked: Revenge), frozen, bleeding, enrage, ..."`
}

type Creature struct {
	Unit
	Entry         uint32   `json:"entry" doc:"creature template id (azworld npc <entry>)"`
	Elite         bool     `json:"elite" doc:"much stronger than its level suggests: meant for groups"`
	Rank          string   `json:"rank,omitempty" doc:"normal, elite, rare, rare_elite or boss"`
	Type          string   `json:"type,omitempty" doc:"beast, humanoid, undead, demon, elemental, giant, dragonkin, mechanical, critter, totem, ... (spells like Polymorph only work on some types). Known a moment after first sight"`
	Subname       string   `json:"subname,omitempty" doc:"the title under an NPC's name, e.g. Weapon Merchant"`
	KillCredit    []uint32 `json:"kill_credit,omitempty" doc:"other creature entries killing this one counts as, for kill objectives (an objective's entry may be one of these)"`
	Skinnable     bool     `json:"skinnable,omitempty" doc:"a corpse that can be skinned"`
	Evading       bool     `json:"evading,omitempty" doc:"it's evading (resetting, unreachable): your attacks do nothing; leave it"`
	Roles         []string `json:"roles,omitempty" doc:"what it offers: vendor, repair, trainer, quest_giver, flight_master, innkeeper, banker, spirit_healer, gossip"`
	Attackable    bool     `json:"attackable" doc:"can be attacked at all (false for triggers, dummies, protected NPCs)"`
	AggroRadius   float64  `json:"aggro_radius,omitempty" doc:"hostile and alive: how close you can come before it attacks you on sight (the server's rule: 20 yd at your level, a yard more per level above you and a yard less per level below, at least 5)"`
	InAggro       bool     `json:"in_aggro,omitempty" doc:"you're within its aggro_radius and in its sight: it attacks you now, or the moment it's free"`
	Lootable      bool     `json:"lootable" doc:"a corpse with loot for you"`
	TappedByOther bool     `json:"tapped_by_other" doc:"someone else damaged it first: killing it gives you nothing"`
	QuestStatus   string   `json:"quest_status,omitempty" doc:"the marker over a quest giver: available (yellow !: a quest for you), available_repeatable (blue !), available_low_level (only quests far below your level), unavailable (none for you yet: level or prerequisites), incomplete (grey ?: you're on its quest), reward (yellow ?: turn a quest in here)"`
	ThreatPct     *float64 `json:"threat_pct,omitempty" doc:"your threat on it as a share of the top (100 = it attacks you; another unit pulls it off you past 110 in melee, 130 at range); only while it has you on its threat table"`
}

type Player struct {
	Unit
	Class string `json:"class" doc:"warrior, paladin, hunter, rogue, priest, death_knight, shaman, mage, warlock, druid"`
	Race  string `json:"race" doc:"human, dwarf, night_elf, gnome, draenei, orc, undead, tauren, troll, blood_elf"`
}

// UnitRef names a unit in events and results.
type UnitRef struct {
	GUID   GUID   `json:"guid"`
	Name   string `json:"name"`
	Entry  uint32 `json:"entry,omitempty" doc:"creatures only"`
	Level  uint32 `json:"level,omitempty"`
	Player bool   `json:"player,omitempty"`
	Me     bool   `json:"me,omitempty" doc:"it's you"`
}

type Object struct {
	GUID  GUID   `json:"guid"`
	Entry uint32 `json:"entry" doc:"game object template id"`
	Name  string `json:"name" doc:"empty for a moment after it first comes into view"`
	Type  string `json:"type" doc:"chest, door, button, goober (quest objects), questgiver, portal (use it: you're teleported), ship (boats and zeppelins), elevator, mailbox, fishing_pool (fish here), fishing_bobber, ... Mining veins and herbs are chests: see gather"`
	Place
	Gather string `json:"gather,omitempty" doc:"the skill it takes: mining, herbalism, lockpicking, fishing or inscription"`
	Skill  uint32 `json:"skill,omitempty" doc:"the skill level it needs"`
	CanUse *bool  `json:"can_use,omitempty" doc:"with gather: your skill is high enough (false without the skill: learn it from a trainer)"`
	Key    uint32 `json:"key,omitempty" doc:"item entry of a key that opens it"`
	Quest  uint32 `json:"quest,omitempty" doc:"the quest it's for"`
	Lit    bool   `json:"lit,omitempty" doc:"the server marks it usable for you (it sparkles): a quest you have needs it (GM characters see every chest lit)"`
}

type Path struct {
	Distance float64 `json:"distance" doc:"walking distance"`
	Gap      float64 `json:"gap" doc:"how far the best path ends from the target: over a few yards means you can't get there"`
}

// ---------- your character ----------

type Resource struct {
	Current uint32 `json:"current"`
	Max     uint32 `json:"max"`
	Type    string `json:"type,omitempty" doc:"power only: mana, rage, energy, focus, runic_power (rage and runic power are x10: 0-1000)"`
}

type XP struct {
	Current uint32 `json:"current" doc:"into this level"`
	Next    uint32 `json:"next" doc:"needed for the next level"`
}

type Casting struct {
	Spell  uint32 `json:"spell"`
	Name   string `json:"name"`
	MSLeft uint32 `json:"ms_left"`
}

type Stats struct {
	Strength          uint32     `json:"strength"`
	Agility           uint32     `json:"agility"`
	Stamina           uint32     `json:"stamina"`
	Intellect         uint32     `json:"intellect"`
	Spirit            uint32     `json:"spirit"`
	Armor             uint32     `json:"armor"`
	AttackPower       uint32     `json:"attack_power"`
	RangedAttackPower uint32     `json:"ranged_attack_power"`
	WeaponDamage      [2]float64 `json:"weapon_damage" doc:"main hand min, max"`
	WeaponSpeed       float64    `json:"weapon_speed" doc:"seconds per swing"`
	MeleeDPS          float64    `json:"melee_dps" doc:"white damage per second, attack power included"`
	CritPct           float64    `json:"crit_pct"`
	DodgePct          float64    `json:"dodge_pct"`
	ParryPct          float64    `json:"parry_pct"`
	SpellCritPct      float64    `json:"spell_crit_pct"`
	SpellPower        uint32     `json:"spell_power"`
	BonusHealing      uint32     `json:"bonus_healing"`
}

type Corpse struct {
	Place
	Map       uint32  `json:"map"`
	ReclaimIn float64 `json:"reclaim_in" doc:"seconds until you can reclaim it"`
}

type Achievements struct {
	Earned   []uint32    `json:"earned"`
	Criteria [][2]uint64 `json:"criteria" doc:"{criteria id, counter}"`
}

// ---------- items ----------

// Item is an item template and, for one you carry, where it is and its state. The template fields fill in once
// the server has answered the item query (Name is empty until then: ask again a moment later).
type Item struct {
	Entry         uint32           `json:"entry" doc:"item template id (azworld item <entry>)"`
	Name          string           `json:"name"`
	Quality       string           `json:"quality" doc:"poor (grey: junk to sell), common, uncommon, rare, epic, legendary"`
	Class         string           `json:"class" doc:"weapon, armor, consumable, container, quest, trade_goods, reagent, projectile, key, misc, ..."`
	Subclass      uint32           `json:"subclass" doc:"armor: 1 cloth 2 leather 3 mail 4 plate 6 shield; weapon: 0 axe 4 mace 7 sword 15 dagger..."`
	Slot          string           `json:"slot,omitempty" doc:"where it's worn: head, chest, main_hand, one_hand, two_hand, finger, trinket, ...; empty if not equippable"`
	ItemLevel     uint32           `json:"item_level"`
	RequiredLevel uint32           `json:"required_level"`
	Armor         uint32           `json:"armor,omitempty"`
	DPS           float64          `json:"dps,omitempty" doc:"weapon damage per second"`
	Stats         map[string]int32 `json:"stats,omitempty" doc:"e.g. {strength = 3, stamina = 2}"`
	UseSpell      uint32           `json:"use_spell,omitempty" doc:"what items.use casts (food, drink, potions, quest items)"`
	StartsQuest   uint32           `json:"starts_quest,omitempty"`
	MaxStack      int32            `json:"max_stack"`
	BuyPrice      uint32           `json:"buy_price"`
	SellPrice     uint32           `json:"sell_price" doc:"copper per item; 0 = vendors won't buy it"`
	Usable        bool             `json:"usable" doc:"you can equip and use it"`
	WhyNot        string           `json:"why_not,omitempty" doc:"why you can't"`

	GUID          GUID   `json:"guid,omitempty" doc:"an item you carry"`
	Count         uint32 `json:"count,omitempty"`
	Bag           uint8  `json:"bag,omitempty" doc:"255 = your own slots (equipped 0-18, bags 19-22, backpack 23-38); 19-22 = inside that bag"`
	BagSlot       uint8  `json:"bag_slot"`
	Equipped      bool   `json:"equipped,omitempty"`
	Durability    uint32 `json:"durability,omitempty"`
	MaxDurability uint32 `json:"max_durability,omitempty"`
	Broken        bool   `json:"broken,omitempty" doc:"durability 0: it does nothing until repaired"`
	Soulbound     bool   `json:"soulbound,omitempty" doc:"can't be traded"`
}

// Comparison is what changes if you swap an item for what you wear in its slot.
type Comparison struct {
	Item      Item             `json:"item"`
	Replaces  *Item            `json:"replaces" doc:"what you'd take off (the weaker of two rings, trinkets or one-handers); null for an empty slot"`
	ItemLevel int32            `json:"item_level" doc:"difference"`
	Armor     int32            `json:"armor"`
	DPS       float64          `json:"dps"`
	Stats     map[string]int32 `json:"stats" doc:"difference per stat"`
}

type VendorItem struct {
	Item
	VendorSlot uint32 `json:"vendor_slot"`
	Price      uint32 `json:"price" doc:"copper per buy_count"`
	BuyCount   uint32 `json:"buy_count" doc:"how many one purchase gives"`
	Stock      int32  `json:"stock" doc:"-1 = unlimited"`
}

type Threat struct {
	Unit   UnitRef `json:"unit"`
	Threat float64 `json:"threat"`
	Pct    float64 `json:"pct" doc:"share of the top threat"`
}

// ---------- NPC dialogue ----------

// GossipWindow identifies the window the server actually opened. Value is present for gossip and quest
// windows; use vendor.items, trainer.list or flights.map for the other window contents. A closed result from
// gossip.select confirms only that the dialogue closed, not that a bind, teleport or other effect succeeded.
type GossipWindow struct {
	Kind  string  `json:"kind" doc:"gossip, quest, vendor, trainer, flight, or closed (select only)"`
	NPC   GUID    `json:"npc_guid"`
	Value *Gossip `json:"value,omitempty"`
}

type Gossip struct {
	NPC     GUID           `json:"npc"`
	Menu    uint32         `json:"menu"`
	Text    string         `json:"text" doc:"what the NPC says (or, opening a quest giver with one quest for you, the quest's story)"`
	TextID  uint32         `json:"text_id,omitempty"`
	Options []GossipOption `json:"options"`
	Quests  []GossipQuest  `json:"quests" doc:"quests it offers or takes (quests.accept / quests.turn_in)"`
}

type GossipOption struct {
	Index   uint32 `json:"index" doc:"pass to gossip.select"`
	Icon    string `json:"icon" doc:"chat, vendor, taxi, trainer, interact, banker, talk, tabard, battle, dot: what choosing it does"`
	Text    string `json:"text"`
	Coded   bool   `json:"coded,omitempty" doc:"asks for text: gossip.select(index, code)"`
	Money   uint32 `json:"money,omitempty" doc:"copper it costs"`
	Confirm string `json:"confirm,omitempty" doc:"the confirmation a real client would show"`
}

type GossipQuest struct {
	Quest uint32 `json:"quest"`
	Title string `json:"title"`
	Level int32  `json:"level"`
}

type Trainer struct {
	NPC      GUID           `json:"npc"`
	Greeting string         `json:"greeting,omitempty"`
	Spells   []TrainerSpell `json:"spells"`
}

type TrainerSpell struct {
	Spell     uint32   `json:"spell" doc:"pass to trainer.learn"`
	Name      string   `json:"name"`
	State     string   `json:"state" doc:"available (learn it now), unavailable (level, skill or a prerequisite missing), known"`
	Cost      uint32   `json:"cost" doc:"copper, after reputation discount"`
	Level     uint32   `json:"level" doc:"required level"`
	SkillLine uint32   `json:"skill_line,omitempty" doc:"professions: the skill it needs"`
	SkillRank uint32   `json:"skill_rank,omitempty"`
	Requires  []uint32 `json:"requires,omitempty" doc:"spells you must know first"`
}

// ---------- spells, quests, talents ----------

type Spell struct {
	ID                 uint32  `json:"id"`
	Name               string  `json:"name"`
	Rank               string  `json:"rank"`
	Level              uint32  `json:"level" doc:"the level you can learn it at (a trainer teaches it from then)"`
	CastMS             uint32  `json:"cast_ms" doc:"0 = instant"`
	CooldownMS         uint32  `json:"cooldown_ms"`
	CategoryCooldownMS uint32  `json:"category_cooldown_ms"`
	GCDMS              uint32  `json:"gcd_ms"`
	Range              float32 `json:"range" doc:"yards against hostile targets (melee ~5)"`
	Cost               uint32  `json:"cost" doc:"what a cast costs you now, in power_type (most mana costs are a share of your base mana)"`
	PowerType          string  `json:"power_type"`
	Known              bool    `json:"known"`
}

type Objective struct {
	Text  string `json:"text" doc:"what the quest log says"`
	Kind  string `json:"kind" doc:"kill, object (use a game object), item (collect), explore (go somewhere or do something: done when the quest completes)"`
	Entry uint32 `json:"entry" doc:"creature, object or item id"`
	Have  uint32 `json:"have"`
	Need  uint32 `json:"need"`
	Done  bool   `json:"done"`
}

type Quest struct {
	ID         uint32      `json:"id"`
	Title      string      `json:"title" doc:"empty until the server answers the quest query: ask again a moment later"`
	Summary    string      `json:"summary" doc:"what the quest log says to do"`
	Level      int32       `json:"level"`
	Complete   bool        `json:"complete" doc:"objectives done: turn it in"`
	Failed     bool        `json:"failed"`
	Objectives []Objective `json:"objectives"`
	NextQuest  uint32      `json:"next_quest,omitempty" doc:"the next in its chain"`
}

// QuestInfo: a quest's text and rewards, as the quest giver and the quest log show them.
type QuestInfo struct {
	ID         uint32   `json:"id"`
	Title      string   `json:"title"`
	Level      int32    `json:"level"`
	MinLevel   uint32   `json:"min_level"`
	Summary    string   `json:"summary" doc:"what to do (the quest log's objectives text)"`
	Details    string   `json:"details" doc:"the story the quest giver tells: often names where to go and who to see"`
	Completed  string   `json:"completed,omitempty" doc:"shown once the objectives are done: usually who to return to"`
	Objectives []string `json:"objectives,omitempty" doc:"objective lines that aren't simple kill/collect counts (events, places to reach)"`
	Money      uint32   `json:"money,omitempty" doc:"copper reward"`
	Spell      uint32   `json:"spell,omitempty" doc:"a spell it teaches or casts on you"`
	Rewards    []Item   `json:"rewards,omitempty" doc:"items you get, all of them"`
	Choices    []Item   `json:"choices,omitempty" doc:"items to choose one of (quests.turn_in reward = its index, 0 = first)"`
	NextQuest  uint32   `json:"next_quest,omitempty"`
}

type Talent struct {
	ID        uint32 `json:"id"`
	Name      string `json:"name"`
	Rank      int    `json:"rank"`
	MaxRank   int    `json:"max_rank"`
	Row       uint32 `json:"row" doc:"row r needs 5 × r points in the tree"`
	Col       uint32 `json:"col"`
	Spell     uint32 `json:"spell" doc:"the spell of your current (or first) rank: spells.info / azworld spell"`
	Requires  string `json:"requires,omitempty"`
	Available bool   `json:"available" doc:"you can learn the next rank now"`
}

type TalentTree struct {
	Name    string   `json:"name"`
	Spent   int      `json:"spent"`
	Talents []Talent `json:"talents"`
}

type Talents struct {
	Points uint32       `json:"points" doc:"unspent"`
	Trees  []TalentTree `json:"trees"`
}

// ---------- others ----------

type Group struct {
	Leader     string        `json:"leader"`
	MeLeader   bool          `json:"me_leader" doc:"you lead the group"`
	Members    []GroupMember `json:"members" doc:"the others, not you"`
	Spread     *float64      `json:"spread,omitempty" doc:"yards between the two of you farthest apart (you included), across the ground: how clustered the party is. Counts whoever's position is known and in your zone"`
	SpreadPair []string      `json:"spread_pair,omitempty" doc:"who those two are"`
}

// GroupMember is a party member as the real client's party frames show them: health, power, level and where they
// are come from the server's party updates even when they're out of sight.
type GroupMember struct {
	Name         string    `json:"name"`
	GUID         GUID      `json:"guid"`
	Leader       bool      `json:"leader"`
	Online       bool      `json:"online"`
	Class        string    `json:"class,omitempty"`
	Level        uint32    `json:"level,omitempty"`
	Health       Resource  `json:"health"`
	Power        Resource  `json:"power"`
	Alive        bool      `json:"alive"`
	Ghost        bool      `json:"ghost,omitempty"`
	InCombat     bool      `json:"in_combat,omitempty" doc:"only known while they're in sight"`
	Zone         string    `json:"zone,omitempty"`
	Position     *Position `json:"position,omitempty" doc:"x, y (and z while in sight)"`
	Distance     *float64  `json:"distance,omitempty" doc:"yards from you"`
	InRange      bool      `json:"in_range" doc:"close enough to see (about 100 yd): units.get works on them"`
	InSight      *bool     `json:"in_sight,omitempty" doc:"in range and nothing solid between you"`
	PathDistance *float64  `json:"path_distance,omitempty" doc:"how far you'd walk to reach them (in range only)"`
	Reachable    *bool     `json:"reachable,omitempty"`
	Target       GUID      `json:"target,omitempty" doc:"what they're targeting (in sight only)"`
	Auras        []Aura    `json:"auras,omitempty" doc:"their buffs and debuffs (spell, name, harmful; details while in sight)"`
}

// GroupReport is how your party has spent the last minutes, from what you've seen of them (group.report).
type GroupReport struct {
	Minutes float64             `json:"minutes"`
	Members []GroupMemberReport `json:"members" doc:"you first"`
}

// GroupMemberReport is one member's time over the report's window, in percent of the seconds sampled.
type GroupMemberReport struct {
	Name          string  `json:"name"`
	You           bool    `json:"you,omitempty"`
	Seconds       int     `json:"seconds" doc:"seconds sampled (while you were grouped with them)"`
	FightingPct   float64 `json:"fighting_pct" doc:"in combat (seen only while they're in sight: out of sight it counts as travelling or idle)"`
	TravellingPct float64 `json:"travelling_pct"`
	IdlePct       float64 `json:"idle_pct" doc:"alive, not fighting, not moving: usually waiting"`
	DeadPct       float64 `json:"dead_pct"`
	WithGroupPct  float64 `json:"with_group_pct" doc:"within 50 yd of another member"`
	InSightPct    float64 `json:"in_sight_pct" doc:"how much of it you saw them; out of sight it's from party updates: their position, not combat, and a death may be missed"`
	Kills         int     `json:"kills" doc:"killing blows"`
	Deaths        int     `json:"deaths"`
}

type TradeSide struct {
	Money uint32 `json:"money"`
	Items []Item `json:"items"`
}

type Trade struct {
	State        string    `json:"state" doc:"none, waiting (you asked), requested (they asked: trade.open), open, complete, or why it ended: cancelled, rejected, busy, too_far, no_target, wrong_faction, ignored, dead, target_dead, stunned, closed, ..."`
	With         string    `json:"with,omitempty"`
	Mine         TradeSide `json:"mine"`
	Theirs       TradeSide `json:"theirs"`
	TheyAccepted bool      `json:"they_accepted"`
	Error        string    `json:"error,omitempty" doc:"why it ended, or why the last step (an accept, an item) was refused"`
}

type Inspect struct {
	Player
	Gear         []Item  `json:"gear"`
	AvgItemLevel float64 `json:"avg_item_level"`
}

type CombatEntry struct {
	T      int64   `json:"t" doc:"unix ms"`
	Kind   string  `json:"kind" doc:"melee, spell, dot, heal, hot, kill, environment (falls, drowning, lava: no src)"`
	Src    UnitRef `json:"src"`
	Dst    UnitRef `json:"dst"`
	Spell  uint32  `json:"spell,omitempty"`
	Amount uint32  `json:"amount,omitempty"`
	Over   uint32  `json:"over,omitempty" doc:"overkill / overheal"`
	Crit   bool    `json:"crit,omitempty"`
}

type SpellMeter struct {
	Spell   uint32 `json:"spell"`
	Name    string `json:"name"`
	Damage  uint32 `json:"damage"`
	Healing uint32 `json:"healing"`
	Hits    uint32 `json:"hits"`
}

type Meter struct {
	Unit    UnitRef      `json:"unit"`
	Damage  uint32       `json:"damage"`
	Healing uint32       `json:"healing"`
	Taken   uint32       `json:"taken"`
	Healed  uint32       `json:"healed"`
	DPS     float64      `json:"dps"`
	HPS     float64      `json:"hps"`
	ActiveS float64      `json:"active_s"`
	Kills   uint32       `json:"kills"`
	CritPct float64      `json:"crit_pct"`
	BySpell []SpellMeter `json:"by_spell,omitempty" doc:"yours only"`
}

// ---------- requests and events ----------

type Error struct {
	Code    string `json:"code" doc:"stable, e.g. out_of_range, not_known, bags_full, stuck, timeout"`
	Message string `json:"message"`
	Next    string `json:"next,omitempty" doc:"one concrete corrective action, when known"`
}

// Request is a raw action handle, including actions that resolve immediately. Await it to inspect the outcome.
type Request struct {
	ID      int    `json:"id"`
	Op      string `json:"op"`
	Pending bool   `json:"pending" doc:"always true on a handle; does not imply the action is still running"`
}

// ActionResult is a resolved raw request or an await wait_timeout. A wait_timeout does not cancel the request.
type ActionResult struct {
	ID    int            `json:"id"`
	Op    string         `json:"op" doc:"the function, e.g. spells.cast"`
	OK    bool           `json:"ok"`
	Data  any            `json:"data,omitempty" doc:"the op's result type (Action results)"`
	Error *Error         `json:"error,omitempty"`
	Args  map[string]any `json:"args,omitempty"`
	MS    int64          `json:"ms,omitempty" doc:"how long it took"`
}

// ActResult is a waiting helper's outcome. It has no raw request ID or pending flag; steps identify nested requests.
type ActResult struct {
	Op    string       `json:"op"`
	OK    bool         `json:"ok"`
	Data  any          `json:"data,omitempty" doc:"helper-specific data, including partial work on failure"`
	Error *Error       `json:"error,omitempty"`
	Steps []ActionStep `json:"steps,omitempty" doc:"bounded record of composite operations in execution order"`
}

// ActionStep records a required or optional operation within a helper.
type ActionStep struct {
	Op        string `json:"op"`
	OK        bool   `json:"ok"`
	RequestID int    `json:"request_id,omitempty"`
	Code      string `json:"code,omitempty"`
	Count     int    `json:"count,omitempty" doc:"number of identical attempts when summarized"`
}

type Event struct {
	Seq  uint64 `json:"seq" doc:"counts up for the whole run"`
	T    int64  `json:"t" doc:"unix ms"`
	Type string `json:"type"`
	Data any    `json:"data" doc:"the type's data (the Events table)"`
}

// ---------- scripts and status ----------

type TaskInfo struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	State      string `json:"state" doc:"new, running, waiting, paused, done, failed, stopped"`
	Status     string `json:"status,omitempty" doc:"what it says it's doing (task.status)"`
	WaitingFor string `json:"waiting_for,omitempty" doc:"e.g. 1.5s, request 12, event kill"`
	Seconds    int    `json:"seconds" doc:"since it started"`
	BusyMS     int    `json:"busy_ms" doc:"time it has spent running Lua (and the API calls it made) since it started: the game waits while it does"`
	Error      string `json:"error,omitempty" doc:"why it failed: file:line: message, then a traceback when the error was deeper than one call"`
}

// Gear grades what you wear against your level, from the game's own numbers: item level, quality, durability.
type Gear struct {
	AvgItemLevel float64    `json:"avg_item_level" doc:"over the 17 slots that count (not shirt or tabard); empty = 0"`
	Score        int        `json:"score" doc:"0-100: how well your gear suits your level. Each slot: its item level against your level, times its quality (poor 0.6, common 0.85, uncommon 1, rare 1.1, epic 1.2), capped at 1; empty or broken = 0. The main-hand weapon weighs most, then chest, legs, head, shoulders"`
	Grade        string     `json:"grade" doc:"poor (under 40), weak (40-64), ok (65-84), good (85+)"`
	Weapon       *Item      `json:"weapon,omitempty" doc:"your main hand"`
	Weakest      []GearSlot `json:"weakest" doc:"the slots holding you back most"`
	Empty        []string   `json:"empty"`
	Broken       []string   `json:"broken"`
}

type GearSlot struct {
	Slot  string `json:"slot"`
	Item  string `json:"item,omitempty"`
	Level uint32 `json:"item_level"`
	Score int    `json:"score" doc:"0-100"`
}

// Spellbook: the class spells you could know at your level (Spell.dbc and SkillLineAbility.dbc, as the real
// client's spellbook knows them) against the ones you do.
type Spellbook struct {
	Known   int            `json:"known"`
	Total   int            `json:"total"`
	Missing []MissingSpell `json:"missing" doc:"learn them at your class trainer (some come from class quests, e.g. stances)"`
}

type MissingSpell struct {
	ID    uint32 `json:"id"`
	Name  string `json:"name"`
	Rank  string `json:"rank,omitempty"`
	Level uint32 `json:"level"`
}

type MobSummary struct {
	Count       int      `json:"count" doc:"hostile creatures within 30 yd, at most 5 levels below you"`
	MaxLevel    uint32   `json:"max_level"`
	Elites      int      `json:"elites"`
	Loot        int      `json:"loot,omitempty" doc:"corpses within 30 yd with loot for you (units.nearby: lootable)"`
	InAggro     int      `json:"in_aggro" doc:"hostile creatures that have you in their aggro range now"`
	AggroMargin *float64 `json:"aggro_margin,omitempty" doc:"yards to the nearest hostile creature's aggro range (negative: you're inside it)"`
}

// Danger: a hostile creature whose aggro range you're in or near: what attacks you if you stay, or step closer.
type Danger struct {
	GUID  GUID   `json:"guid"`
	Name  string `json:"name"`
	Level uint32 `json:"level"`
	Elite bool   `json:"elite,omitempty"`
	Place
	AggroRadius float64 `json:"aggro_radius"`
	InAggro     bool    `json:"in_aggro" doc:"you're inside its aggro range and in its sight"`
	Margin      float64 `json:"margin" doc:"yards to the edge of its aggro range (negative: you're inside)"`
	Pack        int     `json:"pack" doc:"other hostile creatures within 10 yd of it: what comes along if you pull it"`
}

// Status is what a player sees on screen, in one call (me.status, `wow status`): enough to decide what to do next.
// Each part has a detail call for more.
type Status struct {
	Name           string        `json:"name"`
	Class          string        `json:"class"`
	Race           string        `json:"race"`
	Level          uint32        `json:"level"`
	XP             XP            `json:"xp"`
	Health         Resource      `json:"health"`
	Power          Resource      `json:"power"`
	Money          uint32        `json:"money" doc:"copper"`
	Position       Position      `json:"position"`
	Location       Location      `json:"location"`
	Alive          bool          `json:"alive"`
	Ghost          bool          `json:"ghost"`
	InCombat       bool          `json:"in_combat"`
	Resting        bool          `json:"resting"`
	Movement       Movement      `json:"movement" doc:"who's moving you (free, in_flight, feared...)"`
	Casting        *Casting      `json:"casting,omitempty"`
	Auras          []Aura        `json:"auras" doc:"your buffs and debuffs"`
	States         []string      `json:"states,omitempty" doc:"your aura states (defense: you dodged/parried/blocked; health_below_20...)"`
	ComboPoints    *ComboPoints  `json:"combo_points,omitempty" doc:"rogues: combo points on a target; warriors: Overpower is ready on that target"`
	Timers         []MirrorTimer `json:"timers,omitempty" doc:"breath, fatigue or fire running out: surface or turn back"`
	ResurrectOffer *UnitRef      `json:"resurrect_offer,omitempty" doc:"a player is resurrecting you: death.accept_res()"`
	Corpse         *Corpse       `json:"corpse,omitempty"`
	Target         *Unit         `json:"target,omitempty"`
	TargetAuras    []Aura        `json:"target_auras,omitempty" doc:"your target's buffs and debuffs"`
	Threats        []Hostile     `json:"threats" doc:"hostile creatures fighting you, your pet or your group (combat.threat for a full threat table)"`
	Mobs           MobSummary    `json:"mobs_nearby" doc:"units.nearby for each one"`
	Danger         []Danger      `json:"danger,omitempty" doc:"hostile creatures whose aggro range you're in or within 15 yd of, nearest edge first, at most 5; wow look for the map"`
	NPCs           []NearbyNPC   `json:"npcs" doc:"friendly NPCs within 40 yd that offer something: quest givers with a marker first, then nearest; at most 8"`
	Objects        []Object      `json:"objects" doc:"within 40 yd, nearest first, at most 5: chests, gathering nodes (see gather / can_use), fishing pools and objects lit up for your quests"`
	Players        []Player      `json:"players" doc:"within 60 yd, nearest first, at most 10 (units.players for all)"`
	Group          *Group        `json:"group,omitempty"`
	Quests         []Quest       `json:"quests"`
	Gear           GearSummary   `json:"gear" doc:"me.gear() for every slot"`
	Untrained      int           `json:"untrained_spells" doc:"class spells at your level you haven't learned: visit your trainer (trainer.list; me.spellbook() names them)"`
	Talents        uint32        `json:"unspent_talents" doc:"talent points to spend (talents.list, talents.learn)"`
	FreeSlots      int           `json:"free_slots"`
	LootRolls      []LootRoll    `json:"loot_rolls,omitempty" doc:"group loot waiting for your roll: items.roll(roll, choice)"`
	BagUpgrades    []Upgrade     `json:"bag_upgrades" doc:"items you carry that beat what you wear: items.equip(item.guid) (items.compare for the stats)"`
	LastMinute     *Meter        `json:"last_minute,omitempty" doc:"your fighting over the last minute"`
	Tasks          []TaskInfo    `json:"tasks"`
}

// Hostile: a creature in your fight, as a unit frame shows it.
type Hostile struct {
	GUID      GUID    `json:"guid"`
	Name      string  `json:"name"`
	Level     uint32  `json:"level"`
	HealthPct float64 `json:"health_pct"`
	Place
	On        string   `json:"on" doc:"who it's attacking: you, pet, or a group member's name"`
	ThreatPct *float64 `json:"threat_pct,omitempty" doc:"your threat on it as a share of the top (100 = it attacks you)"`
	Elite     bool     `json:"elite,omitempty"`
	Evading   bool     `json:"evading,omitempty" doc:"resetting: your attacks do nothing; leave it"`
	Casting   *Casting `json:"casting,omitempty" doc:"interrupt it, or get out of the way"`
}

// NearbyNPC: a friendly NPC and what it offers.
type NearbyNPC struct {
	GUID GUID   `json:"guid"`
	Name string `json:"name"`
	Place
	Roles       []string `json:"roles,omitempty" doc:"vendor, repair, trainer, quest_giver, flight_master, innkeeper, banker, spirit_healer, gossip"`
	QuestStatus string   `json:"quest_status,omitempty" doc:"available (a quest for you), reward (turn one in here), incomplete, ... (see Creature)"`
}

type GearSummary struct {
	Score        int      `json:"score" doc:"0-100: how well your gear suits your level"`
	Grade        string   `json:"grade" doc:"poor, weak, ok, good"`
	AvgItemLevel float64  `json:"avg_item_level"`
	Weakest      []string `json:"weakest" doc:"the three slots holding you back most (empty ones included)"`
	Broken       []string `json:"broken,omitempty" doc:"slots at 0 durability: they give nothing; repair at an armorer"`
}

type Upgrade struct {
	Item      ItemRef `json:"item"`
	Slot      string  `json:"slot"`
	ItemLevel int32   `json:"item_level" doc:"gain over what you wear there"`
}

type ItemRef struct {
	GUID  GUID   `json:"guid"`
	Entry uint32 `json:"entry"`
	Name  string `json:"name"`
}

// ---------- travel ----------

// TaxiNode is a flight point (TaxiNodes.dbc).
type TaxiNode struct {
	ID    uint32  `json:"id"`
	Name  string  `json:"name" doc:"e.g. Stormwind, Elwynn"`
	Map   uint32  `json:"map"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Z     float64 `json:"z"`
	Known bool    `json:"known" doc:"you've discovered it (talk to its flight master once). Only accurate after you've opened a flight map (flights.map)"`
	Cost  uint32  `json:"cost,omitempty" doc:"copper from the flight master you asked (flights.map), over known nodes"`
	Hops  int     `json:"hops,omitempty" doc:"stops on the way (1 = direct)"`
}

// FlightMap is a flight master's map: where you are and where you can fly from there.
type FlightMap struct {
	Current      TaxiNode   `json:"current"`
	Destinations []TaxiNode `json:"destinations" doc:"known nodes you can reach from here, with the cost"`
	Learned      bool       `json:"learned,omitempty" doc:"talking to this flight master discovered its node"`
}

type Flight struct {
	From    TaxiNode `json:"from"`
	To      TaxiNode `json:"to"`
	Cost    uint32   `json:"cost" doc:"copper"`
	Seconds int      `json:"seconds,omitempty" doc:"flight_start: how long it will take; flight_end and results: how long it took"`
}

// Movement says who's moving your character.
type Movement struct {
	State       string    `json:"state" doc:"free (you are), in_flight, teleporting, feared, confused, charmed (the server moves you: move.to is refused), stunned, rooted"`
	Destination *Position `json:"destination,omitempty" doc:"where the server is taking you (a flight, fear)"`
	SecondsLeft float64   `json:"seconds_left,omitempty"`
	Flight      *Flight   `json:"flight,omitempty"`
}

type Teleport struct {
	From Position `json:"from"`
	To   Position `json:"to"`
	Far  bool     `json:"far" doc:"to another map (a loading screen)"`
}

type Reputation struct {
	Faction  uint32 `json:"faction"`
	Name     string `json:"name"`
	Standing int32  `json:"standing" doc:"-42000 to 42999: hated below -6000, hostile, unfriendly below 0, neutral, friendly from 3000, honored 9000, revered 21000, exalted 42000"`
	Rank     string `json:"rank"`
	AtWar    bool   `json:"at_war,omitempty"`
}

type MovementChanged struct {
	State    string `json:"state" doc:"see Movement"`
	Previous string `json:"previous"`
}

type FlightLearned struct {
	Node TaxiNode `json:"node"`
}

// Home is where your hearthstone takes you (set by binding at an innkeeper).
type Home struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Z    float64 `json:"z"`
	Map  uint32  `json:"map"`
	Area string  `json:"area"`
}

// Aura is a buff or debuff on a unit.
type Aura struct {
	Spell       uint32   `json:"spell"`
	Name        string   `json:"name"`
	Harmful     bool     `json:"harmful" doc:"a debuff"`
	Stacks      uint8    `json:"stacks,omitempty" doc:"stacks or charges (1 when it doesn't stack)"`
	SecondsLeft *float64 `json:"seconds_left,omitempty" doc:"nil for auras without a duration"`
	Duration    *float64 `json:"duration,omitempty" doc:"its full duration, seconds"`
	Caster      *UnitRef `json:"caster,omitempty"`
	Mine        bool     `json:"mine" doc:"you cast it"`
	Dispel      string   `json:"dispel,omitempty" doc:"what can remove it: magic, curse, disease, poison"`
}

type ComboPoints struct {
	Target GUID  `json:"target"`
	Points uint8 `json:"points"`
}
