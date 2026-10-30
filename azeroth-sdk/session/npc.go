package session

// NPC dialogue: gossip menus and what NPCs say, quest givers' markers and quest lists, trainer lists; decoded from
// what the server sends the client.

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

const (
	cmsgGossipHello           = 0x17B
	cmsgGossipSelectOption    = 0x17C
	smsgGossipMessage         = 0x17D
	smsgGossipComplete        = 0x17E
	cmsgTrainerList           = 0x1B0
	smsgTrainerList           = 0x1B1
	cmsgNPCTextQuery          = 0x17F
	smsgNPCTextUpdate         = 0x180
	smsgQuestgiverStatus      = 0x183
	smsgQuestList             = 0x185
	smsgQuestDetails          = 0x188
	cmsgQuestgiverStatusMulti = 0x417
	smsgQuestgiverStatusMulti = 0x418
)

// quest giver markers (QuestGiverStatus): what a real client draws over the NPC
var questStatuses = map[uint8]string{1: "unavailable", 2: "available_low_level", 3: "reward", 4: "available_low_level",
	5: "incomplete", 6: "reward", 7: "available_repeatable", 8: "available", 9: "reward", 10: "reward"}

var gossipIcons = map[uint8]string{0: "chat", 1: "vendor", 2: "taxi", 3: "trainer", 4: "interact", 5: "interact", 6: "banker",
	7: "talk", 8: "tabard", 9: "battle", 10: "dot"}

var trainerStates = map[uint8]string{0: "available", 1: "unavailable", 2: "known"}

type npcState struct {
	mu         sync.Mutex
	menu       *types.Gossip // the open gossip menu (the server keeps one per player)
	menuAt     time.Time
	menuKind   string
	closedAt   time.Time
	windowAt   time.Time
	windowKind string
	windowNPC  uint64
	trainers   map[uint64]types.Trainer
	trainAt    map[uint64]time.Time
	texts      map[uint32]string // npc_text id -> what the NPC says
	status     map[uint64]uint8  // quest giver guid -> QuestGiverStatus
	send       func(op uint16, body []byte)
}

// questStatus: the marker over a quest giver; "" for none (or not a quest giver, or not known yet)
func (s *npcState) questStatus(guid uint64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return questStatuses[s.status[guid]]
}

// setMenu records a dialogue the server opened, asking for its text if we haven't seen it.
func (s *npcState) setMenu(g types.Gossip) {
	s.setMenuKind(g, "gossip")
}

func (s *npcState) setMenuKind(g types.Gossip, kind string) {
	s.mu.Lock()
	_, known := s.texts[g.TextID]
	s.menu, s.menuAt = &g, time.Now()
	s.menuKind = kind
	s.mu.Unlock()
	if !known && g.TextID != 0 && s.send != nil {
		s.send(cmsgNPCTextQuery, binary.LittleEndian.AppendUint64(binary.LittleEndian.AppendUint32(nil, g.TextID), uint64(g.NPC)))
	}
}

func (s *npcState) setWindow(npc uint64, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.windowAt, s.windowNPC, s.windowKind = time.Now(), npc, kind
	s.menu = nil
}

func (b *Bot) gossipAfter(npc uint64, asked time.Time, allowClosed bool) func() *script.Outcome {
	return func() *script.Outcome {
		s := b.npc
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.windowNPC == npc && s.windowAt.After(asked) && s.windowAt.After(s.closedAt) && s.windowAt.After(s.menuAt) {
			return script.Ok(types.GossipWindow{Kind: s.windowKind, NPC: types.GUID(npc)})
		}
		if s.menu != nil && uint64(s.menu.NPC) == npc && s.menuAt.After(asked) {
			g := *s.menu
			if t, ok := s.texts[g.TextID]; ok {
				g.Text = t
			} else if g.TextID != 0 && time.Since(s.menuAt) < time.Second {
				return nil
			}
			g.Text = b.gameText(g.Text)
			return script.Ok(types.GossipWindow{Kind: s.menuKind, NPC: g.NPC, Value: &g})
		}
		if s.closedAt.After(asked) {
			if allowClosed {
				return script.Ok(types.GossipWindow{Kind: "closed", NPC: types.GUID(npc)})
			}
			return script.Fail("not_available", "the NPC closed the interaction without opening a window", nil)
		}
		return nil
	}
}

