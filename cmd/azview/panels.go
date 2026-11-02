package main

// The panels: HTML fragments for a moment and the page's UI state, rendered on the server and swapped in by htmx.

import (
	"net/http"
	"strconv"
	"strings"
)

// Query is the UI state a panel is rendered for (viewerQuery in viewer.js).
type Query struct {
	T                         float64 // unix ms
	Sel                       string  // the selected player
	Grp                       int     // the selected group (0: none)
	Tab, Sort                 string
	ByGroup                   bool
	Cats                      map[string]bool
	Metric, Range, Chart, Per string
	Width                     int // the pane's width, CSS px: the activity strip's cells are about 8 px
}

func queryOf(r *http.Request) Query {
	g := r.URL.Query().Get
	q := Query{T: num(r, "t"), Sel: g("sel"), Tab: g("tab"), Sort: g("sort"), ByGroup: g("bygroup") == "1",
		Cats: map[string]bool{}, Metric: g("metric"), Range: g("range"), Chart: g("chart"), Per: g("per")}
	q.Grp, _ = strconv.Atoi(g("grp"))
	q.Width, _ = strconv.Atoi(g("w"))
	for _, c := range strings.Split(g("cats"), ",") {
		q.Cats[c] = true
	}
	return q
}

// Panel is the template and data for a panel; the caller holds the read lock.
func (s *Store) Panel(name string, q Query) (string, any) {
	switch name {
	case "roster":
		return "roster.html", s.Roster(q)
	case "pane":
		if q.Tab == "stats" {
			return "stats.html", s.Stats(q)
		}
	}
	return "feed.html", s.Feed(q)
}

// comma is a number with thousands separators.
func comma(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + comma(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// metric is a stat the Stats tab shows; stats.go has how each is counted.
type metric struct{ Key, Label, Group string }

var metrics = []metric{
	{"dmg", "Damage done", "Combat"}, {"taken", "Damage taken", "Combat"}, {"heal", "Healing done", "Combat"},
	{"healed", "Healing taken", "Combat"}, {"kills", "Kills", "Combat"}, {"deaths", "Deaths", "Combat"},
	{"casts", "Casts", "Combat"}, {"combat", "Time in combat", "Combat"}, {"lowhp", "Time below 35% health", "Combat"},
	{"oom", "Time out of mana (<10%)", "Combat"},
	{"score", "Score", "Score"}, {"progress", "Progress", "Score"}, {"pace", "Pace", "Score"}, {"recent", "Rate, last 10 min", "Score"},
	{"xp", "XP gained", "Progress"}, {"quests", "Quests completed", "Progress"}, {"levels", "Levels gained", "Progress"},
	{"level", "Level", "Progress"}, {"loot", "Items looted", "Progress"}, {"money", "Money gained", "Progress"}, {"gold", "Money held", "Progress"},
	{"distance", "Distance travelled", "Activity"}, {"dead", "Time dead", "Activity"}, {"idle", "Time idle", "Activity"},
	{"moving", "Time moving", "Activity"}, {"grouped", "Time grouped", "Activity"},
	{"say", "Agent messages", "Agent"}, {"api", "Eval calls", "Agent"}, {"errors", "Script errors", "Agent"}, {"chat", "Chat messages sent", "Agent"},
}

// metricGroups is the metrics by group, for the metric picker.
func metricGroups() []struct {
	Group string
	Items []metric
} {
	var out []struct {
		Group string
		Items []metric
	}
	for _, m := range metrics {
		if len(out) == 0 || out[len(out)-1].Group != m.Group {
			out = append(out, struct {
				Group string
				Items []metric
			}{Group: m.Group})
		}
		out[len(out)-1].Items = append(out[len(out)-1].Items, m)
	}
	return out
}

var ranges = []struct{ Key, Label string }{
	{"fight", "Current fight"}, {"60", "Last minute"}, {"300", "Last 5 min"}, {"900", "Last 15 min"}, {"3600", "Last hour"}, {"all", "Whole run"},
}
