package session

// Agent control surface: extra Lua bindings (chat inbox, raw packets, events, reflective
// client access). The daemon (cmd/azsdk) serves them over JSON-RPC.

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pthm/wowbench/azeroth-sdk/client"
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
	inboxCap          = 1000 // ponytail: drop-oldest cap; agents that don't drain lose old messages
)

type ChatMessage struct {
	Time    int64  `json:"time_ms"`
	Type    uint8  `json:"type"` // 1 say, 2 party, 6 yell, 7 whisper, 0x11 channel, 0x0C+ monster ...
	From    string `json:"from"`
	GUID    string `json:"guid"`
	Channel string `json:"channel,omitempty"`
	Text    string `json:"text"`
}

type RawPacket struct {
	Time   int64  `json:"time_ms"`
	Opcode uint16 `json:"opcode"`
	Hex    string `json:"hex"`
}

type controlState struct {
	mu        sync.Mutex
	chat      []ChatMessage
	packets   []RawPacket
	watched   map[uint16]bool
	names     map[uint64]string
	unread    int    // player chat from others since the last read_chat (wakes `wow -c`)
	invitedBy string // pending group invite
}

func capAppend[T any](s []T, v T) []T {
	if len(s) >= inboxCap {
		s = s[1:]
	}
	return append(s, v)
}

//go:embed act.lua
var actLua string // the built-in `act` routines and bot.status() (Lua, on top of the bindings below)

func (b *Bot) setupControl() {
	c := &controlState{watched: map[uint16]bool{}, names: map[uint64]string{}}
	w := b.world
	w.AddPacketHook(func(op uint16, data []byte) { c.onPacket(w, op, data) })

	// player names for get_nearby_players/get_unit: the client only knows names it asked for
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
	b.setupAgentAPI()
	b.setupItems()
	b.setupCombatLog()
	b.setupTrade(func(text string) { // a system message in read_chat that also wakes `wow -c`
		c.mu.Lock()
		defer c.mu.Unlock()
		c.chat = capAppend(c.chat, ChatMessage{Time: time.Now().UnixMilli(), GUID: "0", Text: text})
		c.unread++
	})
	b.watchAreaTriggers()
	b.startRecording()
	e := b.lua
	e.RegisterCall("call", w) // bot.call("QuestgiverHello", guid) etc. — any exported WorldClient method
	e.RegisterFunc("read_chat", func() []ChatMessage {
		c.mu.Lock()
		defer c.mu.Unlock()
		out := append([]ChatMessage{}, c.chat...) // [] not nil when empty, so Lua gets a table
		c.chat, c.unread = nil, 0
		for i := range out {
			if out[i].From == "" {
				var g uint64
				fmt.Sscan(out[i].GUID, &g)
				out[i].From = c.names[g]
			}
		}
		return out
	})
	e.RegisterFunc("chat_pending", func() int {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.unread
	})
	e.RegisterFunc("watch", func(op uint16, on bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.watched[op] = on
	})
	e.RegisterFunc("read_packets", func() []RawPacket {
		c.mu.Lock()
		defer c.mu.Unlock()
		out := append([]RawPacket{}, c.packets...)
		c.packets = nil
		return out
	})
	e.RegisterFunc("send_packet", func(op uint16, hexBody string) error {
		body, err := hex.DecodeString(strings.ReplaceAll(hexBody, " ", ""))
		if err != nil {
			return err
		}
		w.SendPacketRaw(op, body)
		return nil
	})
	e.RegisterFunc("whisper", func(to, msg string) { b.sendChat(7, to, msg) })
	e.RegisterFunc("party", func(msg string) { b.sendChat(2, "", msg) })
	// bot.join_channel("General - Elwynn Forest", 1): built-in zone channels need their DBC id
	// (1 General, 2 Trade, 22 LocalDefense); custom channels use id 0 and any name.
	e.RegisterFunc("join_channel", func(name string, id uint32) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, id)
		buf.Write([]byte{0, 0})
		buf.WriteString(name + "\x00\x00") // name, empty password
		w.SendPacketRaw(cmsgJoinChannel, buf.Bytes())
	})
	e.RegisterFunc("channel", func(name, msg string) { b.sendChat(0x11, name, msg) })
	if !b.config.ValidationMode {
		// set_level only overwrites the client's cached level (for GM test setup); for an agent it
		// just makes get_level() lie, e.g. bundled grind.lua's ".level 6" prep on a non-GM account.
		e.RegisterFunc("set_level", func(uint32) {})
	}
	// Groups. An invite arrives in read_chat as a system message (and wakes `wow -c`).
	w.AddGroupInviteHook(func(name string, _ bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.invitedBy = name
		c.chat = capAppend(c.chat, ChatMessage{Time: time.Now().UnixMilli(), From: name, GUID: "0",
			Text: name + " invites you to a group: bot.accept_invite() or bot.decline_invite()"})
		c.unread++
	})
	e.RegisterFunc("invite", func(name string) error { return w.GroupInvite(name) })
	e.RegisterFunc("accept_invite", func() error { return w.GroupAccept() })
	e.RegisterFunc("decline_invite", func() error {
		c.mu.Lock()
		c.invitedBy = ""
		c.mu.Unlock()
		return w.GroupDecline()
	})
	e.RegisterFunc("leave_group", func() error { return w.GroupLeave() })
	e.RegisterFunc("pending_invite", func() any { // inviter name, or nil
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.invitedBy == "" {
			return nil
		}
		return c.invitedBy
	})
	// group() -> nil when solo, else {leader = name, members = {{name, guid, online}...}} (others, not you)
	e.RegisterFunc("group", func() any {
		st := w.GroupState()
		if !st.InGroup {
			return nil
		}
		leader, members := "", []map[string]any{}
		if st.LeaderGUID == w.CharGUID() {
			leader = b.config.CharacterName
		}
		for _, m := range st.Members {
			members = append(members, map[string]any{"name": m.Name, "guid": fmt.Sprint(m.GUID), "online": m.Online != 0})
			if m.GUID == st.LeaderGUID {
				leader = m.Name
			}
		}
		return map[string]any{"leader": leader, "members": members}
	})
	e.RegisterFunc("events", func() []BotEvent { return b.Events() })
	e.RegisterFunc("money", func() uint32 { return w.Money() })
	// bot.get_xp() -> xp, xp needed for next level (PLAYER_XP / PLAYER_NEXT_LEVEL_XP update fields)
	e.RegisterFunc("get_xp", func() (uint32, uint32) {
		if o := w.GetObject(w.CharGUID()); o != nil {
			return o.Value(0x027A), o.Value(0x027B)
		}
		return 0, 0
	})
	if err := e.DoString(actLua); err != nil {
		b.log("act.lua: %v", err)
	}
}

