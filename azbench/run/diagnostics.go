package run

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// harness writes only typed lifecycle fields. It never includes prompts, commands or runtime error prose.
// The sequence continues across supervisor restarts; a torn final line remains visible as a coverage gap.
func (p *player) harness(details types.DiagnosticDetails) {
	p.harnessEvent(types.Diagnostic{Kind: "runtime_state", Details: details})
}

func (p *player) harnessEvent(ev types.Diagnostic) {
	p.diagMu.Lock()
	defer p.diagMu.Unlock()
	path := filepath.Join(p.dir, "harness.jsonl")
	if !p.diagInit {
		if f, err := os.Open(path); err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 4096), 1024*1024)
			for sc.Scan() {
				var ev types.Diagnostic
				if json.Unmarshal(sc.Bytes(), &ev) == nil {
					p.diagSeq = max(p.diagSeq, ev.Seq)
				}
			}
			if err := sc.Err(); err != nil {
				p.logf("reading diagnostics: %v", err)
				f.Close()
				return
			}
			f.Close()
		} else if !os.IsNotExist(err) {
			p.logf("opening diagnostics: %v", err)
			return
		}
		p.diagInit = true
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		p.logf("writing diagnostics: %v", err)
		return
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		var last [1]byte
		if _, err = f.ReadAt(last[:], st.Size()-1); err == nil && last[0] != '\n' {
			if _, err = f.Write([]byte{'\n'}); err != nil {
				p.logf("writing diagnostics: %v", err)
				return
			}
		}
	}
	p.diagSeq++
	ev.Schema, ev.Seq, ev.T, ev.Agent = 1, p.diagSeq, time.Now().UnixMilli(), p.a.Name
	b, err := json.Marshal(ev)
	if err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err != nil {
		p.logf("writing diagnostics: %v", err)
	}
}

func (p *player) runtimeState(component, state string) {
	p.harness(types.DiagnosticDetails{Component: component, State: state, Runtime: p.a.Runtime})
}
