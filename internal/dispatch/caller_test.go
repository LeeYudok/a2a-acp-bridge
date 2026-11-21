package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func lastMessagePair(t *testing.T, d *Dispatcher, to string) (string, string) {
	t.Helper()
	msgs, err := d.Board.ReceiveMessages(to, false, false, 0)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("%s 수신함 조회 실패: %v (%d건)", to, err, len(msgs))
	}
	m := msgs[len(msgs)-1]
	return m.FromAgent, m.ToAgent
}

func TestAgyDelegationRecordsCaller(t *testing.T) {
	d := newTestDispatcher(t, newMockPool(t))
	ctx := context.Background()

	cases := []struct {
		caller, fromArg, want string
	}{
		{"", "", "user"},
		{"codex", "", "codex"},
		{"codex", "claude", "claude"},
	}
	for _, c := range cases {
		d.Caller = c.caller
		args := map[string]any{"prompt": "누가 보냈나"}
		if c.fromArg != "" {
			args["from_agent"] = c.fromArg
		}
		if _, err := d.CallTool(ctx, "acp_delegate_to_agy", args); err != nil {
			t.Fatalf("위임 실패: %v", err)
		}
		if from, _ := lastMessagePair(t, d, "agy"); from != c.want {
			t.Fatalf("Caller=%q from_agent=%q → 위임 발신자 %q, 원하는 값 %q", c.caller, c.fromArg, from, c.want)
		}
		if from, to := lastMessagePair(t, d, c.want); from != "agy" || to != c.want {
			t.Fatalf("응답이 %s → %s 로 기록됨, 원하는 값 agy → %s", from, to, c.want)
		}
	}
}

func TestCodexDelegationRecordsCaller(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-codex")
	body := "#!/bin/sh\n" +
		`echo '{"type":"thread.started","thread_id":"th-fake"}'` + "\n" +
		`echo '{"type":"item.completed","item":{"type":"agent_message","text":"가짜 응답"}}'` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_CODEX_COMMAND", script)
	d := newTestDispatcher(t, nil)

	if _, err := d.CallTool(context.Background(), "a2a_delegate_to_codex", map[string]any{"prompt": "리뷰", "from_agent": "claude", "sandbox": "read-only"}); err != nil {
		t.Fatalf("Codex 위임 실패: %v", err)
	}
	if from, _ := lastMessagePair(t, d, "codex"); from != "claude" {
		t.Fatalf("Codex 위임 발신자 = %q, 원하는 값 claude", from)
	}
	if from, _ := lastMessagePair(t, d, "claude"); from != "codex" {
		t.Fatalf("Codex 응답 발신자 = %q, 원하는 값 codex", from)
	}
}
