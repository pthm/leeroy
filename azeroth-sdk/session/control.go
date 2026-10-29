package session

// Agent control surface: wires the Lua API together (setupControl) and handles the chat, group and system-message
// side of the protocol: player names, chat events, server errors as "error" events, groups, and raw packets
// (client.*). The daemon (cmd/azsdk) serves it over JSON-RPC.

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/pthm/wowbench/azeroth-sdk/client"
	"github.com/pthm/wowbench/azeroth-sdk/script"
	"github.com/pthm/wowbench/azeroth-sdk/types"
)

const (
	smsgMessageChat   = 0x0096
	smsgGMMessageChat = 0x03B3
	smsgNameQueryResp = 0x0051
	smsgNotification  = 0x01CB
	smsgInvChangeFail = 0x0112
	smsgPartyResult   = 0x007F
	cmsgMessageChat   = 0x0095
	cmsgJoinChannel   = 0x0097
	packetsCap        = 1000 // ponytail: drop-oldest cap for client.packets()
)

// ChatMessage is a decoded SMSG_MESSAGECHAT.
type ChatMessage struct {
	Type    uint8
	From    string
	GUID    uint64
	Channel string
	Text    string
}

type RawPacket struct {
	Time   int64  `json:"time_ms"`
	Opcode uint16 `json:"opcode"`
	Hex    string `json:"hex"`
}

type controlState struct {
	mu        sync.Mutex
	packets   []RawPacket
	watched   map[uint16]bool
	names     map[uint64]string
	classes   map[uint64]uint8 // from name queries
	members   map[uint64]*memberStats
	invitedBy string // pending group invite
}

// memberStats: a party member as SMSG_PARTY_MEMBER_STATS(_FULL) describes them (sent even when out of sight)
type memberStats struct {
	status                 uint16 // 1 online, 4 dead, 8 ghost
	hp, maxHP              uint32
	powerType              uint8
	power, maxPower, level uint16
	zone                   uint16
	x, y                   int16
	auras                  map[uint8][2]uint32 // slot -> spell, flags (GROUP_UPDATE_FLAG_AURAS)
	seen                   bool
}

const (
	smsgPartyMemberStats     = 0x07E
	smsgPartyMemberStatsFull = 0x2F2
)

// parsePartyStats reads the fields up to the position (auras and pet data after it aren't needed).
func (c *controlState) parsePartyStats(full bool, data []byte) {
	r := bytes.NewReader(data)
	if full {
		r.ReadByte()
	}
	guid := readPackedGUID(r)
	var mask uint32
	binary.Read(r, binary.LittleEndian, &mask)
	m := c.members[guid]
	if m == nil {
		m = &memberStats{}
		c.members[guid] = m
	}
	m.seen = true
	rd := func(v any) { binary.Read(r, binary.LittleEndian, v) }
	if mask&0x1 != 0 {
		rd(&m.status)
	}
	if mask&0x2 != 0 {
		rd(&m.hp)
	}
	if mask&0x4 != 0 {
		rd(&m.maxHP)
	}
	if mask&0x8 != 0 {
		rd(&m.powerType)
	}
	if mask&0x10 != 0 {
		rd(&m.power)
	}
	if mask&0x20 != 0 {
		rd(&m.maxPower)
	}
	if mask&0x40 != 0 {
		rd(&m.level)
	}
	if mask&0x80 != 0 {
		rd(&m.zone)
	}
	if mask&0x100 != 0 {
		rd(&m.x)
		rd(&m.y)
	}
	if mask&0x200 != 0 { // u64 slot mask, then per slot: spell u32, flags u8
		var am uint64
		rd(&am)
		if m.auras == nil {
			m.auras = map[uint8][2]uint32{}
		}
		for i := uint8(0); i < 64; i++ {
			if am&(1<<i) == 0 {
				continue
			}
			var spell uint32
			var flags uint8
			rd(&spell)
			rd(&flags)
			if spell == 0 {
				delete(m.auras, i)
			} else {
				m.auras[i] = [2]uint32{spell, uint32(flags)}
			}
		}
	}
}

//go:embed act.lua
var actLua string // the built-in act library and me.status() (Lua, on top of the API)

