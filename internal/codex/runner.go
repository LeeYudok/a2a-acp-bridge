// Package codex 는 `codex exec --json`(또는 `codex exec resume`)을 실행하고 JSONL 이벤트를 해석한다.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/LeeYudok/a2a-acp-bridge/internal/gate"
)

// Result 는 Codex 실행 한 번의 결과다
type Result struct {
	ThreadID    string
	Response    string
	AllMessages []string
}

// ParseJSONL 은 `codex exec --json` 의 JSONL 출력에서 thread_id 와 agent_message 텍스트를 추출한다
func ParseJSONL(stdout, initialThreadID string) (string, []string) {
	threadID := initialThreadID
	msgs := []string{}
	sc := bufio.NewScanner(strings.NewReader(stdout))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var evt struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Item     struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "thread.started":
			if evt.ThreadID != "" {
				threadID = evt.ThreadID
			}
		case "item.completed":
			if evt.Item.Type == "agent_message" && evt.Item.Text != "" {
				msgs = append(msgs, evt.Item.Text)
			}
		}
	}
	return threadID, msgs
}

// Run 은 Codex 에 프롬프트를 보낸다. threadID 가 있으면 그 스레드를 이어간다.
// 개인 게이트가 사용자 확인을 요구하면 같은 스레드에서 한 번 더 답한다.
func Run(ctx context.Context, cwd, sandbox, promptText, threadID string) (Result, error) {
	tid, msgs, err := runOnce(ctx, cwd, sandbox, promptText, threadID)
	if err != nil {
		return Result{}, err
	}
	last := lastMessage(msgs)
	if user := gate.ActiveUser(cwd); user != "" && tid != "" && gate.NeedsReply(last) {
		if tid2, msgs2, err2 := runOnce(ctx, cwd, sandbox, gate.Followup(user, promptText), tid); err2 == nil && len(msgs2) > 0 {
			tid, msgs, last = tid2, msgs2, lastMessage(msgs2)
		}
	}
	return Result{ThreadID: tid, Response: last, AllMessages: msgs}, nil
}

func lastMessage(msgs []string) string {
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1]
}

func runOnce(ctx context.Context, cwd, sandbox, promptText, threadID string) (string, []string, error) {
	if sandbox == "" {
		sandbox = "workspace-write"
	}
	args := []string{"exec", "--skip-git-repo-check", "--json", "-s", sandbox, promptText}
	if threadID != "" {
		args = []string{"exec", "resume", "--skip-git-repo-check", "--json", threadID, promptText}
	}
	cmdName := "codex"
	if envCmd := os.Getenv("A2A_CODEX_COMMAND"); envCmd != "" {
		parts := strings.Fields(envCmd)
		cmdName = parts[0]
		args = append(parts[1:], args...)
	}
	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Dir = cwd
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	_ = cmd.Run()
	tid, msgs := ParseJSONL(out.String(), threadID)
	if tid == "" && len(msgs) == 0 {
		return "", []string{}, fmt.Errorf("codex exec 출력 파싱 실패: %s", strings.TrimSpace(out.String()))
	}
	return tid, msgs, nil
}
