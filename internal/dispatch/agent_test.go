package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

func TestDelegateAgentPiMultiTurnAndSandbox(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "pi-calls.log")
	script := filepath.Join(dir, "fake-pi.sh")
	body := `#!/bin/sh
printf '%s\n' "$*" >> "` + logFile + `"
printf '{"type":"session","id":"pi-sess-1"}\n'
if printf '%s' "$*" | grep -q -- '--session-id pi-sess-1'; then
  printf '{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"TURN2"}]}}\n'
else
  printf '{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"TURN1"}]}}\n'
fi
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	agentsFile := filepath.Join(dir, "agents.json")
	agentsJSON := `[{"id":"pi","name":"Pi","version":"1.7.0","protocols":["Pi-print-json","A2A-1.0"],"endpoint":"pi -p --mode json","executor":{"type":"pi","command":["` + script + `"]}}]`
	if err := os.WriteFile(agentsFile, []byte(agentsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_AGENTS_FILE", agentsFile)

	d := &Dispatcher{
		Board:      board.New(filepath.Join(dir, "board.json")),
		DefaultCwd: dir,
		Caller:     "codex",
	}

	res1Raw, err := d.CallTool(context.Background(), "a2a_delegate_to_agent", map[string]any{
		"agent_id":     "pi",
		"session_name": "pi-review",
		"prompt":       "first turn",
	})
	if err != nil {
		t.Fatalf("turn 1 failed: %v", err)
	}
	res1 := res1Raw.(PiDelegateResult)
	if res1.SessionID != "pi-sess-1" || res1.Response != "TURN1" || res1.Sandbox != "read-only" {
		t.Fatalf("unexpected turn 1 result: %+v", res1)
	}

	res2Raw, err := d.CallTool(context.Background(), "a2a_delegate_to_agent", map[string]any{
		"agent_id":     "pi",
		"session_name": "pi-review",
		"sandbox":      "workspace-write",
		"prompt":       "second turn",
	})
	if err != nil {
		t.Fatalf("turn 2 failed: %v", err)
	}
	res2 := res2Raw.(PiDelegateResult)
	if res2.SessionID != "pi-sess-1" || res2.Response != "TURN2" || res2.Sandbox != "workspace-write" {
		t.Fatalf("unexpected turn 2 result: %+v", res2)
	}

	callsRaw, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(callsRaw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 pi calls, got %d: %s", len(lines), callsRaw)
	}
	if !strings.Contains(lines[0], "--tools read,grep,find,ls") {
		t.Fatalf("turn 1 should enforce read-only tools, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "--session-id pi-sess-1") || strings.Contains(lines[1], "--tools") {
		t.Fatalf("turn 2 should resume session without --tools, got: %s", lines[1])
	}
}

func TestDelegateAgentErrorsAndCommand(t *testing.T) {
	dir := t.TempDir()
	agentsFile := filepath.Join(dir, "agents.json")
	agentsJSON := `[
		{"id":"noexec","name":"NoExec","endpoint":"none"},
		{"id":"echoer","name":"Echoer","endpoint":"echo","executor":{"type":"command","command":["/bin/echo"]}}
	]`
	if err := os.WriteFile(agentsFile, []byte(agentsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_AGENTS_FILE", agentsFile)

	d := &Dispatcher{
		Board:      board.New(filepath.Join(dir, "board.json")),
		DefaultCwd: dir,
		Caller:     "codex",
	}

	if _, err := d.CallTool(context.Background(), "a2a_delegate_to_agent", map[string]any{
		"agent_id": "unknown-id",
		"prompt":   "hi",
	}); err == nil {
		t.Fatal("expected error for unknown agent_id")
	}
	if _, err := d.CallTool(context.Background(), "a2a_delegate_to_agent", map[string]any{
		"agent_id": "noexec",
		"prompt":   "hi",
	}); err == nil {
		t.Fatal("expected error for agent without executor")
	}

	resRaw, err := d.CallTool(context.Background(), "a2a_delegate_to_agent", map[string]any{
		"agent_id": "echoer",
		"prompt":   "hello-cmd",
	})
	if err != nil {
		t.Fatalf("command executor failed: %v", err)
	}
	res := resRaw.(CommandDelegateResult)
	if res.Response != "hello-cmd" {
		t.Fatalf("Response = %q, want hello-cmd", res.Response)
	}
}