func (b *Bot) setupControl() {
	c := &controlState{watched: map[uint16]bool{}, names: map[uint64]string{}, classes: map[uint64]uint8{}, members: map[uint64]*memberStats{}}
	w := b.world
	w.AddPacketHook(func(op uint16, data []byte) { c.onPacket(b, op, data) })

	// player names: the client only knows names it asked for
	asked := map[uint64]time.Time{}
	b.playerName = func(g uint64) string {
		c.mu.Lock()
		defer c.mu.Unlock()
		if n := c.names[g]; n != "" {
			return n
		}
		if time.Since(asked[g]) > 3*time.Second { // SMSG_NAME_QUERY_RESPONSE fills c.names
			asked[g] = time.Now()
			go w.NameQuery(g)
		}
		return ""
	}
	b.notify = func(text string) { b.emit("notice", types.Notice{Message: text}) }
	b.setupCoreAPI()
	b.setupAgentAPI()
	b.setupItems()
	b.setupCombatLog()
	b.setupTalents()
	b.setupTrade()
	b.setupTravel()
	b.setupFights()
	b.setupAuras()
	b.watchAreaTriggers()
	b.watchEvents()
	b.startRecording()
	e := b.lua

	// ---------- group ----------
	e.Func("group.info", "() → Group?", "your party: who leads it, and each member's class, level, health, power, state and where they are (the others, not you); nil when you're solo",
		func() any {
			st := w.GroupState()
			if !st.InGroup {
				return nil
			}
			me := w.CharGUID()
			g := types.Group{Members: []types.GroupMember{}, MeLeader: st.LeaderGUID == me}
			if g.MeLeader {
				g.Leader = b.config.CharacterName
			}
			mx, my, mz, _, mapID := w.Position()
			names := areaNames()
			for _, m := range st.Members {
				gm := types.GroupMember{Name: m.Name, GUID: types.GUID(m.GUID), Leader: m.GUID == st.LeaderGUID, Online: m.Online != 0, Alive: true}
				if gm.Leader {
					g.Leader = m.Name
				}
				c.mu.Lock()
				ms, cls := c.members[m.GUID], c.classes[m.GUID]
				var stats memberStats
				if ms != nil {
					stats = *ms
					stats.auras = map[uint8][2]uint32{}
					for k, v := range ms.auras {
						stats.auras[k] = v
					}
				}
				c.mu.Unlock()
				if cls == 0 && b.playerName != nil {
					b.playerName(m.GUID) // asks the server (name, race, class) if we haven't yet
				}
				if stats.seen {
					for _, a := range stats.auras {
						gm.Auras = append(gm.Auras, types.Aura{Spell: a[0], Name: spellName(a[0]), Harmful: a[1]&0x80 != 0, Stacks: 1})
					}
					gm.Level, gm.Online = uint32(stats.level), stats.status&1 != 0
					gm.Alive, gm.Ghost = stats.status&0xC == 0, stats.status&8 != 0
					gm.Health = types.Resource{Current: stats.hp, Max: stats.maxHP}
					gm.Power = types.Resource{Current: uint32(stats.power), Max: uint32(stats.maxPower), Type: name(powerNames, stats.powerType)}
					gm.Zone = names[uint32(stats.zone)]
					if stats.x != 0 || stats.y != 0 {
						gm.Position = &types.Position{X: float64(stats.x), Y: float64(stats.y), Map: mapID}
						d := math.Round(math.Hypot(float64(stats.x)-float64(mx), float64(stats.y)-float64(my)))
						gm.Distance = &d
					}
				}
				if o := w.GetObject(m.GUID); o != nil { // in sight: exact
					u := b.worldObjToUnitInfo(o)
					gm.InSight, gm.Level, gm.Alive = true, u.Level, u.IsAlive
					gm.Health = types.Resource{Current: u.Health, Max: u.MaxHealth}
					gm.InCombat, gm.Target = u.Flags&0x80000 != 0, types.GUID(u.Target)
					gm.Position = &types.Position{X: round1(u.PosX), Y: round1(u.PosY), Z: round1(u.PosZ), Map: mapID}
					d := math.Round(math.Sqrt(float64((u.PosX-mx)*(u.PosX-mx)+(u.PosY-my)*(u.PosY-my)+(u.PosZ-mz)*(u.PosZ-mz)))*10) / 10
					gm.Distance = &d
					b0 := o.Value(fieldUnitBytes0)
					cls = uint8(b0 >> 8 & 0xFF)
					gm.Auras = b.aurasOf(m.GUID)
					pt := uint16(b0 >> 24 & 0xFF)
					gm.Power = types.Resource{Current: o.Value(client.UnitFieldPower1 + pt), Max: o.Value(client.UnitFieldMaxPower1 + pt), Type: name(powerNames, uint8(pt))}
				}
				if cls != 0 {
					gm.Class = name(classNames, cls)
				}
				g.Members = append(g.Members, gm)
			}
			return g
		})
	e.Func("group.invite", "(name) → request", "invite a player (by exact character name) to your group: ok once the invite is sent; they answer with group.accept", func(name string) map[string]any {
		args := map[string]any{"name": name}
		if err := w.GroupInvite(name); err != nil {
			return b.refuse("group.invite", args, "failed", err.Error())
		}
		start := time.Now()
		return b.request("group.invite", args, 5*time.Second, func() *script.Outcome {
			if time.Since(start) > 1500*time.Millisecond { // the server only answers failures
				return script.Ok(nil)
			}
			return nil
		}, func(ev script.Event) *script.Outcome { return failOn(ev, "group") })
	})
	e.Func("group.accept", "() → request", "accept a pending invite (party_invite event)", func() map[string]any {
		if err := w.GroupAccept(); err != nil {
			return b.refuse("group.accept", nil, "failed", err.Error())
		}
		return b.request("group.accept", nil, 5*time.Second, func() *script.Outcome {
			if w.InGroup() {
				return script.Ok(nil)
			}
			return nil
		}, nil)
	})
	e.Func("group.decline", "() → request", "decline a pending invite", func() map[string]any {
		c.mu.Lock()
		c.invitedBy = ""
		c.mu.Unlock()
		return b.sent("group.decline", nil, w.GroupDecline())
	})
	e.Func("group.leave", "() → request", "leave your group", func() map[string]any {
		if !w.InGroup() {
			return b.refuse("group.leave", nil, "not_in_group", "you aren't in a group")
		}
		w.GroupLeave()
		return b.request("group.leave", nil, 5*time.Second, func() *script.Outcome {
			if !w.InGroup() {
				return script.Ok(nil)
			}
			return nil
		}, nil)
	})
	e.Func("group.invited_by", "() → string?", "who invited you, nil if no invite is pending", func() any {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.invitedBy == "" {
			return nil
		}
		return c.invitedBy
	})
	w.AddGroupInviteHook(func(name string, _ bool) {
		c.mu.Lock()
		c.invitedBy = name
		c.mu.Unlock()
		b.emit("party_invite", types.PartyInvite{From: name})
	})

	// ---------- client: the raw protocol ----------
	e.Func("client.watch", "(opcode [, on = true])", "capture a server opcode for client.packets()", func(op uint16, on any) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.watched[op] = on != false
	})
	e.Func("client.packets", "() → {{opcode, hex, time_ms}}", "drain the captured packets", func() []RawPacket {
		c.mu.Lock()
		defer c.mu.Unlock()
		out := append([]RawPacket{}, c.packets...)
		c.packets = nil
		return out
	})
	e.Func("client.send", "(opcode, hex)", "send a client packet (the header and encryption are added)", func(op uint16, hexBody string) error {
		body, err := hex.DecodeString(strings.ReplaceAll(hexBody, " ", ""))
		if err != nil {
			return err
		}
		return w.SendPacketRaw(op, body)
	})
	if err := e.DoString(actLua); err != nil {
		b.log("act.lua: %v", err)
	}
}

