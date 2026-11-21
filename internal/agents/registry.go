package agents

import "strings"

// User 는 사람이 CLI 나 HTTP 로 직접 호출했을 때의 발신자 ID 다
const User = "user"

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

// FromClientName 은 MCP 클라이언트 이름으로 에이전트 ID 를 추정한다. 모르면 빈 문자열이다.
func FromClientName(name string) string {
	lower := strings.ToLower(name)
	for _, h := range clientHints {
		if strings.Contains(lower, h.hint) {
			return h.id
		}
	}
	return ""
}
