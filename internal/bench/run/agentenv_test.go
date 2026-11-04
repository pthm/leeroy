package run

import (
	"slices"
	"strings"
	"testing"

	"github.com/pthm/leeroy/internal/bench/config"
)

func TestAgentEnv(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin", "HOME=/home/pat", "LANG=en_GB.UTF-8", "LC_ALL=C", "MISE_DATA_DIR=/m", "LEEROY_DB_ADDR=db:3306",
		"CLAUDE_CODE_OAUTH_TOKEN=claude-token", "OPENAI_API_KEY=openai-key", "OPENROUTER_API_KEY=or-key",
		"R2_SECRET_ACCESS_KEY=r2", "HCLOUD_TOKEN=hc", "GITHUB_TOKEN=gh", "AWS_SECRET_ACCESS_KEY=aws", "LEEROY_HOST_PRICE_HOUR=1",
		"OPENCODE_CONFIG=/x", "SSH_AUTH_SOCK=/s",
	}
	names := func(env []string) []string {
		out := make([]string, 0, len(env))
		for _, kv := range env {
			k, _, _ := strings.Cut(kv, "=")
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	basics := []string{"HOME", "LANG", "LC_ALL", "LEEROY_DB_ADDR", "MISE_DATA_DIR", "PATH"}
	for _, c := range []struct {
		a    config.Agent
		keys []string
	}{
		{config.Agent{Runtime: "claude"}, []string{"CLAUDE_CODE_OAUTH_TOKEN"}},
		{config.Agent{Runtime: "codex"}, []string{"OPENAI_API_KEY"}},
		{config.Agent{Runtime: "opencode", Provider: "openrouter", Model: "google/gemini"}, []string{"OPENROUTER_API_KEY"}},
		{config.Agent{Runtime: "opencode", Model: "openai/gpt"}, []string{"OPENAI_API_KEY"}},
		{
			config.Agent{Runtime: "claude", Provider: "openrouter", Model: "anthropic/claude"},
			[]string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "OPENROUTER_API_KEY"},
		},
	} {
		want := append(slices.Clone(basics), c.keys...)
		slices.Sort(want)
		env := agentEnv(environ, c.a)
		if got := names(env); !slices.Equal(got, want) {
			t.Errorf("%s/%s: got %v, want %v", c.a.Runtime, c.a.Provider, got, want)
		}
		if c.a.Runtime == "claude" && c.a.Provider == "openrouter" && !slices.Contains(env, "ANTHROPIC_API_KEY=or-key") {
			t.Errorf("claude through openrouter: ANTHROPIC_API_KEY isn't the OpenRouter key")
		}
	}
}