func (c *controlState) onPacket(b *Bot, op uint16, data []byte) {
	w := b.world
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.watched[op] {
		if len(c.packets) >= packetsCap {
			c.packets = c.packets[1:]
		}
		c.packets = append(c.packets, RawPacket{time.Now().UnixMilli(), op, hex.EncodeToString(data)})
	}
	fail := func(op, code, text string) { b.emit("error", types.ErrorEvent{Op: op, Code: code, Message: text}) }
	switch op {
	case smsgInvChangeFail: // bag errors, e.g. a loot or quest reward that doesn't fit
		if len(data) > 0 && data[0] != 0 {
			if msg, ok := equipErrors[data[0]]; ok {
				fail("items", slug(msg), msg)
			} else {
				fail("items", fmt.Sprintf("inventory_%d", data[0]), fmt.Sprintf("inventory error %d", data[0]))
			}
		}
	case 0x1A5: // SMSG_BUY_FAILED: vendor, item, reason
		if len(data) >= 13 {
			code := map[byte]string{0: "not_sold_here", 2: "not_enough_money", 5: "out_of_range", 7: "sold_out", 8: "cant_carry_more"}[data[12]]
			fail("vendor.buy", code, "buy failed: "+map[byte]string{0: "vendor doesn't have that item", 2: "not enough money", 5: "too far from the vendor",
				7: "sold out", 8: "you can't carry more of that"}[data[12]])
		}
	case 0x1A1: // SMSG_SELL_ITEM: vendor, item, error (0 = sold)
		if len(data) >= 17 && data[16] != 0 {
			code := map[byte]string{1: "item_not_found", 2: "wont_buy", 3: "vendor_not_found", 4: "not_yours"}[data[16]]
			fail("vendor.sell", code, "sell failed: "+map[byte]string{1: "item not found", 2: "the vendor won't buy that", 3: "vendor not found",
				4: "you don't own that"}[data[16]])
		}
	case 0x1B4: // SMSG_TRAINER_BUY_FAILED: trainer, spell, reason
		if len(data) >= 16 {
			spell := binary.LittleEndian.Uint32(data[8:])
			reason := binary.LittleEndian.Uint32(data[12:])
			fail("trainer.learn", map[uint32]string{0: "unavailable", 1: "not_enough_money", 2: "skill_too_low"}[reason],
				fmt.Sprintf("can't learn spell %d: %s", spell, map[uint32]string{0: "not available to you (level, class or already known)",
					1: "not enough money", 2: "skill too low"}[reason]))
		}
	case 0x1B3: // SMSG_TRAINER_BUY_SUCCEEDED: trainer, spell
		if len(data) >= 12 {
			if spell := binary.LittleEndian.Uint32(data[8:]); spell != 3286 { // 3286: an innkeeper's Bind, not a spell learned
				b.emit("learned", types.Learned{Spell: spell, Name: spellName(spell)})
			}
		}
	case smsgPartyResult: // operation, member name, result
		r := bytes.NewReader(data)
		var op uint32
		binary.Read(r, binary.LittleEndian, &op)
		name := readCString(r)
		var res uint32
		binary.Read(r, binary.LittleEndian, &res)
		if res != 0 {
			fail("group", slug(partyErrors[res]), fmt.Sprintf("group command failed for %q: %s", name, partyErrors[res]))
		}
	case 0x0077, 0x007D: // SMSG_GROUP_UNINVITE, SMSG_GROUP_LIST: no invite pending any more
		c.invitedBy = ""
	case smsgNotification: // server feedback like "Inventory is full"
		b.emit("notice", types.Notice{Message: readCString(bytes.NewReader(data))})
	case smsgNameQueryResp:
		r := bytes.NewReader(data)
		guid := readPackedGUID(r)
		if found, _ := r.ReadByte(); found == 0 {
			c.names[guid] = readCString(r)
			readCString(r) // realm
			r.ReadByte()   // race
			r.ReadByte()   // gender
			if cls, err := r.ReadByte(); err == nil {
				c.classes[guid] = cls
			}
		}
	case smsgPartyMemberStats, smsgPartyMemberStatsFull:
		c.parsePartyStats(op == smsgPartyMemberStatsFull, data)
	case smsgMessageChat, smsgGMMessageChat:
		if m, guid, ok := parseChat(op, data); ok && (guid != w.CharGUID() || m.Type == 8 || m.Type == 9) {
			if m.From == "" && guid != 0 {
				m.From = c.names[guid]
				if m.From == "" {
					go w.NameQuery(guid)
				}
			}
			c.chatEvent(b, m, guid)
		}
	}
}

