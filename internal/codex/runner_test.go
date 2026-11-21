package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseJSONL(t *testing.T) {
	out := "warning: 잡음\n" +
		`{"type":"thread.started","thread_id":"th-1"}` + "\n" +
		`{"type":"item.completed","item":{"type":"reasoning","text":"생각"}}` + "\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"첫 답"}}` + "\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"마지막 답"}}` + "\n"
	tid, msgs := ParseJSONL(out, "")
	if tid != "th-1" || len(msgs) != 2 || msgs[1] != "마지막 답" {
		t.Fatalf("ParseJSONL = (%q, %v)", tid, msgs)
	}
	if tid, _ := ParseJSONL("", "th-keep"); tid != "th-keep" {
		t.Fatalf("이벤트가 없으면 기존 스레드 ID 를 유지해야 함: %q", tid)
	}
}

func TestRunUsesCommandOverride(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-codex")
	body := "#!/bin/sh\n" +
		`echo '{"type":"thread.started","thread_id":"th-fake"}'` + "\n" +
		`echo '{"type":"item.completed","item":{"type":"agent_message","text":"가짜 응답"}}'` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_CODEX_COMMAND", script)
	res, err := Run(context.Background(), t.TempDir(), "read-only", "질문", "")
	if err != nil {
		t.Fatalf("Run 실패: %v", err)
	}
	if res.ThreadID != "th-fake" || res.Response != "가짜 응답" {
		t.Fatalf("Run 결과 = %+v", res)
	}
}