func (s *npcState) onPacket(op uint16, data []byte) {
	r := bytes.NewReader(data)
	u8 := func() uint8 { v, _ := r.ReadByte(); return v }
	u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
	u64 := func() (v uint64) { binary.Read(r, binary.LittleEndian, &v); return }
	switch op {
	case smsgListInventory:
		if len(data) >= 9 {
			s.setWindow(u64(), "vendor")
		}
	case smsgShowTaxiNodes:
		if len(data) >= 16 {
			u32()
			s.setWindow(u64(), "flight")
		}
	case smsgGossipMessage: // PlayerMenu::SendGossipMenu
		g := types.Gossip{NPC: types.GUID(u64()), Menu: u32(), TextID: u32(), Options: []types.GossipOption{}, Quests: []types.GossipQuest{}}
		for n := u32(); n > 0 && r.Len() > 0; n-- {
			o := types.GossipOption{Index: u32()}
			o.Icon = gossipIcons[u8()]
			o.Coded = u8() != 0
			o.Money = u32()
			o.Text, o.Confirm = readCString(r), readCString(r)
			g.Options = append(g.Options, o)
		}
		for n := u32(); n > 0 && r.Len() > 0; n-- {
			q := types.GossipQuest{Quest: u32()}
			u32() // icon
			q.Level = int32(u32())
			u32() // flags
			u8()  // repeatable
			q.Title = readCString(r)
			g.Quests = append(g.Quests, q)
		}
		s.setMenu(g)
	case smsgQuestList: // PlayerMenu::SendQuestGiverQuestList: a quest giver with no gossip menu
		g := types.Gossip{NPC: types.GUID(u64()), Text: readCString(r), Options: []types.GossipOption{}, Quests: []types.GossipQuest{}}
		u32() // emote delay
		u32() // emote
		for n := u8(); n > 0 && r.Len() > 0; n-- {
			q := types.GossipQuest{Quest: u32()}
			u32() // icon
			q.Level = int32(u32())
			u32() // flags
			u8()  // repeatable
			q.Title = readCString(r)
			g.Quests = append(g.Quests, q)
		}
		s.setMenuKind(g, "quest")
	case smsgQuestDetails: // PlayerMenu::SendQuestGiverQuestDetails: a quest giver with a single quest for you
		g := types.Gossip{NPC: types.GUID(u64()), Options: []types.GossipOption{}}
		u64() // who shared it
		q := types.GossipQuest{Quest: u32(), Title: readCString(r)}
		g.Text = readCString(r)
		g.Quests = []types.GossipQuest{q}
		s.setMenuKind(g, "quest")
	case smsgNPCTextUpdate: // text id, then 8 options: probability, text (male), text (female), language, 3 emotes
		id := u32()
		best, text := float32(-1), ""
		for range 8 {
			var p float32
			binary.Read(r, binary.LittleEndian, &p)
			t0, t1 := readCString(r), readCString(r)
			r.Seek(4+3*8, 1)
			if t := cmp.Or(t0, t1); t != "" && p > best {
				best, text = p, t
			}
		}
		s.mu.Lock()
		s.texts[id] = text
		s.mu.Unlock()
	case smsgQuestgiverStatus: // guid, status
		guid := u64()
		s.mu.Lock()
		s.status[guid] = u8()
		s.mu.Unlock()
	case smsgQuestgiverStatusMulti: // count, then guid, status: every quest giver in sight
		s.mu.Lock()
		for n := u32(); n > 0 && r.Len() >= 9; n-- {
			guid := u64()
			s.status[guid] = u8()
		}
		s.mu.Unlock()
	case smsgGossipComplete:
		s.mu.Lock()
		s.menu, s.closedAt = nil, time.Now()
		s.mu.Unlock()
	case smsgTrainerList: // WorldPackets::NPC::TrainerList
		if len(data) < 16 {
			return
		}
		guid := u64()
		s.setWindow(guid, "trainer")
		t := types.Trainer{NPC: types.GUID(guid), Spells: []types.TrainerSpell{}}
		u32() // type: class, mounts, tradeskills, pets
		for n := u32(); n > 0 && r.Len() >= 38; n-- {
			sp := types.TrainerSpell{Spell: u32()}
			sp.State = trainerStates[u8()]
			sp.Cost = u32()
			u32() // talent points
			u32() // profession points (first rank of a primary profession)
			sp.Level = uint32(u8())
			sp.SkillLine, sp.SkillRank = u32(), u32()
			for range 3 {
				if req := u32(); req != 0 {
					sp.Requires = append(sp.Requires, req)
				}
			}
			sp.Name = spellName(sp.Spell)
			t.Spells = append(t.Spells, sp)
		}
		t.Greeting = readCString(r)
		s.mu.Lock()
		s.trainers[guid], s.trainAt[guid] = t, time.Now()
		s.mu.Unlock()
	}
}