// chatEvent emits a "chat" event. A player's name may need a name query first: the event waits for it (up to a
// second) on its own goroutine.
func (c *controlState) chatEvent(b *Bot, m ChatMessage, guid uint64) {
	ev := func(from string) {
		b.emit("chat", types.Chat{Kind: chatKind(m.Type), From: from, GUID: types.GUID(guid), Channel: m.Channel, Text: m.Text})
	}
	if m.From != "" || guid == 0 || guid>>48 != 0 {
		ev(m.From)
		return
	}
	go func() {
		for i := 0; i < 10; i++ {
			c.mu.Lock()
			n := c.names[guid]
			c.mu.Unlock()
			if n != "" {
				ev(n)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		ev("")
	}()
}

// chatLang: AC rejects LANG_UNIVERSAL from players and Horde can't speak Common.
func (b *Bot) chatLang() uint32 {
	switch b.world.CharRace() {
	case 2, 5, 6, 8, 10:
		return client.LangOrcish
	}
	return client.LangCommon
}

// sendChat sends CMSG_MESSAGECHAT; `to` is the whisper target (type 7) or channel name (type 0x11).
func (b *Bot) sendChat(typ uint32, to, msg string) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, typ)
	binary.Write(&buf, binary.LittleEndian, b.chatLang())
	if typ == 7 || typ == 0x11 {
		buf.WriteString(to + "\x00")
	}
	buf.WriteString(msg + "\x00")
	b.world.SendPacketRaw(cmsgMessageChat, buf.Bytes())
}

