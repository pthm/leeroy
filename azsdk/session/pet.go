package session

// Pets: the creature you control, a hunter's tamed beast, a warlock's demon, a mage's water elemental. The server sends
// its bar when it's summoned (SMSG_PET_SPELLS: its stance, its command, and its abilities with their autocast);
// commands, stances and abilities go back as CMSG_PET_ACTION and autocast as CMSG_PET_SPELL_AUTOCAST. The server
// doesn't confirm a command or a stance, so, as in the game's own client, pet.info reports the last one sent.

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/azsdk/client"
	"github.com/pthm/leeroy/azsdk/script"
	"github.com/pthm/leeroy/azsdk/types"
)

const (
	smsgPetSpells        = 0x179
	smsgPetCastFailed    = 0x138
	cmsgPetSpellAutocast = 0x2F3
	cmsgPetCastSpell     = 0x1F0

	actPassive  = 0x01 // a pet action's type, in its top byte: an ability it can't use by itself (passive, or cast only)
	actDisabled = 0x81 // an ability, autocast off
	actEnabled  = 0xC1 // an ability, autocast on
	actCommand  = 0x07 // stay, follow, attack
	actReaction = 0x06 // passive, defensive, aggressive

	spellFeedPet = 6991
	powerHappy   = 4 // POWER_HAPPINESS, a hunter's pet's
)

var (
	petReacts   = []string{"passive", "defensive", "aggressive"}
	petCommands = []string{"stay", "follow", "attack"}
)

// passive: Spell.dbc's SPELL_ATTR0_PASSIVE (the pet bar's actPassive also marks abilities that are only cast)
func passive(id uint32) bool {
	s := spellInfo(id)
	return s != nil && s.attrs&0x40 != 0
}

func at(names []string, i uint8) string {
	if int(i) < len(names) {
		return names[i]
	}
	return ""
}

// petBar: the pet's state as SMSG_PET_SPELLS last gave it, with the commands sent since.
type petBar struct {
	mu             sync.Mutex
	guid           uint64
	react, command uint8
	spells         []petSpell
}

type petSpell struct {
	id    uint32
	state uint8 // actPassive, actDisabled or actEnabled
}

// read: Player::PetSpellInitialize: guid (0: the pet is gone), family, duration, react, command, flags, the action
// bar (10, with the autocast state of the abilities on it), then the abilities (count, each packed: id and state)
// and cooldowns.
func (p *petBar) read(data []byte) {
	r := bytes.NewReader(data)
	var head struct {
		GUID           uint64
		Family         uint16
		Duration       uint32
		React, Command uint8
		Flags          uint16
		Bar            [10]uint32
		Count          uint8
	}
	err := binary.Read(r, binary.LittleEndian, &head)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil || head.GUID == 0 {
		p.guid, p.react, p.command, p.spells = 0, 0, 0, nil
		return
	}
	p.guid, p.react, p.command, p.spells = head.GUID, head.React, head.Command, nil
	for range head.Count {
		var v uint32
		if binary.Read(r, binary.LittleEndian, &v) != nil {
			break
		}
		p.spells = append(p.spells, petSpell{id: v & 0xFFFFFF, state: uint8(v >> 24)})
	}
	for _, v := range head.Bar { // the bar holds the autocast state of what's on it (CharmInfo::SetSpellAutocast)
		if t := uint8(v >> 24); t == actEnabled || t == actDisabled {
			for i := range p.spells {
				if p.spells[i].id == v&0xFFFFFF {
					p.spells[i].state = t
				}
			}
		}
	}
}

func (p *petBar) spell(id uint32) (petSpell, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := slices.IndexFunc(p.spells, func(s petSpell) bool { return s.id == id })
	if i < 0 {
		return petSpell{}, false
	}
	return p.spells[i], true
}

