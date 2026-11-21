package claude

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude 는 인자와 stdin 을 받아 정해진 JSON 을 내는 가짜 claude 실행 파일을 만든다
func fakeClaude(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_CLAUDE_COMMAND", path)
}

func TestRunPassesPromptOnStdinAndResumes(t *testing.T) {
	fakeClaude(t, `prompt=$(cat)
printf '{"type":"result","subtype":"success","is_error":false,"result":"args=%s prompt=%s","session_id":"sess-1","stop_reason":"end_turn","total_cost_usd":0.5}' "$*" "$prompt"
`)
	res, err := Run(context.Background(), t.TempDir(), "", "-로 시작하는 질문", "sess-0")
	if err != nil {
		t.Fatalf("Run 실패: %v", err)
	}
	if res.SessionID != "sess-1" || res.StopReason != "end_turn" || res.CostUSD != 0.5 {
		t.Fatalf("결과 = %+v", res)
	}
	for _, want := range []string{"--permission-mode plan", "--resume sess-0", "--output-format json", "prompt=-로 시작하는 질문"} {
		if !strings.Contains(res.Response, want) {
			t.Fatalf("응답 %q 에 %q 가 없음", res.Response, want)
		}
	}
}

func TestRunReportsErrors(t *testing.T) {
	cases := map[string]string{
		"is_error": `printf '{"type":"result","subtype":"error_max_turns","is_error":true,"result":"턴 한도","session_id":"s"}'`,
		"exit":     `echo "인증 필요" >&2; exit 1`,
		"not json": `echo "plain text"`,
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			fakeClaude(t, script+"\n")
			if _, err := Run(context.Background(), t.TempDir(), "plan", "q", ""); err == nil {
				t.Fatal("오류를 돌려줘야 함")
			}
		})
	}
	fakeClaude(t, "echo '인증 필요' >&2; exit 1\n")
	_, err := Run(context.Background(), t.TempDir(), "plan", "q", "")
	if err == nil || !strings.Contains(err.Error(), "인증 필요") {
		t.Fatalf("stderr 진단이 오류에 있어야 함: %v", err)
	}
}

func TestRunPropagatesCancellation(t *testing.T) {
	fakeClaude(t, "sleep 5\n")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := Run(ctx, t.TempDir(), "plan", "q", ""); err == nil || ctx.Err() == nil {
		t.Fatalf("취소 오류를 돌려줘야 함: %v", err)
	}
}

// stdout 에 결과 JSON 이 아닌 출력이 있으면 종료 오류를 가리지 않아야 한다(PR #14 Codex 리뷰 P3)
func TestRunKeepsExitErrorWhenStdoutIsNotResult(t *testing.T) {
	fakeClaude(t, "echo starting; exit 7\n")
	_, err := Run(context.Background(), t.TempDir(), "plan", "q", "")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("종료 코드 7 의 ExitError 를 돌려줘야 함: %v", err)
	}
}
