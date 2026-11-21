package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeDelegationRecordsCallerAndSession(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-claude")
	body := "#!/bin/sh\ncat >/dev/null\n" +
		`printf '{"type":"result","subtype":"success","is_error":false,"result":"클로드 응답","session_id":"sess-c1"}'` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_CLAUDE_COMMAND", script)
	d := newTestDispatcher(t, nil)
	ctx := context.Background()

	raw, err := d.CallTool(ctx, "a2a_delegate_to_claude", map[string]any{"prompt": "리뷰해 줘", "from_agent": "agy", "session_name": "review"})
	if err != nil {
		t.Fatalf("Claude 위임 실패: %v", err)
	}
	res := raw.(ClaudeDelegateResult)
	if res.Response != "클로드 응답" || res.SessionID != "sess-c1" || res.PermissionMode != "plan" {
		t.Fatalf("결과 = %+v", res)
	}
	if from, _ := lastMessagePair(t, d, "claude"); from != "agy" {
		t.Fatalf("위임 발신자 = %q, 원하는 값 agy", from)
	}
	if from, _ := lastMessagePair(t, d, "agy"); from != "claude" {
		t.Fatalf("응답 발신자 = %q, 원하는 값 claude", from)
	}
	if rec, err := d.Board.ResolveSession("review", "claude"); err != nil || rec.SessionID != "sess-c1" {
		t.Fatalf("명명 세션 저장 실패: %+v %v", rec, err)
	}
	if _, err := d.CallTool(ctx, "a2a_delegate_to_claude", map[string]any{"prompt": "x", "permission_mode": "bypassPermissions"}); err == nil {
		t.Fatal("허용하지 않은 permission_mode 는 거부해야 함")
	}
}

func writeFakeClaude(t *testing.T, body string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fake-claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_CLAUDE_COMMAND", script)
}

// 명명 세션을 cwd 없이 이어가면 그 세션을 만든 디렉터리에서 실행해야 한다(PR #14 Codex 리뷰 P2)
func TestClaudeNamedSessionReusesStoredCwd(t *testing.T) {
	writeFakeClaude(t, `printf '{"type":"result","is_error":false,"result":"%s","session_id":"sess-cwd"}' "$PWD"`+"\n")
	d := newTestDispatcher(t, nil)
	repo, _ := filepath.EvalSymlinks(t.TempDir())
	ctx := context.Background()
	if _, err := d.CallTool(ctx, "a2a_delegate_to_claude", map[string]any{"prompt": "1", "session_name": "rev", "cwd": repo}); err != nil {
		t.Fatal(err)
	}
	raw, err := d.CallTool(ctx, "a2a_delegate_to_claude", map[string]any{"prompt": "2", "session_name": "rev"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := filepath.EvalSymlinks(raw.(ClaudeDelegateResult).Response); got != repo {
		t.Fatalf("두 번째 호출 cwd = %q, 원하는 값(저장된 세션 cwd) %q", got, repo)
	}
}

// async 작업도 AsyncTimeout 을 지켜야 한다(PR #14 Codex 리뷰 P2)
func TestAsyncDelegationHonorsTimeout(t *testing.T) {
	writeFakeClaude(t, "sleep 5\n")
	d := newTestDispatcher(t, nil)
	d.AsyncTimeout = 300 * time.Millisecond
	start := time.Now()
	raw, err := d.CallTool(context.Background(), "a2a_delegate_to_claude", map[string]any{"prompt": "느린 일", "async": true})
	if err != nil {
		t.Fatal(err)
	}
	d.Wait()
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("async 작업이 타임아웃을 무시함: %s", elapsed)
	}
	task, err := d.Board.GetTask(raw.(ClaudeDelegateResult).TaskID)
	if err != nil || task.State != "failed" {
		t.Fatalf("타임아웃된 태스크 상태 = %q (%v), 원하는 값 failed", task.State, err)
	}
}
