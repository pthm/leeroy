// Package types is the data model of the agent API: every value the Lua API returns, every action result and
// every event's data is one of these structs, with the same field names everywhere. Lua sees them as tables
// (through their json names), the RPC API as JSON, and the TypeScript SDK as generated interfaces (cmd/apigen
// writes sdk/ts/src/types.ts and docs/types.md from this file), so all three stay in sync.
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

// Unit is what creatures and players share.
type Unit struct {
	GUID      GUID    `json:"guid"`
	Name      string  `json:"name" doc:"empty for a moment after a creature first comes into view"`
	Level     uint32  `json:"level"`
	Health    uint32  `json:"health"`
	MaxHealth uint32  `json:"max_health"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Z         float64 `json:"z"`
	Distance  float64 `json:"distance" doc:"yards from you"`
	Alive     bool    `json:"alive"`
	Reaction  string  `json:"reaction" doc:"toward you: hostile (attacks on sight), neutral (fights back) or friendly"`
	Target    GUID    `json:"target" doc:"who it's targeting; null for nobody"`
	InCombat  bool    `json:"in_combat"`
}

type Creature struct {
	Unit
	Entry         uint32   `json:"entry" doc:"creature template id (wowdb npc <entry>)"`
	Elite         bool     `json:"elite" doc:"much stronger than its level suggests"`
	Roles         []string `json:"roles,omitempty" doc:"what it offers: vendor, repair, trainer, quest_giver, flight_master, innkeeper, banker, spirit_healer, gossip"`
	Attackable    bool     `json:"attackable" doc:"can be attacked at all (false for triggers, dummies, protected NPCs)"`
	Lootable      bool     `json:"lootable" doc:"a corpse with loot for you"`
	TappedByOther bool     `json:"tapped_by_other" doc:"someone else damaged it first: killing it gives you nothing"`
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
	GUID     GUID    `json:"guid"`
	Entry    uint32  `json:"entry" doc:"game object template id"`
	Name     string  `json:"name" doc:"empty for a moment after it first comes into view"`
	Type     string  `json:"type" doc:"chest, door, button, goober (quest objects), questgiver, mailbox, fishing_node, ..."`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Z        float64 `json:"z"`
	Distance float64 `json:"distance"`
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
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Z         float64 `json:"z"`
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
	Entry         uint32           `json:"entry" doc:"item template id (wowdb item <entry>)"`
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

// ---------- spells, quests, talents ----------

type Spell struct {
	ID                 uint32  `json:"id"`
	Name               string  `json:"name"`
	Rank               string  `json:"rank"`
	CastMS             uint32  `json:"cast_ms" doc:"0 = instant"`
	CooldownMS         uint32  `json:"cooldown_ms"`
	CategoryCooldownMS uint32  `json:"category_cooldown_ms"`
	GCDMS              uint32  `json:"gcd_ms"`
	Range              float32 `json:"range" doc:"yards against hostile targets (melee ~5)"`
	Cost               uint32  `json:"cost"`
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

type Talent struct {
	ID        uint32 `json:"id"`
	Name      string `json:"name"`
	Rank      int    `json:"rank"`
	MaxRank   int    `json:"max_rank"`
	Row       uint32 `json:"row" doc:"row r needs 5 × r points in the tree"`
	Col       uint32 `json:"col"`
	Spell     uint32 `json:"spell" doc:"the spell of your current (or first) rank: spells.info / wowdb spell"`
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
	Leader  string        `json:"leader"`
	Members []GroupMember `json:"members" doc:"the others, not you"`
}

type GroupMember struct {
	Name   string `json:"name"`
	GUID   GUID   `json:"guid"`
	Online bool   `json:"online"`
}

type TradeSide struct {
	Money uint32 `json:"money"`
	Items []Item `json:"items"`
}

type Trade struct {
	State        string    `json:"state" doc:"none, waiting (you asked), requested (they asked: trade.open), open, complete, cancelled, rejected, busy, too_far, no_target, closed"`
	With         string    `json:"with,omitempty"`
	Mine         TradeSide `json:"mine"`
	Theirs       TradeSide `json:"theirs"`
	TheyAccepted bool      `json:"they_accepted"`
}

type Inspect struct {
	Player
	Gear         []Item  `json:"gear"`
	AvgItemLevel float64 `json:"avg_item_level"`
}

type CombatEntry struct {
	T      int64   `json:"t" doc:"unix ms"`
	Kind   string  `json:"kind" doc:"melee, spell, dot, heal, hot, kill"`
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
}

// Result is an action's request (Pending) or its outcome.
type Result struct {
	ID      int            `json:"id"`
	Op      string         `json:"op" doc:"the function, e.g. spells.cast"`
	Pending bool           `json:"pending,omitempty" doc:"not resolved yet: await it, or result(id) later"`
	OK      bool           `json:"ok"`
	Data    any            `json:"data,omitempty" doc:"the op's result type (lua-api.md)"`
	Error   *Error         `json:"error,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
	MS      int64          `json:"ms,omitempty" doc:"how long it took"`
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
	Error      string `json:"error,omitempty"`
}

type MobSummary struct {
	Count    int    `json:"count" doc:"hostile creatures within 30 yd, at most 5 levels below you"`
	MaxLevel uint32 `json:"max_level"`
	Elites   int    `json:"elites"`
}

// Status is a one-call snapshot of your character (me.status, `wow status`).
type Status struct {
	Name        string       `json:"name"`
	Class       string       `json:"class"`
	Race        string       `json:"race"`
	Level       uint32       `json:"level"`
	XP          XP           `json:"xp"`
	Health      Resource     `json:"health"`
	Power       Resource     `json:"power"`
	Money       uint32       `json:"money" doc:"copper"`
	Position    Position     `json:"position"`
	Location    Location     `json:"location"`
	Alive       bool         `json:"alive"`
	Ghost       bool         `json:"ghost"`
	InCombat    bool         `json:"in_combat"`
	Resting     bool         `json:"resting"`
	Casting     *Casting     `json:"casting,omitempty"`
	FreeSlots   int          `json:"free_slots"`
	Stats       *Stats       `json:"stats,omitempty"`
	Target      *Unit        `json:"target,omitempty"`
	Attackers   []Creature   `json:"attackers"`
	Mobs        MobSummary   `json:"mobs_nearby"`
	Players     []Player     `json:"players" doc:"within 60 yd"`
	Group       *Group       `json:"group,omitempty"`
	Quests      []Quest      `json:"quests"`
	Broken      []Item       `json:"broken_gear"`
	BagUpgrades []Comparison `json:"bag_upgrades" doc:"items you carry that beat what you wear (by item level)"`
	LastMinute  *Meter       `json:"last_minute,omitempty" doc:"your fighting over the last minute"`
	Corpse      *Corpse      `json:"corpse,omitempty"`
	Tasks       []TaskInfo   `json:"tasks"`
}
