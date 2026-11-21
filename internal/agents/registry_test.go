package agents

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRegistry(t *testing.T) {
	t.Setenv("A2A_AGENTS_FILE", filepath.Join(t.TempDir(), "missing.json"))
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

func TestDynamicAgentsFileLoadsCustomSlugs(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "agents.json")
	payload := `[
		{"id":"hermes","name":"Hermes Agent","endpoint":"hermes acp","protocols":["A2A-1.0","MCP"]},
		{"id":"opencode","name":"OpenCode CLI","endpoint":"opencode run"},
		{"id":"Bad_ID","name":"무효 slug"},
		{"id":"user","name":"예약 ID 제외"},
		{"id":"codex","name":"Codex Overridden","endpoint":"codex custom"}
	]`
	if err := os.WriteFile(cfg, []byte(payload), 0o600); err != nil {
		t.Fatalf("agents.json 쓰기 실패: %v", err)
	}
	t.Setenv("A2A_AGENTS_FILE", cfg)

	ids := IDs()
	if !slices.Equal(ids, []string{"agy", "codex", "claude", "hermes", "opencode"}) {
		t.Fatalf("동적 에이전트 목록 불일치: %v", ids)
	}
	cards := Cards()
	if cards[1].Name != "Codex Overridden" {
		t.Fatalf("기존 ID 덮어쓰기 실패: %+v", cards[1])
	}
	if got := FromClientName("opencode-desktop"); got != "opencode" {
		t.Fatalf("FromClientName(opencode-desktop) = %q", got)
	}
	if IsValidSlug("") || IsValidSlug("-bad") || IsValidSlug("Bad") || !IsValidSlug("hermes-1") {
		t.Fatal("IsValidSlug 판정 오류")
	}
}
