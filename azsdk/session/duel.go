package session

// Duels: challenge a player (the Duel spell), accept or decline a challenge, yield. The server plants a flag between
// the two; a duel ends when one side is beaten to 1 health or yields (won), or stays over 50 yd from the flag for
// 10 s (fled). Nothing is restored afterwards: health, mana and cooldowns are as the duel left them.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/pthm/leeroy/azsdk/script"
	"github.com/pthm/leeroy/azsdk/types"
)

const (
	smsgDuelRequested   = 0x167 // flag guid, challenger guid
	smsgDuelOutOfBounds = 0x168
	smsgDuelInBounds    = 0x169
	smsgDuelComplete    = 0x16A // uint8: 1 decided (SMSG_DUEL_WINNER follows), 0 broken off
	smsgDuelWinner      = 0x16B // uint8 0 won / 1 fled, winner name, loser name (to everyone nearby)
	cmsgDuelAccepted    = 0x16C // flag guid
	cmsgDuelCancelled   = 0x16D // flag guid: decline or withdraw a challenge, or yield a duel under way
	smsgDuelCountdown   = 0x2B7 // uint32 ms
	spellDuel           = 7266
)

type duelState struct {
	mu      sync.Mutex
	d       types.Duel
	flag    uint64
	startAt time.Time // countdown: when the fighting starts
}

// snapshot is the duel now (s.mu held): countdown turns to fighting once its 3 s are up.
func (s *duelState) snapshot() types.Duel {
	if s.d.State == "countdown" && !s.startAt.IsZero() && time.Now().After(s.startAt) {
		s.d.State = "fighting"
	}
	return s.d
}

