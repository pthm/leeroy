package session

// The stored boarding routines: every boat, zeppelin, elevator and tram that runs on a timetable, where it stops, where
// to wait for it at each stop (the pier or the landing, which leaving it puts you back on), and where to stand aboard
// (in its axes). transports.json is generated (transportgen_test.go) and checked in the game by GM characters riding
// each one at once (testdata/transportgen/ride.py: board at a stop, ride, leave at the next, against the server's .gps).
// Left out: the Icecrown gunships (no stops for passengers), the Northrend turtles (no pier found), lifts that wait to
// be set off (pauseAtTime) and parts that move without pausing.
//
// Boats and the like (gameobject type 15) run on a taxi path (transportpath.go): their stops are its key frames.
// Elevators and trams (type 11) run on TransportAnimation.dbc offsets from where they're spawned, rotated by their
// parent rotation and looping (StaticTransport::RelocateToProgress); their stops are the pauses in it. Both kinds tell
// a client their path progress when it sees them (UPDATEFLAG_TRANSPORT), so their position is known from then on.

import (
	_ "embed"
	"encoding/json"
	"math"
)

//go:embed transports.json
var transportsJSON []byte

type tableTransport struct {
	Entry   uint32       `json:"entry"`
	Name    string       `json:"name"`
	Kind    string       `json:"kind"`               // ship, zeppelin, elevator, tram
	Map     uint32       `json:"map,omitempty"`      // elevators and trams: where it is
	Spawn   [4]float32   `json:"spawn,omitempty"`    // elevators and trams: where it's spawned (x, y, z, o)
	PathRot float32      `json:"path_rot,omitempty"` // elevators and trams: its path's rotation
	Anim    [][4]float32 `json:"anim,omitempty"`     // elevators and trams: (ms, x, y, z) offsets
	Stops   []tableStop  `json:"stops"`
}

type tableStop struct {
	Frame  int        `json:"frame"` // boats: its path's key frame; elevators: -1
	Map    uint32     `json:"map"`
	At     [3]float32 `json:"at"`               // where the transport is while it waits here
	Wait   [3]float32 `json:"wait"`             // where to stand to board it here, and where leaving it here puts you
	Deck   [3]float32 `json:"deck"`             // where to stand aboard, in its axes
	Arrive uint32     `json:"arrive,omitempty"` // elevators: ms into its loop it arrives, and departs
	Depart uint32     `json:"depart,omitempty"`
}

var transportTable = func() []tableTransport {
	var t []tableTransport
	if err := json.Unmarshal(transportsJSON, &t); err != nil {
		panic("transports.json: " + err.Error())
	}
	return t
}()

// period: an elevator's loop, ms
func (tt *tableTransport) period() uint32 {
	if len(tt.Anim) == 0 {
		return 0
	}
	return uint32(tt.Anim[len(tt.Anim)-1][0])
}

// staticAt: where an elevator or tram is at ms into its loop (StaticTransport::RelocateToProgress, without the
// rotation of the model, which doesn't move it).
func (tt *tableTransport) staticAt(ms uint32) [3]float32 {
	a := tt.Anim
	if p := tt.period(); p > 0 {
		ms %= p
	}
	i := 0
	for i+1 < len(a) && uint32(a[i+1][0]) <= ms {
		i++
	}
	off := [3]float64{float64(a[i][1]), float64(a[i][2]), float64(a[i][3])}
	if i+1 < len(a) && a[i+1][0] > a[i][0] {
		f := (float64(ms) - float64(a[i][0])) / float64(a[i+1][0]-a[i][0])
		for k := 0; k < 3; k++ {
			off[k] += f * (float64(a[i+1][k+1]) - off[k])
		}
	}
	s, c := math.Sincos(float64(tt.PathRot))
	x, y := off[0]*c-off[1]*s, off[0]*s+off[1]*c
	return [3]float32{tt.Spawn[0] + float32(x), tt.Spawn[1] + float32(y), tt.Spawn[2] + float32(off[2])}
}

// tableFor: the table's transport for one in view: its entry, and for an elevator (an entry can have several), the
// one spawned nearest it across the ground (they move up and down; a tram's entries are its own).
func tableFor(entry, mapID uint32, x, y float32) *tableTransport {
	var best *tableTransport
	bestD := math.Inf(1)
	for i := range transportTable {
		tt := &transportTable[i]
		if tt.Entry != entry || (len(tt.Anim) > 0 && tt.Map != mapID) {
			continue
		}
		d := math.Hypot(float64(tt.Spawn[0]-x), float64(tt.Spawn[1]-y))
		if len(tt.Anim) == 0 {
			d = 0
		}
		if d < bestD {
			best, bestD = tt, d
		}
	}
	return best
}
