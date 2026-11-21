package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexNamedSessionReusesAndStoresSandbox(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-codex")
	body := "#!/bin/sh\n" +
		`printf '%s\n' '{"type":"thread.started","thread_id":"th-sandbox"}'` + "\n" +
		`printf '{"type":"item.completed","item":{"type":"agent_message","text":"%s"}}\n' "$*"` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_CODEX_COMMAND", script)
	d := newTestDispatcher(t, nil)
	ctx := context.Background()

	raw, err := d.CallTool(ctx, "a2a_delegate_to_codex", map[string]any{
		"prompt": "첫 턴", "session_name": "worker",
	})
	if err != nil {
		t.Fatalf("첫 Codex 위임 실패: %v", err)
	}
	if !strings.Contains(raw.(CodexDelegateResult).Response, "-s workspace-write exec ") {
		t.Fatalf("첫 턴 샌드박스 인자 = %q", raw.(CodexDelegateResult).Response)
	}
	rec, err := d.Board.ResolveSession("worker", "codex")
	if err != nil || rec.Mode != "workspace-write" {
		t.Fatalf("저장된 Codex 세션 = %+v, %v", rec, err)
	}

	raw, err = d.CallTool(ctx, "a2a_delegate_to_codex", map[string]any{
		"prompt": "둘째 턴", "session_name": "worker",
	})
	if err != nil {
		t.Fatalf("둘째 Codex 위임 실패: %v", err)
	}
	if !strings.Contains(raw.(CodexDelegateResult).Response, "-s workspace-write exec resume ") {
		t.Fatalf("둘째 턴 샌드박스 인자 = %q", raw.(CodexDelegateResult).Response)
	}
}

func TestCodexNamedSessionStoresExplicitReadOnly(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-codex")
	body := "#!/bin/sh\n" +
		`printf '%s\n' '{"type":"thread.started","thread_id":"th-read-only"}'` + "\n" +
		`printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"완료"}}'` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_CODEX_COMMAND", script)
	d := newTestDispatcher(t, nil)
	if _, err := d.CallTool(context.Background(), "a2a_delegate_to_codex", map[string]any{
		"prompt": "읽기", "session_name": "reviewer", "sandbox": "read-only",
	}); err != nil {
		t.Fatalf("Codex 위임 실패: %v", err)
	}
	rec, err := d.Board.ResolveSession("reviewer", "codex")
	if err != nil || rec.Mode != "read-only" {
		t.Fatalf("저장된 Codex 세션 = %+v, %v", rec, err)
	}
}