func (c *controlState) onPacket(w *client.WorldClient, op uint16, data []byte) {
	now := time.Now().UnixMilli()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.watched[op] {
		c.packets = capAppend(c.packets, RawPacket{now, op, hex.EncodeToString(data)})
	}
	sys := func(text string) { c.chat = capAppend(c.chat, ChatMessage{Time: now, GUID: "0", Text: text}) }
	switch op {
	case smsgInvChangeFail: // bag errors, e.g. a loot or quest reward that doesn't fit
		if len(data) > 0 && data[0] != 0 {
			if msg, ok := equipErrors[data[0]]; ok {
				sys(msg)
			} else {
				sys(fmt.Sprintf("inventory error %d", data[0]))
			}
		}
	case 0x1A5: // SMSG_BUY_FAILED: vendor, item, reason
		if len(data) >= 13 {
			sys("buy failed: " + map[byte]string{0: "vendor doesn't have that item", 2: "not enough money", 5: "too far from the vendor",
				7: "sold out", 8: "you can't carry more of that"}[data[12]])
		}
	case 0x1A1: // SMSG_SELL_ITEM: vendor, item, error (0 = sold)
		if len(data) >= 17 && data[16] != 0 {
			sys("sell failed: " + map[byte]string{1: "item not found", 2: "the vendor won't buy that", 3: "vendor not found",
				4: "you don't own that"}[data[16]])
		}
	case 0x1B4: // SMSG_TRAINER_BUY_FAILED: trainer, spell, reason
		if len(data) >= 16 {
			spell := binary.LittleEndian.Uint32(data[8:])
			sys(fmt.Sprintf("can't learn spell %d: %s", spell, map[uint32]string{0: "not available to you (level, class or already known)",
				1: "not enough money", 2: "skill too low"}[binary.LittleEndian.Uint32(data[12:])]))
		}
	case 0x1B3: // SMSG_TRAINER_BUY_SUCCEEDED: trainer, spell
		if len(data) >= 12 {
			sys(fmt.Sprintf("learned spell %d", binary.LittleEndian.Uint32(data[8:])))
		}
	case smsgPartyResult: // operation, member name, result
		r := bytes.NewReader(data)
		var op uint32
		binary.Read(r, binary.LittleEndian, &op)
		name := readCString(r)
		var res uint32
		binary.Read(r, binary.LittleEndian, &res)
		if res != 0 {
			sys(fmt.Sprintf("group command failed for %q: %s (%d)", name, partyErrors[res], res))
			c.unread++
		}
	case 0x0077, 0x007D: // SMSG_GROUP_UNINVITE, SMSG_GROUP_LIST: no invite pending any more
		c.invitedBy = ""
	case smsgNotification: // server feedback like "Inventory is full" -> system chat
		c.chat = capAppend(c.chat, ChatMessage{Time: now, Type: 0, From: "", GUID: "0", Text: readCString(bytes.NewReader(data))})
	case smsgNameQueryResp:
		r := bytes.NewReader(data)
		guid := readPackedGUID(r)
		if found, _ := r.ReadByte(); found == 0 {
			c.names[guid] = readCString(r)
		}
	case smsgMessageChat, smsgGMMessageChat:
		if m, guid, ok := parseChat(op, data); ok {
			m.Time = now
			if m.From == "" && guid != 0 {
				if n, ok := c.names[guid]; ok {
					m.From = n
				} else {
					go w.NameQuery(guid) // resolved lazily in read_chat
				}
			}
			c.chat = capAppend(c.chat, m)
			if m.Type != 0 && m.Type != 9 && !monsterChat(m.Type) && guid != w.CharGUID() { // not system, echo, NPC or self
				c.unread++
			}
		}
	}
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
	m.GUID = fmt.Sprint(sender)
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
