package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// User 는 사람이 CLI 나 HTTP 로 직접 호출했을 때의 발신자 ID 다
const User = "user"

// ExtraAgentsPath 는 동적 N-에이전트 정의 파일 경로를 반환한다.
// A2A_AGENTS_FILE 환경변수가 없으면 ~/.local/share/a2a-acp-bridge/agents.json 을 확인한다.
func ExtraAgentsPath() string {
	if env := os.Getenv("A2A_AGENTS_FILE"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "a2a-acp-bridge", "agents.json")
}

// IsValidSlug 는 에이전트 ID가 ^[a-z0-9][a-z0-9-]{0,23}$ 소문자 slug 인지 검사한다.
func IsValidSlug(id string) bool {
	if len(id) == 0 || len(id) > 24 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if i == 0 && !alnum {
			return false
		}
		if !alnum && c != '-' {
			return false
		}
	}
	return true
}

// Cards 는 기본 내장 에이전트(agy·codex·claude)와 동적 레지스트리(hermes·opencode 등)를 합쳐 반환한다.
func Cards() []Card {
	base := builtinCards()
	extra := loadExtraCards(ExtraAgentsPath())
	if len(extra) == 0 {
		return base
	}
	byIdx := make(map[string]int, len(base))
	for i, c := range base {
		byIdx[c.ID] = i
	}
	for _, c := range extra {
		if idx, ok := byIdx[c.ID]; ok {
			base[idx] = c
			continue
		}
		byIdx[c.ID] = len(base)
		base = append(base, c)
	}
	return base
}

func loadExtraCards(path string) []Card {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var parsed []Card
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	out := make([]Card, 0, len(parsed))
	for _, c := range parsed {
		c.ID = strings.TrimSpace(c.ID)
		if !IsValidSlug(c.ID) || c.ID == User {
			continue
		}
		if c.Capabilities == nil {
			c.Capabilities = map[string]bool{}
		}
		if c.Protocols == nil {
			c.Protocols = []string{"A2A-1.0"}
		}
		if c.Skills == nil {
			c.Skills = []Skill{}
		}
		out = append(out, c)
	}
	return out
}

// IDs 는 보드 메시지를 받을 수 있는 에이전트 ID 목록이다
func IDs() []string {
	cards := Cards()
	ids := make([]string, 0, len(cards))
	for _, c := range cards {
		ids = append(ids, c.ID)
	}
	return ids
}

// Senders 는 발신자로 쓸 수 있는 ID 목록이다(에이전트 + user)
func Senders() []string {
	return append(IDs(), User)
}

// clientHints 는 MCP initialize 의 clientInfo.name 에 들어 있으면 그 에이전트로 보는 문자열이다
var clientHints = []struct{ hint, id string }{
	{"codex", "codex"},
	{"antigravity", "agy"},
	{"agy", "agy"},
	{"gemini", "agy"},
	{"claude", "claude"},
}

// FromClientName 은 MCP 클라이언트 이름으로 에이전트 ID 를 추정한다.
// 정확 일치를 우선 확인하고, 동적 에이전트 ID 는 단어 경계를 기준으로 매칭한다.
func FromClientName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return ""
	}
	ids := IDs()
	for _, id := range ids {
		if lower == id {
			return id
		}
	}
	for _, h := range clientHints {
		if strings.Contains(lower, h.hint) {
			return h.id
		}
	}
	for _, id := range ids {
		if matchesTokenBoundary(lower, id) {
			return id
		}
	}
	return ""
}

func matchesTokenBoundary(haystack, needle string) bool {
	if needle == "" || len(needle) > len(haystack) {
		return false
	}
	offset := 0
	for offset <= len(haystack)-len(needle) {
		idx := strings.Index(haystack[offset:], needle)
		if idx < 0 {
			return false
		}
		start := offset + idx
		end := start + len(needle)
		leftOK := start == 0 || !isAlphaNum(haystack[start-1])
		rightOK := end == len(haystack) || !isAlphaNum(haystack[end])
		if leftOK && rightOK {
			return true
		}
		offset = start + 1
	}
	return false
}

func isAlphaNum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}