// watchPet reads the pet's packets: from before login, as a pet out when you log out comes back with you.
func (b *Bot) watchPet() {
	p := &b.pet
	b.world.AddPacketHook(func(op uint16, data []byte) {
		switch op {
		case smsgPetSpells:
			p.read(data)
		case smsgPetCastFailed: // cast count, spell, reason: as SMSG_CAST_FAILED
			if len(data) >= 6 {
				spell := binary.LittleEndian.Uint32(data[1:5])
				b.emit("pet_cast_failed", types.Cast{Count: data[0], Spell: spell, Name: spellName(spell), Code: castResultNames[data[5]]})
			}
		case smsgSpellGo:
			if g := parseSpellGo(data); g.caster != 0 && g.caster == b.GetPetGUID() {
				b.emit("pet_cast_done", types.Cast{Count: g.count, Spell: g.spell, Name: spellName(g.spell), Hits: len(g.hits)})
			}
		}
	})
}

func (b *Bot) setupPet() {
	w := b.world
	e := b.lua
	p := &b.pet
	ok := func(op string, args map[string]any) map[string]any {
		return b.lua.Reqs.Now(script.Spec{Op: op, Args: args}, script.Ok(nil))
	}
	pet := func(op string, args map[string]any) (uint64, map[string]any) {
		g := b.GetPetGUID()
		if g == 0 {
			return 0, b.refuse(op, args, "no_pet", "you have no pet out")
		}
		return g, nil
	}

	e.Func("pet.info", "() → Pet?", "your pet: health, focus or mana, happiness (a hunter's), stance, command and abilities; nil for none", func() *types.Pet {
		g := b.GetPetGUID()
		o := w.GetObject(g)
		if g == 0 || o == nil {
			return nil
		}
		u := b.GetUnitInfo(g)
		pt := uint8(o.Value(client.UnitFieldBytes0) >> 24)
		out := &types.Pet{GUID: types.GUID(g), Health: types.Resource{Current: o.Value(client.UnitFieldHealth), Max: o.Value(client.UnitFieldMaxHealth)},
			Power:  types.Resource{Current: o.Value(client.UnitFieldPower1 + uint16(pt)), Max: o.Value(client.UnitFieldMaxPower1 + uint16(pt)), Type: name(powerNames, pt)},
			Spells: []types.PetSpell{}}
		if u != nil {
			out.Name, out.Level = u.Name, u.Level
			if u.Target != 0 {
				r := b.ref(u.Target)
				out.Target = &r
			}
		}
		if max := o.Value(client.UnitFieldMaxPower1 + powerHappy); max > 0 {
			v := o.Value(client.UnitFieldPower1 + powerHappy)
			out.Happiness = &types.Happiness{Current: v, Max: max, State: []string{"unhappy", "content", "happy"}[min(2, v/333000)]}
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.guid == g {
			out.React, out.Command = at(petReacts, p.react), at(petCommands, p.command)
			for _, s := range p.spells {
				n := spellName(s.id)
				if strings.Contains(n, "(DND)") || strings.Contains(n, "Scaling") {
					continue // the server's own bookkeeping (Tamed Pet Passive 07 (DND), Hunter Pet Scaling 02), not abilities
				}
				out.Spells = append(out.Spells, types.PetSpell{ID: s.id, Name: n, Passive: passive(s.id), Autocast: s.state == actEnabled})
			}
		}
		return out
	})
	command := func(op string, cmd uint8) map[string]any {
		args := map[string]any{}
		g, refused := pet(op, args)
		if refused != nil {
			return refused
		}
		if err := w.PetAction(g, uint32(actCommand)<<24|uint32(cmd), 0); err != nil {
			return b.refuse(op, args, "failed", err.Error())
		}
		p.mu.Lock()
		p.command = cmd
		p.mu.Unlock()
		return ok(op, args)
	}
	e.Func("pet.follow", "() → Request", "your pet follows you (and stops fighting)", func() map[string]any { return command("pet.follow", 1) })
	e.Func("pet.stay", "() → Request", "your pet stays where it is", func() map[string]any { return command("pet.stay", 0) })
	e.Func("pet.attack", "(guid) → Request", "send your pet at a unit (as combat.pet_attack)", func(t uint64) map[string]any {
		args := map[string]any{"unit": gs(t)}
		g, refused := pet("pet.attack", args)
		if refused != nil {
			return refused
		}
		if b.GetUnitInfo(t) == nil {
			return b.refuse("pet.attack", args, "unit_gone", "target is not observed")
		}
		if err := w.PetAction(g, uint32(actCommand)<<24|2, t); err != nil {
			return b.refuse("pet.attack", args, "failed", err.Error())
		}
		p.mu.Lock()
		p.command = 2
		p.mu.Unlock()
		return b.request("pet.attack", args, 5*time.Second, func() *script.Outcome {
			if u := b.GetUnitInfo(g); u != nil && u.Target == t {
				return script.Ok(nil)
			}
			return nil
		}, nil)
	})
	e.Func("pet.react", "(stance) → Request", "your pet's stance: passive (it never attacks on its own), defensive (it attacks what attacks you or it), aggressive (it attacks hostiles in range)", func(stance string) map[string]any {
		args := map[string]any{"stance": stance}
		i := slices.Index(petReacts, stance)
		if i < 0 {
			return b.refuse("pet.react", args, "invalid_arguments", "passive, defensive or aggressive")
		}
		g, refused := pet("pet.react", args)
		if refused != nil {
			return refused
		}
		if err := w.PetAction(g, uint32(actReaction)<<24|uint32(i), 0); err != nil {
			return b.refuse("pet.react", args, "failed", err.Error())
		}
		p.mu.Lock()
		p.react = uint8(i)
		p.mu.Unlock()
		return ok("pet.react", args)
	})
	e.Func("pet.cast", "(id [, guid]) → Request<Cast>", "your pet uses one of its abilities (ids from pet.info), on a unit or its own target", func(id uint32, t uint64) map[string]any {
		args := map[string]any{"spell": id, "unit": gs(t)}
		g, refused := pet("pet.cast", args)
		if refused != nil {
			return refused
		}
		s, known := p.spell(id)
		switch {
		case !known:
			return b.refuse("pet.cast", args, "not_known", "your pet doesn't have that ability (pet.info lists them)")
		case passive(id):
			return b.refuse("pet.cast", args, "passive", "a passive ability: always on")
		}
		if err := w.PetAction(g, uint32(s.state)<<24|id, t); err != nil {
			return b.refuse("pet.cast", args, "failed", err.Error())
		}
		return b.request("pet.cast", args, 5*time.Second, nil, func(ev script.Event) *script.Outcome {
			c, isCast := ev.Data.(types.Cast)
			switch {
			case !isCast || c.Spell != id:
				return nil
			case c.Code == "dont_report": // the server's answer to a target out of range or sight (PetHandler.cpp)
				return script.Fail("out_of_range", "out of range or sight of your pet: the server may send it in to cast it later (a pet_cast_done event), but often it stops short", nil)
			case ev.Type == "pet_cast_done":
				return script.Ok(c)
			case ev.Type == "pet_cast_failed":
				return script.Fail(c.Code, "your pet couldn't: "+c.Code, nil)
			}
			return nil
		})
	})
	e.Func("pet.cast_at", "(id, x, y, z) → Request<Cast>", "your pet uses an ability cast at a place (a water elemental's Freeze)", func(id uint32, x, y, z float32) map[string]any {
		args := map[string]any{"spell": id, "x": x, "y": y, "z": z}
		g, refused := pet("pet.cast_at", args)
		if refused != nil {
			return refused
		}
		if _, known := p.spell(id); !known || passive(id) {
			return b.refuse("pet.cast_at", args, "not_known", "not one of your pet's abilities (pet.info lists them)")
		}
		// CMSG_PET_CAST_SPELL: the pet, cast count, spell, flags, then the target: a place (TARGET_FLAG_DEST_LOCATION,
		// no transport)
		buf := binary.LittleEndian.AppendUint64(nil, g)
		buf = append(buf, 0)
		buf = binary.LittleEndian.AppendUint32(buf, id)
		buf = append(buf, 0)
		buf = binary.LittleEndian.AppendUint32(buf, 0x40)
		buf = append(buf, 0)
		for _, v := range []float32{x, y, z} {
			buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(v))
		}
		if err := w.SendPacketRaw(cmsgPetCastSpell, buf); err != nil {
			return b.refuse("pet.cast_at", args, "failed", err.Error())
		}
		return b.request("pet.cast_at", args, 5*time.Second, nil, func(ev script.Event) *script.Outcome {
			c, isCast := ev.Data.(types.Cast)
			switch {
			case !isCast || c.Spell != id:
				return nil
			case ev.Type == "pet_cast_done":
				return script.Ok(c)
			case ev.Type == "pet_cast_failed":
				return script.Fail(map[bool]string{true: "out_of_range", false: c.Code}[c.Code == "dont_report"], "your pet couldn't: "+c.Code, nil)
			}
			return nil
		})
	})
	e.Func("pet.autocast", "(id, on) → Request", "turn your pet's use of an ability on its own on or off", func(id uint32, on bool) map[string]any {
		args := map[string]any{"spell": id, "on": on}
		g, refused := pet("pet.autocast", args)
		if refused != nil {
			return refused
		}
		if s, known := p.spell(id); !known || s.state == actPassive {
			return b.refuse("pet.autocast", args, "not_autocast", "not one of your pet's abilities it can use by itself (pet.info lists them; a passive one, or one only cast)")
		}
		body := binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint64(nil, g), id)
		if err := w.SendPacketRaw(cmsgPetSpellAutocast, append(body, map[bool]byte{true: 1, false: 0}[on])); err != nil {
			return b.refuse("pet.autocast", args, "failed", err.Error())
		}
		p.mu.Lock()
		for i := range p.spells {
			if p.spells[i].id == id {
				p.spells[i].state = map[bool]uint8{true: actEnabled, false: actDisabled}[on]
			}
		}
		p.mu.Unlock()
		return ok("pet.autocast", args)
	})
	e.Func("pet.feed", "(item) → Request<Cast>", "feed your hunter's pet food you carry (entry or guid), with Feed Pet: it gains happiness. It only eats the kinds of food its family likes", func(item uint64) map[string]any {
		args := map[string]any{"item": gs(item)}
		if _, refused := pet("pet.feed", args); refused != nil {
			return refused
		}
		it := findItem(w, item)
		switch {
		case it == nil:
			return b.refuse("pet.feed", args, "item_not_found", "no such item in your bags")
		case !w.KnowsSpell(spellFeedPet):
			return b.refuse("pet.feed", args, "not_known", "you don't know Feed Pet")
		}
		count, free := b.castSequences.reserve(time.Now(), castTimeout(spellFeedPet))
		if !free {
			return b.refuse("pet.feed", args, "cast_busy", "cast correlation slots are exhausted; wait before retrying")
		}
		b.agent.castSent(spellFeedPet)
		if err := w.CastSpellOnItemCount(spellFeedPet, uint64(it.GUID), count); err != nil {
			return b.refuse("pet.feed", args, "failed", err.Error())
		}
		return b.request("pet.feed", args, 5*time.Second, nil, func(ev script.Event) *script.Outcome {
			c, isCast := ev.Data.(types.Cast)
			switch {
			case !isCast || c.Spell != spellFeedPet:
				return nil
			case ev.Type == "cast_done":
				return script.Ok(c)
			case ev.Type == "cast_failed":
				return script.Fail(c.Code, "Feed Pet failed: "+c.Code, nil)
			}
			return nil
		})
	})
}
