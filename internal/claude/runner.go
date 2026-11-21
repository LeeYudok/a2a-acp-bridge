// Package claude 는 Claude Code 를 `claude -p --output-format json` 으로 실행하고 결과 JSON 을 해석한다.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultPermissionMode 는 권한 모드를 주지 않았을 때 쓰는 값이다. 읽기 전용이다.
const DefaultPermissionMode = "plan"

// PermissionModes 는 위임에 허용하는 권한 모드다
var PermissionModes = []string{"plan", "default", "acceptEdits"}

// waitDelay 는 컨텍스트 취소 뒤 출력 파이프가 닫히기를 기다리는 최대 시간이다
const waitDelay = 2 * time.Second

// Result 는 Claude Code 실행 한 번의 결과다
type Result struct {
	SessionID  string
	Response   string
	StopReason string
	CostUSD    float64
}

type printOutput struct {
	Type         string  `json:"type"`
	Subtype      string  `json:"subtype"`
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	SessionID    string  `json:"session_id"`
	StopReason   string  `json:"stop_reason"`
	TotalCostUSD float64 `json:"total_cost_usd"`
}

// ParseOutput 은 `claude -p --output-format json` 의 stdout 을 해석한다
func ParseOutput(stdout []byte) (Result, error) {
	var out printOutput
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &out); err != nil {
		return Result{}, fmt.Errorf("claude JSON 파싱 실패: %w", err)
	}
	if out.IsError {
		return Result{}, fmt.Errorf("claude 실행 오류(%s): %s", out.Subtype, out.Result)
	}
	if out.SessionID == "" && out.Result == "" {
		return Result{}, errors.New("claude 응답에 session_id 와 result 가 없음")
	}
	return Result{SessionID: out.SessionID, Response: out.Result, StopReason: out.StopReason, CostUSD: out.TotalCostUSD}, nil
}

// Run 은 Claude Code 에 프롬프트를 보낸다. sessionID 가 있으면 그 세션을 이어간다.
// 프롬프트는 stdin 으로 넘겨 '-' 로 시작하는 글도 플래그로 오인되지 않게 한다.
func Run(ctx context.Context, cwd, permissionMode, prompt, sessionID string) (Result, error) {
	if permissionMode == "" {
		permissionMode = DefaultPermissionMode
	}
	args := []string{"-p", "--output-format", "json", "--permission-mode", permissionMode}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	cmdName := "claude"
	if parts := strings.Fields(os.Getenv("A2A_CLAUDE_COMMAND")); len(parts) > 0 {
		cmdName = parts[0]
		args = append(parts[1:], args...)
	}
	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(prompt)
	// 취소 뒤 claude 의 자식 프로세스가 stdout 을 붙잡고 있어도 Wait 가 끝나게 한다
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if err := ctx.Err(); err != nil {
		return Result{}, withStderr("claude -p", err, stderr.String())
	}
	if runErr != nil {
		// stdout 이 is_error 결과 JSON 이면 그 내용이 종료 코드보다 정확하다. 그 밖의 출력이면 종료 오류를 그대로 쓴다.
		var out printOutput
		if json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &out) == nil && out.IsError {
			return Result{}, withStderr("claude -p", fmt.Errorf("claude 실행 오류(%s): %s: %w", out.Subtype, out.Result, runErr), stderr.String())
		}
		return Result{}, withStderr("claude -p", runErr, stderr.String())
	}
	res, err := ParseOutput(stdout.Bytes())
	if err != nil {
		return Result{}, withStderr("claude -p", err, stderr.String())
	}
	return res, nil
}

func withStderr(operation string, err error, stderr string) error {
	if diagnostic := strings.TrimSpace(stderr); diagnostic != "" {
		return fmt.Errorf("%s: %w (stderr: %s)", operation, err, diagnostic)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
