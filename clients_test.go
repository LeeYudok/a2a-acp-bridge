package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCodexJSONLLargeMessage(t *testing.T) {
	message := strings.Repeat("x", 3*1024*1024)
	output := "{\"type\":\"thread.started\",\"thread_id\":\"new\"}\n\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"` + message + `"}}`
	id, messages, err := ParseCodexJSONL(output, "old")
	if err != nil {
		t.Fatal(err)
	}
	if id != "new" || len(messages) != 1 || messages[0] != message {
		t.Fatalf("thread ID = %q, message count = %d, expected new thread and one complete message", id, len(messages))
	}
}

func TestParseCodexJSONLErrors(t *testing.T) {
	tests := []struct {
		name, line, diagnostic string
	}{
		{"malformed JSON", "{", "codex JSONL line 2"},
		{"plain text", "unexpected output", "codex JSONL line 2"},
		{"oversized line", strings.Repeat("x", 4*1024*1024+1), "scan codex JSONL line 2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			output := "{\"type\":\"thread.started\",\"thread_id\":\"partial\"}\n" + tc.line
			id, messages, err := ParseCodexJSONL(output, "")
			if err == nil || !strings.Contains(err.Error(), tc.diagnostic) || id != "" || len(messages) != 0 {
				t.Fatalf("id = %q, messages = %v, error = %v, expected %q and no partial result", id, messages, err, tc.diagnostic)
			}
		})
	}
}

func TestRunCodexOnceStreamsAndErrors(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	const threadEvent = `printf '%s\n' '{"type":"thread.started","thread_id":"stdout"}'`
	const stderrEvent = `printf '%s\n' '{"type":"thread.started","thread_id":"stderr"}' >&2`
	tests := []struct {
		name, script, diagnostic string
		check                    func(error) bool
	}{
		{name: "stderr ignored on success", script: threadEvent + "\n" + stderrEvent},
		{
			name: "nonzero exit with valid stdout", script: threadEvent + "\n" + stderrEvent + "\nexit 7",
			diagnostic: stderrEventID,
			check: func(err error) bool {
				var exitErr *exec.ExitError
				return errors.As(err, &exitErr) && exitErr.ExitCode() == 7
			},
		},
		{
			name: "invalid stdout", script: "printf '{'; " + stderrEvent,
			diagnostic: stderrEventID,
			check: func(err error) bool {
				var syntaxErr *json.SyntaxError
				return errors.As(err, &syntaxErr)
			},
		},
		{
			name: "events only on stderr", script: stderrEvent,
			diagnostic: stderrEventID,
			check: func(err error) bool {
				return strings.Contains(err.Error(), "no thread ID or agent messages")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "codex-test")
			if err := os.WriteFile(script, []byte("#!/bin/sh\n"+tc.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("A2A_CODEX_COMMAND", "codex-test")
			t.Setenv("PATH", filepath.Dir(script))
			id, _, err := runCodexOnce(context.Background(), "", "", "", "")
			if tc.check == nil {
				if err != nil || id != "stdout" {
					t.Fatalf("id = %q, error = %v, expected stdout thread", id, err)
				}
			} else if err == nil || !tc.check(err) || !strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("error = %v, expected %s with stderr diagnostic", err, tc.name)
			}
		})
	}
}

const stderrEventID = `"thread_id":"stderr"`

func TestRunCodexOnceCanceled(t *testing.T) {
	t.Setenv("A2A_CODEX_COMMAND", os.Args[0])
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := runCodexOnce(ctx, "", "", "", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, expected context.Canceled", err)
	}
}
