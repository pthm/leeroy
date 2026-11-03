package run

import (
	"fmt"
	"strings"
)

// agentMaxWaitSeconds caps a wait (wow wait, wow -w): under the 120 s that Claude Code's and opencode's shells give a
// command by default, with room to spare (codex exec has no limit: a 150 s command ran to completion)
const agentMaxWaitSeconds = 110

// The same command inventory feeds the runtime permissions and generated help.
func shellContract(runtime string) string {
	names := make([]string, len(agentCommands))
	for i, c := range agentCommands {
		names[i] = "`" + c + "`"
	}
	text := fmt.Sprintf("\n## Shell contract (%s)\n\nSupported commands: %s.\n\nUse `wow wait 20` or `wow -w 20 'return task.list()'`. This runtime sets a maximum wait of %d seconds; `wow help` reports the active limit. Repeat individual calls to wait longer. A rejected wait returns a nonzero exit code and a shorter-call alternative. Create files with the runtime's file editing tool.\n", runtime, strings.Join(names, ", "), agentMaxWaitSeconds)
	switch runtime {
	case "codex":
		text += "\nThe runtime enforces workspace write boundaries, not a command allowlist.\n"
	case "claude":
		text += "\nShell permissions allow the command prefixes above. Prefer bounded foreground Bash calls. If Bash returns a background task ID, use the runtime's completion notification; Read can inspect the output file named in the response. Do not assume TaskOutput is installed, and do not treat background launch as completion. Nobody is there to approve a prompt, so a command that needs one is refused: shell variables (`$X`), brace or range globs and redirects outside your workspace all do; write temporary files in your workspace.\n"
	case "opencode":
		text += "\nBash permissions allow each listed command and its arguments; other commands are denied. Use separate short calls if a long shell call times out.\n"
	}
	return text
}
