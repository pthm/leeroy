package main

// The feed: the latest events up to the playhead, filtered by category and narrowed to the selected player or group.

import "html/template"

// FeedRow is one line of the feed.
type FeedRow struct {
	T     float64 // unix ms, for seeking
	When  string
	Color string
	Line  template.HTML
}

// focus is whose events to show: the selected player, the selected group's members at t, or everyone (nil).
func (s *Store) focus(q Query) []string {
	if q.Sel != "" {
		return []string{q.Sel}
	}
	if q.Grp == 0 {
		return nil
	}
	groups, _ := s.GroupsAt(q.T)
	for _, g := range groups {
		if g.ID == q.Grp {
			var out []string
			for _, p := range g.Members {
				out = append(out, p.Name)
			}
			return out
		}
	}
	return []string{} // the group has broken up: nobody
}

func (s *Store) inFocus(e Ev, f []string) bool {
	if f == nil {
		return true
	}
	for _, n := range f {
		if S(e, "ev") == "chat" && s.Hears(e, n) || S(e, "ev") != "chat" && Involves(e, n) {
			return true
		}
	}
	return false
}

// Feed is the last 200 shown events at or before q.T, oldest first.
func (s *Store) Feed(q Query) map[string]any {
	f := s.focus(q)
	var rows []FeedRow
	for i := uptoEv(s.feed, q.T) - 1; i >= 0 && len(rows) < 200; i-- {
		e := s.feed[i]
		cat, color := feedCat(S(e, "ev"))
		if !q.Cats[cat] || !s.inFocus(e, f) {
			continue
		}
		rows = append(rows, FeedRow{T: N(e, "t"), When: Fmt(N(e, "t") - s.Start), Color: color, Line: s.Line(e, q.Sel)})
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return map[string]any{"Rows": rows, "Only": s.only(q, f)}
}

// only is the "only these players · everyone" chip when a player or group is selected.
func (s *Store) only(q Query, f []string) template.HTML {
	if f == nil {
		return ""
	}
	who := ""
	for i, n := range f {
		if i > 0 {
			who += ", "
		}
		who += s.Who(n)
	}
	label := "only "
	if q.Sel == "" {
		label = "only group "
	}
	return template.HTML(`<div class="filters"><span class="only">` + label + who + ` <button data-clear>everyone</button></span></div>`)
}
