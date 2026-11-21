package agents

import (
	"slices"
	"testing"
)

func TestRegistry(t *testing.T) {
	if ids := IDs(); !slices.Equal(ids, []string{"agy", "codex", "claude"}) {
		t.Fatalf("IDs = %v", ids)
	}
	if s := Senders(); !slices.Contains(s, User) || len(s) != 4 {
		t.Fatalf("Senders = %v", s)
	}
	cases := map[string]string{
		"codex-mcp-client": "codex",
		"Antigravity":      "agy",
		"gemini-cli":       "agy",
		"claude-code":      "claude",
		"curl":             "",
	}
	for name, want := range cases {
		if got := FromClientName(name); got != want {
			t.Errorf("FromClientName(%q) = %q, 원하는 값 %q", name, got, want)
		}
	}
}
