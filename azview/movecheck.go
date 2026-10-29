// azview movecheck [recorder.jsonl] [--chars A,B] [--from ms]
//
// Checks recorded movement against the terrain: speed (median per 1 s sample; sustained over 5 s), the steepest
// slope crossed on each step, and whether the player was under water (a real client swims at the surface). One JSON
// line per character.
// ponytail: slopes and water are measured against outdoor terrain, so steps on building floors and city streets
// (Stormwind, inns) can show as steep climbs; check those against the route.
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/pthm/leeroy/azworld/world"
)

var maxSlope = math.Tan(50 * math.Pi / 180) // AzerothCore navmesh walkable slope (MmapTileRecastConfig)

type report struct {
	Name                   string   `json:"name"`
	Samples                int      `json:"samples"`
	MovingS                int      `json:"moving_s"`
	MedianSpeedAlive       *float64 `json:"median_speed_alive"`
	MedianSpeedGhost       *float64 `json:"median_speed_ghost"`
	FasterThanRun5s        int      `json:"faster_than_run_5s"`
	UphillSteeperThan50deg int      `json:"uphill_steeper_than_50deg"`
	SteepestDeg            float64  `json:"steepest_deg"`
	Examples               []string `json:"examples"`
	UnderwaterS            int      `json:"underwater_s"`
	UnderwaterFasterSwim   int      `json:"underwater_faster_than_swim"`
}

func med(a []float64) *float64 {
	if len(a) == 0 {
		return nil
	}
	slices.Sort(a)
	v := math.Round(a[len(a)>>1]*100) / 100
	return &v
}

func runMovecheck(args []string) {
	file := ServerLog()
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		file = args[0]
	}
	var chars []string
	if v, ok := Flag(args, "chars"); ok {
		chars = strings.Split(v, ",")
	}
	from := 0.0
	if v, ok := Flag(args, "from"); ok {
		from, _ = strconv.ParseFloat(v, 64)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	n, s := N, S
	by, order := map[string][]Ev{}, []string{}
	for _, e := range ParseLines(string(b)) {
		if s(e, "ev") != "pos" || n(e, "t") < from || (chars != nil && !slices.Contains(chars, s(e, "n"))) {
			continue
		}
		if _, ok := by[s(e, "n")]; !ok {
			order = append(order, s(e, "n"))
		}
		by[s(e, "n")] = append(by[s(e, "n")], e)
	}
	height := func(m, x, y float64) float64 { h, _, _ := world.Sample(uint32(m), x, y); return h }
	for _, name := range order {
		track := by[name]
		r := report{Name: name, Examples: []string{}}
		var steepest float64
		var aliveV, ghostV []float64
		for i := 1; i < len(track); i++ {
			a, p := track[i-1], track[i]
			dt := (n(p, "t") - n(a, "t")) / 1000
			if dt <= 0 || dt > 2 || a["m"] != p["m"] {
				continue
			}
			r.Samples++
			d := math.Hypot(n(p, "x")-n(a, "x"), n(p, "y")-n(a, "y"))
			v := d / dt
			_, water, _ := world.Sample(uint32(n(p, "m")), n(p, "x"), n(p, "y"))
			if !math.IsNaN(water) && n(p, "z") < water-1.5 {
				r.UnderwaterS++
				if v > 5.5 {
					r.UnderwaterFasterSwim++
				}
			}
			if d < 1 || v > 40 { // standing, or a teleport (graveyard, spirit healer)
				continue
			}
			r.MovingS++
			ghost := Truthy(p["ghost"])
			if ghost {
				ghostV = append(ghostV, v)
			} else {
				aliveV = append(aliveV, v)
			}
			// sustained speed over ~5 s: single samples alias against the client's heartbeat cadence
			if i >= 5 {
				if w := track[i-5]; n(p, "t")-n(w, "t") <= 6000 && w["ghost"] == p["ghost"] {
					path := 0.0
					for k := i - 4; k <= i; k++ {
						path += math.Hypot(n(track[k], "x")-n(track[k-1], "x"), n(track[k], "y")-n(track[k-1], "y"))
					}
					limit := 7.7 // run 7, ghost 10.5, +10%
					if ghost {
						limit = 11.5
					}
					if path/((n(p, "t")-n(w, "t"))/1000) > limit {
						r.FasterThanRun5s++
					}
				}
			}
			// steepest terrain slope along the step, sampled every 2 yards
			m, worst := n(a, "m"), 0.0
			steps := max(1, int(math.Ceil(d/2)))
			prev := height(m, n(a, "x"), n(a, "y"))
			for k := 1; k <= steps; k++ {
				kf, sf := float64(k), float64(steps)
				h := height(m, n(a, "x")+(n(p, "x")-n(a, "x"))*kf/sf, n(a, "y")+(n(p, "y")-n(a, "y"))*kf/sf)
				if !math.IsNaN(h) && !math.IsNaN(prev) {
					worst = math.Max(worst, (h-prev)/(d/float64(steps))) // uphill only
				}
				prev = h
			}
			steepest = math.Max(steepest, worst)
			if worst > maxSlope {
				r.UphillSteeperThan50deg++
				if len(r.Examples) < 3 {
					r.Examples = append(r.Examples, fmt.Sprintf("(%.0f,%.0f) %.0fdeg", math.Round(n(a, "x")), math.Round(n(a, "y")),
						math.Round(math.Atan(worst)*180/math.Pi)))
				}
			}
		}
		r.MedianSpeedAlive, r.MedianSpeedGhost = med(aliveV), med(ghostV)
		r.SteepestDeg = math.Round(math.Atan(steepest) * 180 / math.Pi)
		out, _ := json.Marshal(r)
		fmt.Println(string(out))
	}
}
