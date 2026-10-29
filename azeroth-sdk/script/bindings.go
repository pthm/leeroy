package script

import (
	"strconv"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// BotAPI is what the engine needs from the bot for the core bot.* functions. The session registers the rest with
// RegisterFunc.
type BotAPI interface {
	GetPosition() (x, y, z, o float32)
	MoveTo(x, y, z float32) error
	StopMoving() error
	IsMoving() bool

	AttackTarget(guid uint64) error
	StopAttack() error
	SetTarget(guid uint64) error
	CastSpell(spellID uint32, targetGUID uint64) error
	IsSpellReady(spellID uint32) bool
	GetHealth() (current, max uint32)
	GetPower() (current, max uint32)
	GetLevel() uint32
	InCombat() bool
	IsAlive() bool
	GetTargetGUID() uint64

	GetNearbyUnits(maxDist float32) []UnitInfo
	GetNearbyPlayers(maxDist float32) []UnitInfo
	GetUnitInfo(guid uint64) *UnitInfo

	SendChat(message string) error
	SendCommand(command string) error
	SendGuildCommand(command string) error
	Loot(guid uint64) error
	LootAll(guid uint64) error

	Log(format string, args ...interface{})

	GetClass() uint8
	HasAuraOn(guid uint64, spellID uint32) bool
	CanCast(spellID uint32, targetGUID uint64) bool
	GetPetGUID() uint64
	PetAttack(target uint64)
	GetStance() int
	GetOwnGUID() uint64
	GetPowerType() uint8
	IsBehindTarget(targetGUID uint64) bool

	GetFacing() float32
	SetFacing(o float32) error
	FaceTarget(guid uint64) bool
	SetSheathed(state uint32) error

	ValidationMode() bool
	// ConsumeTeleport is true once after a completed teleport.
	ConsumeTeleport() bool
}

// UnitInfo is a nearby unit as scripts see it.
type UnitInfo struct {
	GUID      uint64
	Entry     uint32
	Health    uint32
	MaxHealth uint32
	Level     uint32
	PosX      float32
	PosY      float32
	PosZ      float32
	IsAlive   bool
	IsPlayer  bool
	Distance  float32
	Name      string
	Faction   uint32
	NPCFlags  uint32
	Flags     uint32
	DynFlags  uint32
	Lootable  bool
	Target    uint64 // who this unit is targeting (0 = none)
}

func gstr(g uint64) lua.LString { return lua.LString(strconv.FormatUint(g, 10)) }

func unitTable(L *lua.LState, u *UnitInfo) *lua.LTable {
	t := L.CreateTable(0, 18)
	t.RawSetString("guid", gstr(u.GUID)) // strings: GUIDs exceed a double's precision
	t.RawSetString("entry", lua.LNumber(u.Entry))
	t.RawSetString("health", lua.LNumber(u.Health))
	t.RawSetString("max_health", lua.LNumber(u.MaxHealth))
	t.RawSetString("level", lua.LNumber(u.Level))
	t.RawSetString("x", lua.LNumber(u.PosX))
	t.RawSetString("y", lua.LNumber(u.PosY))
	t.RawSetString("z", lua.LNumber(u.PosZ))
	t.RawSetString("is_alive", lua.LBool(u.IsAlive))
	t.RawSetString("is_player", lua.LBool(u.IsPlayer))
	t.RawSetString("distance", lua.LNumber(u.Distance))
	t.RawSetString("name", lua.LString(u.Name))
	t.RawSetString("faction", lua.LNumber(u.Faction))
	t.RawSetString("npc_flags", lua.LNumber(u.NPCFlags))
	t.RawSetString("flags", lua.LNumber(u.Flags))
	t.RawSetString("dyn_flags", lua.LNumber(u.DynFlags))
	t.RawSetString("lootable", lua.LBool(u.Lootable))
	t.RawSetString("target", gstr(u.Target))
	return t
}

// registerBindings installs the core bot.* functions (called once from NewEngine).
func (e *Engine) registerBindings() {
	b := e.bot
	num := func(L *lua.LState, i int) float64 { return float64(L.OptNumber(i, 0)) }
	guid := func(L *lua.LState, i int) uint64 { return guidOf(L.Get(i)) }
	push := func(L *lua.LState, vs ...lua.LValue) int {
		for _, v := range vs {
			L.Push(v)
		}
		return len(vs)
	}
	units := func(L *lua.LState, us []UnitInfo) int {
		t := L.CreateTable(len(us), 0)
		for i := range us {
			t.Append(unitTable(L, &us[i]))
		}
		return push(L, t)
	}
	dist := func(L *lua.LState) float32 {
		if d := num(L, 1); d > 0 {
			return float32(d)
		}
		return 30
	}
	fns := map[string]lua.LGFunction{
		"now_ms": func(L *lua.LState) int { return push(L, lua.LNumber(time.Now().UnixMilli())) },
		"get_position": func(L *lua.LState) int {
			x, y, z, o := b.GetPosition()
			return push(L, lua.LNumber(x), lua.LNumber(y), lua.LNumber(z), lua.LNumber(o))
		},
		"get_facing":  func(L *lua.LState) int { return push(L, lua.LNumber(b.GetFacing())) },
		"set_facing":  func(L *lua.LState) int { b.SetFacing(float32(num(L, 1))); return 0 },
		"face_target": func(L *lua.LState) int { return push(L, lua.LBool(b.FaceTarget(guid(L, 1)))) },
		"set_sheath":  func(L *lua.LState) int { b.SetSheathed(uint32(num(L, 1))); return 0 },
		"stop_moving": func(L *lua.LState) int { b.StopMoving(); return 0 },
		"is_moving":   func(L *lua.LState) int { return push(L, lua.LBool(b.IsMoving())) },
		"attack":      func(L *lua.LState) int { return push(L, lua.LBool(b.AttackTarget(guid(L, 1)) == nil)) },
		"stop_attack": func(L *lua.LState) int { b.StopAttack(); return 0 },
		"set_target":  func(L *lua.LState) int { b.SetTarget(guid(L, 1)); return 0 },
		"is_spell_ready": func(L *lua.LState) int {
			return push(L, lua.LBool(b.IsSpellReady(uint32(num(L, 1)))))
		},
		"get_health": func(L *lua.LState) int {
			c, m := b.GetHealth()
			return push(L, lua.LNumber(c), lua.LNumber(m))
		},
		"get_power": func(L *lua.LState) int {
			c, m := b.GetPower()
			return push(L, lua.LNumber(c), lua.LNumber(m))
		},
		"get_level":          func(L *lua.LState) int { return push(L, lua.LNumber(b.GetLevel())) },
		"in_combat":          func(L *lua.LState) int { return push(L, lua.LBool(b.InCombat())) },
		"is_alive":           func(L *lua.LState) int { return push(L, lua.LBool(b.IsAlive())) },
		"get_target":         func(L *lua.LState) int { return push(L, gstr(b.GetTargetGUID())) },
		"get_nearby_units":   func(L *lua.LState) int { return units(L, b.GetNearbyUnits(dist(L))) },
		"get_nearby_players": func(L *lua.LState) int { return units(L, b.GetNearbyPlayers(dist(L))) },
		"get_unit": func(L *lua.LState) int {
			if u := b.GetUnitInfo(guid(L, 1)); u != nil {
				return push(L, unitTable(L, u))
			}
			return push(L, lua.LNil)
		},
		"send_chat":          func(L *lua.LState) int { b.SendChat(L.ToString(1)); return 0 },
		"send_command":       func(L *lua.LState) int { b.SendCommand(L.ToString(1)); return 0 },
		"send_guild_command": func(L *lua.LState) int { b.SendGuildCommand(L.ToString(1)); return 0 },
		"loot":               func(L *lua.LState) int { b.Loot(guid(L, 1)); return 0 },
		"get_class":          func(L *lua.LState) int { return push(L, lua.LNumber(b.GetClass())) },
		"get_own_guid":       func(L *lua.LState) int { return push(L, gstr(b.GetOwnGUID())) },
		"get_faction": func(L *lua.LState) int {
			if u := b.GetUnitInfo(b.GetOwnGUID()); u != nil {
				return push(L, lua.LNumber(u.Faction))
			}
			return push(L, lua.LNumber(0))
		},
		"has_aura_on": func(L *lua.LState) int {
			return push(L, lua.LBool(b.HasAuraOn(guid(L, 1), uint32(num(L, 2)))))
		},
		"can_cast": func(L *lua.LState) int {
			return push(L, lua.LBool(b.CanCast(uint32(num(L, 1)), guid(L, 2))))
		},
		"get_pet_guid":     func(L *lua.LState) int { return push(L, gstr(b.GetPetGUID())) },
		"pet_attack":       func(L *lua.LState) int { b.PetAttack(guid(L, 1)); return 0 },
		"get_stance":       func(L *lua.LState) int { return push(L, lua.LNumber(b.GetStance())) },
		"get_power_type":   func(L *lua.LState) int { return push(L, lua.LNumber(b.GetPowerType())) },
		"is_behind_target": func(L *lua.LState) int { return push(L, lua.LBool(b.IsBehindTarget(guid(L, 1)))) },
		"validation_mode":  func(L *lua.LState) int { return push(L, lua.LBool(b.ValidationMode())) },
		"consume_teleport": func(L *lua.LState) int { return push(L, lua.LBool(b.ConsumeTeleport())) },
	}
	for name, fn := range fns {
		e.setBot(name, fn)
	}
	// move_to, cast_spell and loot_all are actions: the session replaces them with request-returning versions.
	e.setBot("move_to", func(L *lua.LState) int {
		return push(L, lua.LBool(b.MoveTo(float32(num(L, 1)), float32(num(L, 2)), float32(num(L, 3))) == nil))
	})
	e.setBot("cast_spell", func(L *lua.LState) int {
		return push(L, lua.LBool(b.CastSpell(uint32(num(L, 1)), guid(L, 2)) == nil))
	})
	e.setBot("loot_all", func(L *lua.LState) int { b.LootAll(guid(L, 1)); return 0 })
}
