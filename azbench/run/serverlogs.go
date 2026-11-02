package run

// The server's logs over a run: the worldserver's log, its errors and mod-recorder's lines (when it records), from
// where each stood when the clock started (run.json's server_logs) to the end, saved in <run>/server/ at each pause
// and at the end. The server's own files keep growing across runs, and its log lines carry no time.

import (
	"io"
	"os"
	"path/filepath"

	"github.com/pthm/leeroy/azbench/runfiles"
)

var serverLogNames = []string{"Server.log", "Errors.log", "recorder.jsonl"}

func (r *Runner) serverLogDir() string {
	return filepath.Join(r.repo, "server/azerothcore/env/dist/logs")
}

// noteServerLogs records each server log's length, when the clock starts.
func (r *Runner) noteServerLogs(m *runfiles.Manifest) {
	if m.ServerLogs != nil {
		return // a resume: the run's own start counts
	}
	m.ServerLogs = map[string]int64{}
	for _, name := range serverLogNames {
		if st, err := os.Stat(filepath.Join(r.serverLogDir(), name)); err == nil {
			m.ServerLogs[name] = st.Size()
		}
	}
}

// saveServerLogs copies what each server log gained since the clock started into server/.
func (r *Runner) saveServerLogs() {
	m, err := r.f().ReadManifest()
	if err != nil || m.ServerLogs == nil {
		return
	}
	os.MkdirAll(r.f().Server(), 0o755)
	for name, at := range m.ServerLogs {
		in, err := os.Open(filepath.Join(r.serverLogDir(), name))
		if err != nil {
			continue
		}
		if st, _ := in.Stat(); st == nil || st.Size() <= at { // nothing new (or rotated: shorter than it was)
			in.Close()
			continue
		}
		in.Seek(at, io.SeekStart)
		if out, err := os.Create(filepath.Join(r.f().Server(), name)); err == nil {
			io.Copy(out, in)
			out.Close()
		}
		in.Close()
	}
}
