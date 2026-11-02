package session

// The character's game log: the records its session keeps in its event stream file beside the events (script.Bus.Write:
// no seq, not shown to the agent): its state each second (position, health, power, money, group, target, pet), the
// creatures near it every 2 s and the transports on its map every 4, its group changes, login and logout, and the API
// calls (api, script_error). With the events it saw, that's the run's record of the game (azbench/gamelog reads it).
// And Diagnostics: the session's diagnostic stream, in its own file.

import (
	"encoding/json"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// Diagnostics is the session's diagnostic stream (types.Diagnostic), one file shared by all sessions in a daemon and
// appended to: azbench's harness writes to it too, each line one write.
type Diagnostics struct {
	mu sync.Mutex
	f  *os.File
}

// OpenDiagnostics appends diagnostics to path.
func OpenDiagnostics(path string) (*Diagnostics, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Diagnostics{f: f}, nil
}

func (d *Diagnostics) write(ev types.Diagnostic) {
	if d == nil {
		return
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.f.Write(append(line, '\n'))
}

// SetDiagnostics attaches the diagnostics file before Run.
func (b *Bot) SetDiagnostics(d *Diagnostics) { b.diag = d }

// GameLog is gameLog, for the API (its calls and script errors).
func (b *Bot) GameLog(kind string, body any) { b.gameLog(kind, body) }

// gameLog writes a record in the character's event stream file only (script.Bus.Write).
func (b *Bot) gameLog(kind string, body any) {
	if b.lua != nil && b.lua.Bus != nil {
		b.lua.Bus.Write(kind, body)
	}
}

// logTransports: every boat and zeppelin on the character's map (the server shows them all on arrival), where each is
// now.
func (b *Bot) logTransports() {
	b.scanTransports()
	aboard, _ := b.world.Transport()
	b.trs.mu.Lock()
	var guids []uint64
	for g := range b.trs.recs {
		guids = append(guids, g)
	}
	b.trs.mu.Unlock()
	var rows []types.TransportState
	now := time.Now()
	for _, g := range guids {
		if v, ok := b.transportView(g, now, aboard); ok {
			rows = append(rows, types.TransportState{GUID: types.GUID(g), Entry: v.Entry, Name: v.Name, Map: v.Map,
				X: round1(float32(v.X)), Y: round1(float32(v.Y)), O: v.O})
		}
	}
	if len(rows) > 0 {
		b.gameLog("transports", rows)
	}
}

// startGameLog starts the game log once the character is in the world (called from setupControl).
func (b *Bot) startGameLog() {
	w := b.world
	b.gameLog("login", types.Login{Race: name(raceNames, b.GetRace()), Class: name(classNames, b.GetClass()), Level: w.PlayerLevel()})
	me := w.CharGUID()
	// party changes. The server
	// sends the list at login, before recording starts, so the current state is recorded first. It also resends the
	// list unchanged on many updates (about 50 times for each real change), so only changes are recorded.
	var lastGroup *types.GroupState
	record := func(g types.GroupState) {
		if lastGroup == nil || !reflect.DeepEqual(g, *lastGroup) {
			lastGroup = &g
			b.gameLog("group", g)
		}
	}
	onGroup := func(st client.GroupState) {
		observed := observedGroup(me, st)
		b.lua.RecordObservation("group_state", types.DiagnosticDetails{Group: &observed})
		if !st.InGroup {
			record(types.GroupState{Members: []string{}})
			return
		}
		members, leader := []string{b.config.CharacterName}, ""
		if st.LeaderGUID == me {
			leader = b.config.CharacterName
		}
		for _, m := range st.Members {
			members = append(members, m.Name)
			if m.GUID == st.LeaderGUID {
				leader = m.Name
			}
		}
		record(types.GroupState{Group: uint32(st.GroupGUID), Leader: leader, Members: members})
	}
	onGroup(w.GroupState())
	w.AddGroupListHook(onGroup)

	// its state once a second, nearby creatures every 2 s and transports every 4, and the diagnostics' samples
	go func() {
		b.lua.RecordObservation("runtime_state", types.DiagnosticDetails{Component: "tooling", State: "instrumented_v1"})
		var previousGear *types.DiagnosticGear
		sampleCondition := func() {
			available := w.GetObject(w.CharGUID()) != nil
			alive := b.IsAlive() && !b.IsGhost()
			g := b.gear()
			gear := types.DiagnosticGear{Money: w.Money(), MainHandBroken: g.Weapon != nil && g.Weapon.Broken, BrokenSlots: g.Broken}
			if available && (previousGear == nil || !reflect.DeepEqual(*previousGear, gear)) {
				b.lua.RecordObservation("gear_state", types.DiagnosticDetails{Gear: &gear})
				previousGear = &gear
			}
			b.lua.RecordObservation("character_state", types.DiagnosticDetails{SampleAvailable: &available, Alive: &alive, Gear: &gear})
		}
		sampleCondition()
		var previous *types.DiagnosticQuestLog
		observeQuests := func() {
			current := observedQuests(w.GetObject(w.CharGUID()))
			if previous == nil || !reflect.DeepEqual(*previous, current) {
				b.lua.RecordObservation("quest_state", types.DiagnosticDetails{QuestLog: &current})
				previous = &current
			}
		}
		observeQuests()
		t := time.NewTicker(time.Second)
		defer t.Stop()
		tick := 0
		for {
			select {
			case <-b.stopCh:
				b.gameLog("logout", nil)
				return
			case <-t.C:
				sampleCondition()
				observeQuests()
				x, y, z, o, m := w.Position()
				hp, hpmax := b.GetHealth()
				pw, pwmax := b.GetPower()
				st := types.State{Position: types.Position{X: round1(x), Y: round1(y), Z: round1(z), O: facing(o), Map: m},
					Level: w.PlayerLevel(), Money: w.Money(), Group: groupID(w),
					Health: types.Resource{Current: hp, Max: hpmax}, Power: types.Resource{Current: pw, Max: pwmax, Type: name(powerNames, b.GetPowerType())},
					Alive: b.IsAlive(), Ghost: b.IsGhost(), InCombat: b.InCombat()}
				if me := w.GetObject(w.CharGUID()); me != nil {
					st.XP = me.Value(0x27A)
				}
				if pg := b.GetPetGUID(); pg != 0 {
					if u := b.GetUnitInfo(pg); u != nil {
						st.Pet = &types.PetState{UnitRef: b.ref(pg), X: round1(u.PosX), Y: round1(u.PosY), Z: round1(u.PosZ),
							Health: types.Resource{Current: u.Health, Max: u.MaxHealth}, Alive: u.IsAlive}
					}
				}
				if tg := b.GetTargetGUID(); tg != 0 && b.IsAlive() {
					r := b.ref(tg)
					st.Target = &r
				}
				b.gameLog("state", st)
				if tick++; tick%2 == 0 {
					b.gameLog("units", types.Nearby{Map: m, Units: b.nearbyUnitRows()})
				}
				if tick%4 == 0 {
					b.logTransports()
				}
			}
		}
	}()
}

// groupID is the server's group id (the low half of the group GUID), 0 when not in a group.
func groupID(w *client.WorldClient) uint32 {
	if st := w.GroupState(); st.InGroup {
		return uint32(st.GroupGUID)
	}
	return 0
}

// playerPet: a creature a player summoned that isn't a totem (UNIT_FIELD_SUMMONEDBY a player's GUID, whose high
// part is 0): a pet, a demon, a water elemental.
func (b *Bot) playerPet(u UnitInfo) bool {
	o := b.world.GetObject(u.GUID)
	if o == nil {
		return false
	}
	by := o.GUIDField(client.UnitFieldSummonedBy)
	if by == 0 || by>>48 != 0 {
		return false
	}
	creatures.Lock()
	info, known := creatures.info[u.Entry]
	creatures.Unlock()
	return !known || info.typ != 11
}

// nearbyUnitRows: the creatures within 60 yd (types.NearbyUnit)
func (b *Bot) nearbyUnitRows() []types.NearbyUnit {
	var faction uint32
	if me := b.world.GetObject(b.world.CharGUID()); me != nil {
		faction = me.Value(fieldUnitFaction)
	}
	rows := []types.NearbyUnit{}
	for _, u := range b.GetNearbyUnits(60) {
		if u.IsPlayer || u.Flags&0x2000000 != 0 { // UNIT_FLAG_NOT_SELECTABLE: invisible triggers
			continue
		}
		if b.playerPet(u) { // a player's pet is in its master's pos, not here
			continue
		}
		state := map[string]float64{"hostile": 0, "neutral": 1, "friendly": 2}[factionReaction(u.Faction, faction)]
		if !u.IsAlive {
			state += 4
		}
		hp := 0.0
		if u.MaxHealth > 0 {
			hp = float64(100 * u.Health / u.MaxHealth)
		}
		rows = append(rows, types.NearbyUnit{float64(u.Entry), float64(u.Level), round1(u.PosX), round1(u.PosY), hp, state})
	}
	return rows
}