func (b *Bot) setupDuel() {
	s := &duelState{d: types.Duel{State: "none"}}
	w := b.world
	me := func() string { return b.config.CharacterName }
	w.AddPacketHook(func(op uint16, data []byte) {
		r := bytes.NewReader(data)
		s.mu.Lock()
		var ev string
		switch op {
		case smsgDuelRequested:
			var flag, from uint64
			_ = binary.Read(r, binary.LittleEndian, &flag)
			_ = binary.Read(r, binary.LittleEndian, &from)
			opp := s.d.Opponent // our own challenge: who we challenged
			challenger := b.unitName(from)
			if from != w.CharGUID() {
				opp = challenger
			}
			s.flag, s.startAt = flag, time.Time{}
			s.d = types.Duel{State: "requested", Challenger: challenger, Opponent: opp}
			ev = "duel_requested"
		case smsgDuelCountdown:
			var ms uint32
			_ = binary.Read(r, binary.LittleEndian, &ms)
			s.d.State, s.startAt = "countdown", time.Now().Add(time.Duration(ms)*time.Millisecond)
			ev = "duel_countdown"
		case smsgDuelOutOfBounds:
			s.d.OutOfBounds, ev = true, "duel_out_of_bounds"
		case smsgDuelInBounds:
			s.d.OutOfBounds, ev = false, "duel_in_bounds"
		case smsgDuelComplete:
			if c, _ := r.ReadByte(); c == 0 && s.d.State != "none" && s.d.State != "ended" {
				s.d.State, s.d.Result, s.d.OutOfBounds, s.flag = "ended", "cancelled", false, 0
				ev = "duel_ended"
			}
		case smsgDuelWinner:
			how, _ := r.ReadByte()
			winner, loser := readCString(r), readCString(r)
			if winner != me() && loser != me() { // someone else's duel nearby
				break
			}
			s.d = types.Duel{
				State: "ended", Challenger: s.d.Challenger, Result: map[byte]string{0: "beaten", 1: "fled"}[how],
				Winner: winner, Loser: loser, Won: winner == me(),
			}
			if winner == me() {
				s.d.Opponent = loser
			} else {
				s.d.Opponent = winner
			}
			s.flag = 0
			ev = "duel_ended"
		}
		d := s.d
		s.mu.Unlock()
		if ev != "" {
			b.emit(ev, d)
		}
	})

	e := b.lua
	until := func(op string, args map[string]any, timeout time.Duration, ok ...string) map[string]any {
		return b.request(op, args, timeout, func() *script.Outcome {
			s.mu.Lock()
			defer s.mu.Unlock()
			d := s.snapshot()
			for _, st := range ok {
				if d.State == st {
					return script.Ok(d)
				}
			}
			return nil
		}, func(ev script.Event) *script.Outcome {
			if c, ok := ev.Data.(types.Cast); ok && ev.Type == "cast_failed" && c.Spell == spellDuel {
				return script.Fail(c.Code, "the server refused the challenge: "+c.Code, nil)
			}
			if d, ok := ev.Data.(types.Duel); ok && ev.Type == "duel_ended" && d.Result == "cancelled" {
				return script.Fail("cancelled", "the duel was declined or broken off", d)
			}
			return nil
		})
	}
	e.Func("duel.challenge", "(player) → Request<Duel>", "challenge a player within ~10 yd (name or guid) to a duel: ok once the challenge is out (state requested); they answer with duel.accept or duel.decline", func(who string) map[string]any {
		args := map[string]any{"player": who}
		s.mu.Lock()
		busy := s.snapshot().State
		s.mu.Unlock()
		if busy == "requested" || busy == "countdown" || busy == "fighting" {
			return b.refuse("duel.challenge", args, "busy", "already in a duel or a challenge ("+busy+")")
		}
		var g uint64
		name := who
		if _, err := fmt.Sscan(who, &g); err != nil || g == 0 {
			g = 0
			for _, p := range b.GetNearbyPlayers(60) {
				if p.Name == who {
					g = p.GUID
				}
			}
		} else {
			name = b.unitName(g)
		}
		if g == 0 {
			return b.refuse("duel.challenge", args, "player_not_found", fmt.Sprintf("no player %q within 60 yards", who))
		}
		s.mu.Lock()
		s.d = types.Duel{State: "none", Opponent: name}
		s.mu.Unlock()
		if err := b.world.CastSpell(spellDuel, g); err != nil {
			return b.refuse("duel.challenge", args, "failed", err.Error())
		}
		return until("duel.challenge", args, 5*time.Second, "requested")
	})
	answer := func(op string, accept bool) map[string]any {
		s.mu.Lock()
		d, flag := s.snapshot(), s.flag
		s.mu.Unlock()
		if d.State != "requested" || flag == 0 {
			return b.refuse(op, nil, "no_challenge", "no duel challenge is pending (duel_requested event)")
		}
		if accept && d.Challenger == me() {
			return b.refuse(op, nil, "own_challenge", "you made this challenge: your opponent accepts it")
		}
		body := make([]byte, 8)
		binary.LittleEndian.PutUint64(body, flag)
		code := uint16(cmsgDuelCancelled)
		if accept {
			code = cmsgDuelAccepted
		}
		if err := w.SendPacketRaw(code, body); err != nil {
			return b.refuse(op, nil, "failed", err.Error())
		}
		if accept {
			return until(op, nil, 5*time.Second, "countdown", "fighting")
		}
		return b.request(op, nil, 5*time.Second, func() *script.Outcome {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.d.State == "ended" || s.d.State == "none" {
				return script.Ok(s.d)
			}
			return nil
		}, nil)
	}
	e.Func("duel.accept", "() → Request<Duel>", "accept a pending challenge (duel_requested): ok at the countdown; the duel starts 3 s later", func() map[string]any {
		return answer("duel.accept", true)
	})
	e.Func("duel.decline", "() → Request<Duel>", "decline a challenge to you, or withdraw your own", func() map[string]any {
		return answer("duel.decline", false)
	})
	e.Func("duel.yield", "() → Request<Duel>", "give up the duel under way: your opponent wins", func() map[string]any {
		s.mu.Lock()
		d, flag := s.snapshot(), s.flag
		s.mu.Unlock()
		if d.State != "fighting" && d.State != "countdown" {
			return b.refuse("duel.yield", nil, "not_duelling", "no duel is under way")
		}
		body := make([]byte, 8)
		binary.LittleEndian.PutUint64(body, flag)
		if err := w.SendPacketRaw(cmsgDuelCancelled, body); err != nil {
			return b.refuse("duel.yield", nil, "failed", err.Error())
		}
		return until("duel.yield", nil, 5*time.Second, "ended")
	})
	e.Func("duel.state", "() → Duel", "the duel now: none, requested, countdown, fighting or ended (with the result)", func() types.Duel {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.snapshot()
	})
}