// parseChat decodes SMSG_MESSAGECHAT (layout per AzerothCore ChatHandler::BuildChatPacket).
func parseChat(op uint16, data []byte) (m ChatMessage, sender uint64, ok bool) {
	defer func() { ok = ok && recover() == nil }()
	r := bytes.NewReader(data)
	var lang, flags uint32
	var receiver uint64
	m.Type, _ = r.ReadByte()
	binary.Read(r, binary.LittleEndian, &lang)
	binary.Read(r, binary.LittleEndian, &sender)
	binary.Read(r, binary.LittleEndian, &flags)
	monster := monsterChat(m.Type)
	if monster || op == smsgGMMessageChat {
		m.From = readSized(r)
	}
	if m.Type == 0x11 {
		m.Channel = readCString(r)
	}
	binary.Read(r, binary.LittleEndian, &receiver)
	if monster && receiver != 0 && receiver>>48 != 0 && receiver>>48 != 0xF140 {
		readSized(r) // non-player receiver name
	}
	m.Text = readSized(r)
	if monster {
		m.Text = strings.ReplaceAll(m.Text, "%s", m.From) // emotes: "%s attempts to run away in fear!"
	}
	m.GUID = sender
	return m, sender, true
}

func monsterChat(t uint8) bool {
	return (t >= 0x0C && t <= 0x10) || t == 0x29 || t == 0x2A || t == 0x2F
}

func readSized(r *bytes.Reader) string {
	var n uint32
	binary.Read(r, binary.LittleEndian, &n)
	b := make([]byte, n)
	r.Read(b)
	return strings.TrimRight(string(b), "\x00")
}

func readCString(r *bytes.Reader) string {
	var sb strings.Builder
	for {
		c, err := r.ReadByte()
		if err != nil || c == 0 {
			return sb.String()
		}
		sb.WriteByte(c)
	}
}

func readPackedGUID(r *bytes.Reader) uint64 {
	mask, _ := r.ReadByte()
	var g uint64
	for i := 0; i < 8; i++ {
		if mask&(1<<i) != 0 {
			b, _ := r.ReadByte()
			g |= uint64(b) << (8 * i)
		}
	}
	return g
}

var equipErrors = map[byte]string{4: "Bag is full", 17: "You can't carry any more of those", 22: "Slot is empty",
	23: "Item not found", 25: "Out of range", 29: "Not enough money", 38: "You are dead", 39: "You can't do that right now",
	49: "Already looted", 50: "Inventory is full", 52: "Item is sold out", 53: "Bag is full", 56: "Bag is full", 57: "Item is sold out"}

var partyErrors = map[uint32]string{1: "no player by that name online", 2: "not in your group", 4: "group is full",
	5: "already in a group", 6: "you aren't in a group", 7: "you aren't the leader", 8: "wrong faction", 9: "they're ignoring you"}
