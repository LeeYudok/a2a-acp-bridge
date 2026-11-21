// Package gate 는 작업 디렉터리의 개인 게이트(.claude/state/personal-gate) 상태를 읽고,
// 에이전트가 사용자 확인을 요구할 때 보낼 후속 프롬프트를 만든다.
package gate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var replyMarkers = []string{"사용자를 확인", "사용자가 누구인지", "누구이신가요", "personal-layer.sh use"}

// ActiveUser 는 cwd 의 개인 게이트에 기록된 활성 사용자 이름을 반환한다. 없으면 빈 문자열이다.
func ActiveUser(cwd string) string {
	raw, err := os.ReadFile(filepath.Join(cwd, ".claude", "state", "personal-gate", "current"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// NeedsReply 는 에이전트 응답이 사용자 확인을 요구하는지 판단한다.
func NeedsReply(text string) bool {
	for _, m := range replyMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// Followup 은 사용자 확인 요구에 답하면서 원래 요청을 다시 전달하는 프롬프트를 만든다.
func Followup(user, original string) string {
	return fmt.Sprintf("%s\n(`.claude/scripts/personal-layer.sh use %s` 단독 실행 후 원래 요청에 답하세요: %s)", user, user, original)
}
