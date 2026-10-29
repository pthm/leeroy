package metrics

import "math"

// The score's windows, in hours.
const (
	RateFloorH  = 1.0 / 6 // rate: the first 10 minutes count as 10, so a quick early quest doesn't read as a huge rate
	RecentH     = 1.0 / 6 // recent_rate: over the last 10 minutes
	BestWindowH = 0.25    // best_window: the best rate over any 15-minute window
)

// Rates of a progress curve at its latest point, in progress per hour.
//
//	Rate        value per hour since the start (at least RateFloorH)
//	Recent      value per hour over the last RecentH
//	BestWindow  the best rate over any BestWindowH window so far
//	Pace        the time-average of value (area under the curve / time)
//	Score       (Rate + BestWindow) / 2: sustained progress counts, and so does finding a better strategy: time spent
//	            trying one lowers the rate but not the best window
type Rates struct {
	Rate, Recent, BestWindow, Pace, Score float64
}

type point struct{ h, v float64 }

// Curve is a run's progress over time, sampled as it goes (the judge's 15 s polls).
type Curve struct {
	pts  []point
	area float64 // integral of value dt, in value-hours
	best float64
}

// Add appends a sample (h hours since the start, value v) and returns the rates at it. A resumed run replays its
// earlier samples through Add first, so the curve, the area and the best window carry on.
func (c *Curve) Add(h, v float64) Rates {
	if n := len(c.pts); n > 0 {
		p := c.pts[n-1]
		c.area += (p.v + v) / 2 * (h - p.h)
	}
	c.pts = append(c.pts, point{h, v})
	c.best = math.Max(c.best, c.windowRate(len(c.pts)-1))
	ago := c.pts[0] // ~RecentH back
	for _, p := range c.pts {
		if p.h >= h-RecentH {
			ago = p
			break
		}
	}
	r := Rates{Rate: v / math.Max(h, RateFloorH), BestWindow: c.best, Pace: v}
	if h > 0 {
		r.Pace = c.area / h
	}
	if h-ago.h > 0 {
		r.Recent = (v - ago.v) / (h - ago.h)
	}
	r.Score = (r.Rate + r.BestWindow) / 2
	return r
}

// windowRate: the rate over the BestWindowH ending at point k (since the start while the run is shorter).
func (c *Curve) windowRate(k int) float64 {
	hk, vk := c.pts[k].h, c.pts[k].v
	if hk < BestWindowH {
		return vk / math.Max(hk, RateFloorH)
	}
	j := k
	for j > 0 && c.pts[j-1].h >= hk-BestWindowH {
		j--
	}
	return (vk - c.pts[j].v) / math.Max(hk-c.pts[j].h, 1.0/60)
}

// PerHour is amount per hour over seconds, counting at least floor seconds (so a short period doesn't read as a
// huge rate), rounded.
func PerHour(amount, seconds, floor float64) float64 {
	return math.Round(amount / math.Max(seconds, floor) * 3600)
}
