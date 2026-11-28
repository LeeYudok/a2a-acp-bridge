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
		"claudedesktop":    "claude",
		"notcodex":         "",
		"myclaudeclient":   "",
		"curl":             "",
	}
	for name, want := range cases {
		if got := FromClientName(name); got != want {
			t.Errorf("FromClientName(%q) = %q, 원하는 값 %q", name, got, want)
		}
	}
}

func TestDynamicAgentsParseFailureIsVisible(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "agents.json")
	if err := os.WriteFile(cfg, []byte(`[{"id":`), 0o600); err != nil {
		t.Fatalf("깨진 agents.json 쓰기 실패: %v", err)
	}
	t.Setenv("A2A_AGENTS_FILE", cfg)

	cards, warnings := CardsWithWarnings()
	if !slices.Equal(IDs(), []string{"agy", "codex", "claude"}) {
		t.Fatalf("파싱 실패 때 내장 카드가 유지되지 않음: cards=%v", cards)
	}
	if len(warnings) != 1 || warnings[0].Source != cfg || warnings[0].Message == "" {
		t.Fatalf("agents.json 파싱 경고 불일치: %+v", warnings)
	}
	disc := Discover(t.TempDir(), nil, nil)
	if len(disc.RegistryWarnings) != 1 {
		t.Fatalf("Discovery 에 레지스트리 경고가 없음: %+v", disc.RegistryWarnings)
	}
}

func TestDynamicAgentsFileLoadsCustomSlugs(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "agents.json")
	payload := `[
		{"id":"hermes","name":"Hermes Agent","endpoint":"hermes acp","protocols":["A2A-1.0","MCP"]},
		{"id":"opencode","name":"OpenCode CLI","endpoint":"opencode run"},
		{"id":"go","name":"Go Agent","endpoint":"go-agent"},
		{"id":"Bad_ID","name":"무효 slug"},
		{"id":"user","name":"예약 ID 제외"},
		{"id":"codex","name":"Codex Overridden","endpoint":"codex custom"}
	]`
	if err := os.WriteFile(cfg, []byte(payload), 0o600); err != nil {
		t.Fatalf("agents.json 쓰기 실패: %v", err)
	}
	t.Setenv("A2A_AGENTS_FILE", cfg)

	ids := IDs()
	if !slices.Equal(ids, []string{"agy", "codex", "claude", "hermes", "opencode", "go"}) {
		t.Fatalf("동적 에이전트 목록 불일치: %v", ids)
	}
	cards := Cards()
	if cards[1].Name != "Codex Overridden" {
		t.Fatalf("기존 ID 덮어쓰기 실패: %+v", cards[1])
	}
	// 짧은 slug "go" 는 경계 구분자가 있는 경우만 매칭되고 "cargo-cli" 와는 매칭되지 않아야 함
	if got := FromClientName("opencode-desktop"); got != "opencode" {
		t.Fatalf("FromClientName(opencode-desktop) = %q", got)
	}
	if got := FromClientName("go-agent"); got != "go" {
		t.Fatalf("FromClientName(go-agent) = %q", got)
	}
	if got := FromClientName("cargo-cli"); got != "" {
		t.Fatalf("FromClientName(cargo-cli) = %q (빈 문자열이어야 함)", got)
	}
	if IsValidSlug("") || IsValidSlug("-bad") || IsValidSlug("Bad") || !IsValidSlug("hermes-1") {
		t.Fatal("IsValidSlug 판정 오류")
	}
	hermesCard, ok := Find("hermes")
	if !ok || hermesCard.Executable {
		t.Fatalf("hermes without executor should not be executable: %+v ok=%v", hermesCard, ok)
	}
	agyCard, ok := Find("agy")
	if !ok || !agyCard.Executable {
		t.Fatalf("builtin agy should be executable: %+v ok=%v", agyCard, ok)
	}
}