func (b *Bot) setupNPC() {
	w := b.world
	s := &npcState{trainers: map[uint64]types.Trainer{}, trainAt: map[uint64]time.Time{}, texts: map[uint32]string{},
		status: map[uint64]uint8{}, send: func(op uint16, body []byte) { w.SendPacketRaw(op, body) }}
	b.npc = s
	w.AddPacketHook(func(op uint16, data []byte) { s.onPacket(op, data) })
	e := b.lua
	// The server sends quest givers' markers when quests change; a real client also asks for each NPC that comes into
	// sight. ponytail: ask for all of them every few seconds instead.
	go func() {
		for range time.Tick(4 * time.Second) {
			if w.GetObject(w.CharGUID()) != nil {
				w.SendPacketRaw(cmsgQuestgiverStatusMulti, nil)
			}
		}
	}()

	e.Func("gossip.open", "(npc_guid) → Request<GossipWindow>", "talk to a nearby NPC: the observed window kind and dialogue value, when present. Fails not_available on refusal or send failure, timeout after 5 seconds without a response",
		func(npc uint64) map[string]any {
			args := map[string]any{"npc_guid": gs(npc)}
			if r := b.noNPC("gossip.open", args, &npc); r != nil {
				return r
			}
			asked := time.Now()
			if err := w.SendPacketRaw(cmsgGossipHello, binary.LittleEndian.AppendUint64(nil, npc)); err != nil {
				return b.refuse("gossip.open", args, "not_available", "the server connection could not send the interaction")
			}
			return b.request("gossip.open", args, 5*time.Second, b.gossipAfter(npc, asked, false), nil)
		})
	e.Func("gossip.select", "(index [, code = nil]) → Request<GossipWindow>", "choose a dialogue option: returns the observed window kind, including closed. A closed window does not confirm a bind or teleport. code supplies text for coded options. Fails on no_menu, bad_option, not_available, or timeout after 5 seconds",
		func(index uint32, code string) map[string]any {
			args := map[string]any{"index": index}
			s.mu.Lock()
			menu := s.menu
			s.mu.Unlock()
			if menu == nil {
				return b.refuse("gossip.select", args, "no_menu", "no gossip menu is open: gossip.open(npc) first")
			}
			for _, o := range menu.Options {
				if o.Index != index {
					continue
				}
				body := binary.LittleEndian.AppendUint64(nil, uint64(menu.NPC))
				body = binary.LittleEndian.AppendUint32(body, menu.Menu)
				body = binary.LittleEndian.AppendUint32(body, index)
				if o.Coded {
					body = append(append(body, code...), 0)
				}
				asked := time.Now()
				if err := w.SendPacketRaw(cmsgGossipSelectOption, body); err != nil {
					return b.refuse("gossip.select", args, "not_available", "the server connection could not send the selection")
				}
				return b.request("gossip.select", args, 5*time.Second, b.gossipAfter(uint64(menu.NPC), asked, true), nil)
			}
			return b.refuse("gossip.select", args, "no_such_option", "the open menu has no option with that index")
		})

	e.Func("trainer.list", "(npc_guid) → Request<Trainer>", "what a trainer within ~5 yd teaches you: every spell for your class with its cost, level and whether you can learn it now (available), later (unavailable) or already know it",
		func(npc uint64) map[string]any {
			args := map[string]any{"npc_guid": gs(npc)}
			if r := b.noNPC("trainer.list", args, &npc); r != nil {
				return r
			}
			if r := b.noRole("trainer.list", args, npc, roleTrainer, "a trainer"); r != nil {
				return r
			}
			asked := time.Now()
			w.SendPacketRaw(cmsgTrainerList, binary.LittleEndian.AppendUint64(nil, npc))
			return b.request("trainer.list", args, 5*time.Second, func() *script.Outcome {
				s.mu.Lock()
				defer s.mu.Unlock()
				if s.trainAt[npc].After(asked) {
					return script.Ok(s.trainers[npc])
				}
				return nil
			}, nil)
		})
}

var genderToken = regexp.MustCompile(`\$[gG]\s*([^:;]*):([^;]*);`)

// gameText fills in the tokens of NPC and quest text as a real client does: $N your name, $C class, $R race,
// $G male:female; and $B a line break.
func (b *Bot) gameText(t string) string {
	if !strings.Contains(t, "$") {
		return t
	}
	w := b.world
	var class, race string
	female := false
	if o := w.GetObject(w.CharGUID()); o != nil {
		b0 := o.Value(fieldUnitBytes0)
		race, class = strings.ReplaceAll(name(raceNames, uint8(b0&0xFF)), "_", " "), strings.ReplaceAll(name(classNames, uint8(b0>>8&0xFF)), "_", " ")
		female = b0>>16&0xFF == 1
	}
	t = genderToken.ReplaceAllStringFunc(t, func(m string) string {
		p := genderToken.FindStringSubmatch(m)
		if female {
			return p[2]
		}
		return p[1]
	})
	return strings.NewReplacer("$B", "\n", "$b", "\n", "$N", b.unitName(w.CharGUID()), "$n", b.unitName(w.CharGUID()),
		"$C", class, "$c", class, "$R", race, "$r", race).Replace(t)
}
