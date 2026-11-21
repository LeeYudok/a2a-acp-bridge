package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
