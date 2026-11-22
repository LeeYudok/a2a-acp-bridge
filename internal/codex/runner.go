// Package codex 는 `codex exec --json`(또는 `codex exec resume`)을 실행하고 JSONL 이벤트를 해석한다.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/LeeYudok/a2a-acp-bridge/internal/gate"
)

// Result 는 Codex 실행 한 번의 결과다
type Result struct {
	ThreadID    string
	Response    string
	AllMessages []string
}

// maxJSONLLine 은 JSONL 한 줄의 최대 크기다 (agent_message 하나가 길 수 있다)
const maxJSONLLine = 4 * 1024 * 1024

// ParseJSONL 은 `codex exec --json` 의 JSONL 출력에서 thread_id 와 agent_message 텍스트를 추출한다.
// 빈 줄은 건너뛰고, JSON 이 아닌 줄이나 너무 긴 줄은 오류로 돌려준다.
func ParseJSONL(stdout, initialThreadID string) (string, []string, error) {
	threadID := initialThreadID
	msgs := []string{}
	sc := bufio.NewScanner(strings.NewReader(stdout))
	sc.Buffer(make([]byte, 64*1024), maxJSONLLine)
	lineNumber := 0
	for sc.Scan() {
		lineNumber++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
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
		if err := json.Unmarshal(line, &evt); err != nil {
			return "", nil, fmt.Errorf("codex JSONL line %d: %w", lineNumber, err)
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
	if err := sc.Err(); err != nil {
		return "", nil, fmt.Errorf("scan codex JSONL line %d: %w", lineNumber+1, err)
	}
	return threadID, msgs, nil
}

// Run 은 Codex 에 프롬프트를 보낸다. threadID 가 있으면 그 스레드를 이어간다.
// 개인 게이트가 사용자 확인을 요구하면 같은 스레드에서 한 번 더 답한다.
func Run(ctx context.Context, cwd, sandbox, promptText, threadID string) (Result, error) {
	sandbox = EffectiveSandbox(sandbox, threadID)
	tid, msgs, err := runOnce(ctx, cwd, sandbox, promptText, threadID)
	if err != nil {
		return Result{}, err
	}
	last := lastMessage(msgs)
	if user := gate.ActiveUser(cwd); user != "" && tid != "" && gate.NeedsReply(last) {
		tid2, msgs2, err := runOnce(ctx, cwd, sandbox, gate.Followup(user, promptText), tid)
		if err != nil {
			return Result{}, err
		}
		if len(msgs2) > 0 {
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
	sandbox = EffectiveSandbox(sandbox, threadID)
	var args []string
	if threadID != "" {
		args = []string{"-s", sandbox, "exec", "resume", "--skip-git-repo-check", "--json", threadID, promptText}
	} else {
		args = []string{"-s", sandbox, "exec", "--skip-git-repo-check", "--json", promptText}
	}
	cmdName := "codex"
	if parts := strings.Fields(os.Getenv("A2A_CODEX_COMMAND")); len(parts) > 0 {
		cmdName = parts[0]
		args = append(parts[1:], args...)
	}
	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Dir = cwd
	cmd.Env = childEnv(cwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if err := ctx.Err(); err != nil {
		return "", nil, outputError("codex exec", err, stderr.String())
	}
	if runErr != nil {
		return "", nil, outputError("codex exec", runErr, stderr.String())
	}
	tid, msgs, err := ParseJSONL(stdout.String(), threadID)
	if err != nil {
		return "", nil, outputError("parse codex exec stdout", err, stderr.String())
	}
	if tid == "" && len(msgs) == 0 {
		return "", nil, outputError("parse codex exec stdout", errors.New("no thread ID or agent messages"), stderr.String())
	}
	return tid, msgs, nil
}

// EffectiveSandbox 는 생략된 샌드박스 정책을 정한다. Codex 0.170.0의 resume은
// 기존 스레드 정책을 복원하지 않고 현재 설정을 다시 적용하므로, 재개는 읽기 전용을 기본으로 둔다.
func EffectiveSandbox(sandbox, threadID string) string {
	if sandbox != "" {
		return sandbox
	}
	if threadID != "" {
		return "read-only"
	}
	return "workspace-write"
}

// childEnv 는 부모 Codex 의 실행 문맥을 자식에게 넘기지 않고, 필요하면 대상 CODEX_HOME 만 교체한다.
func childEnv(cwd string) []string {
	targetHome := os.Getenv("A2A_CODEX_HOME")
	pwd := ""
	if cwd != "" {
		pwd = cwd
		if abs, err := filepath.Abs(cwd); err == nil {
			pwd = abs
		}
	}
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "CODEX_SESSION_ID", "CODEX_THREAD_ID", "CODEX_TOOL_CALL_ID":
			continue
		case "CODEX_HOME":
			if targetHome != "" {
				continue
			}
		case "PWD":
			if pwd != "" {
				continue
			}
		}
		env = append(env, entry)
	}
	if targetHome != "" {
		env = append(env, "CODEX_HOME="+targetHome)
	}
	if pwd != "" {
		env = append(env, "PWD="+pwd)
	}
	return env
}

// outputError 는 오류에 stderr 진단 내용을 덧붙인다
func outputError(operation string, err error, stderr string) error {
	if diagnostic := strings.TrimSpace(stderr); diagnostic != "" {
		return fmt.Errorf("%s: %w (stderr: %s)", operation, err, diagnostic)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
