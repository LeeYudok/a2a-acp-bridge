package codex

import (
	"strings"
	"testing"
)

func TestParseJSONL(t *testing.T) {
	out := `{"type":"thread.started","thread_id":"th-1"}` + "\n" +
		`{"type":"item.completed","item":{"type":"reasoning","text":"생각"}}` + "\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"첫 답"}}` + "\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"마지막 답"}}` + "\n"
	tid, msgs, err := ParseJSONL(out, "")
	if err != nil || tid != "th-1" || len(msgs) != 2 || msgs[1] != "마지막 답" {
		t.Fatalf("ParseJSONL = (%q, %v, %v)", tid, msgs, err)
	}
	if tid, _, err := ParseJSONL("", "th-keep"); err != nil || tid != "th-keep" {
		t.Fatalf("이벤트가 없으면 기존 스레드 ID 를 유지해야 함: %q, %v", tid, err)
	}
}

func TestParseJSONLLargeMessage(t *testing.T) {
	message := strings.Repeat("x", 3*1024*1024)
	output := "{\"type\":\"thread.started\",\"thread_id\":\"new\"}\n\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"` + message + `"}}`
	id, messages, err := ParseJSONL(output, "old")
	if err != nil {
		t.Fatal(err)
	}
	if id != "new" || len(messages) != 1 || messages[0] != message {
		t.Fatalf("thread ID = %q, message count = %d, expected new thread and one complete message", id, len(messages))
	}
}

func TestParseJSONLErrors(t *testing.T) {
	tests := []struct {
		name, line, diagnostic string
	}{
		{"malformed JSON", "{", "codex JSONL line 2"},
		{"plain text", "unexpected output", "codex JSONL line 2"},
		{"oversized line", strings.Repeat("x", maxJSONLLine+1), "scan codex JSONL line 2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			output := "{\"type\":\"thread.started\",\"thread_id\":\"partial\"}\n" + tc.line
			id, messages, err := ParseJSONL(output, "")
			if err == nil || !strings.Contains(err.Error(), tc.diagnostic) || id != "" || len(messages) != 0 {
				t.Fatalf("id = %q, messages = %v, error = %v, expected %q and no partial result", id, messages, err, tc.diagnostic)
			}
		})
	}
}
