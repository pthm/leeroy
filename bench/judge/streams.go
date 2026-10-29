package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pthm/leeroy/azeroth-sdk/metrics"
)

// streams tails every <Char>.events.jsonl in a directory (a team's, or one run's), keeping the decoded events
// cooperation and the report need. Each read picks up where the last one stopped.
type streams struct {
	dir  string
	at   map[string]int64
	tail map[string]string
	evs  map[string][]metrics.Event // character -> events, in file order
}

func newStreams(dir string) *streams {
	return &streams{dir: dir, at: map[string]int64{}, tail: map[string]string{}, evs: map[string][]metrics.Event{}}
}

// keep: the event types the metrics read (the files are mostly casts, results and logs).
var keep = []string{`"type":"damage"`, `"type":"heal"`, `"type":"xp"`, `"type":"kill"`, `"type":"party_kill"`,
	`"type":"death"`, `"type":"task_started"`, `"type":"task_finished"`, `"type":"stall"`, `"type":"party_changed"`}

func (s *streams) read() {
	files, _ := filepath.Glob(filepath.Join(s.dir, "*.events.jsonl"))
	for _, f := range files {
		char := strings.TrimSuffix(filepath.Base(f), ".events.jsonl")
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		st, _ := fh.Stat()
		if st.Size() < s.at[f] { // rewritten
			s.at[f], s.tail[f], s.evs[char] = 0, "", nil
		}
		fh.Seek(s.at[f], io.SeekStart)
		buf, _ := io.ReadAll(io.LimitReader(fh, st.Size()-s.at[f]))
		fh.Close()
		s.at[f] += int64(len(buf))
		lines := strings.Split(s.tail[f]+string(buf), "\n")
		s.tail[f] = lines[len(lines)-1]
		for _, l := range lines[:len(lines)-1] {
			for _, k := range keep {
				if strings.Contains(l, k) {
					if e, ok := metrics.Decode([]byte(l)); ok {
						s.evs[char] = append(s.evs[char], e)
					}
					break
				}
			}
		}
	}
}

// since: every stream's events from t on (unix ms).
func (s *streams) since(t int64) map[string][]metrics.Event {
	out := map[string][]metrics.Event{}
	for c, evs := range s.evs {
		for i, e := range evs {
			if e.T >= t {
				out[c] = evs[i:]
				break
			}
		}
	}
	return out
}
