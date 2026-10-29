// Package metrics is the one definition of how LEEROY measures a run, shared by the judge (the score), its report
// card and the client's own progress events, so a number means the same thing everywhere:
//
//   - rates.go: the score's rates over a progress curve (rate, recent rate, best window, pace, score)
//   - combat.go: totals from one character's event stream (damage dealt and taken, healing, kills, deaths, XP)
//   - coop.go: cooperation across a team's streams (assist share, contested hits)
//   - timeuse.go: how a character's time went, from position samples (fighting, travelling, idle, dead, grouped)
//
// Pure functions over values: no files, no database, so the headless client can use it too.
package metrics
