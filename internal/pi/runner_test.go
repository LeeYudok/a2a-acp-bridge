package pi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOutput(t *testing.T) {
	longSystem := strings.Repeat("x", 80*1024)
	jsonl := strings.Join([]string{
		"Warning: No project session found with id 'sess-1'; creating a new session with that id.",
		`{"type":"session","version":3,"id":"sess-1","timestamp":"2026-11-28T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message_end","message":{"role":"system","content":"` + longSystem + `"}}`,
		`{"type":"message_end","message":{"role":"assistant","model":"glm","stopReason":"toolUse","content":[{"type":"thinking","thinking":"step 1"}]}}`,
		`{"type":"turn_end","message":{"role":"assistant","model":"glm","stopReason":"stop","content":[{"type":"thinking","thinking":"step 2"},{"type":"text","text":"DONE"}],"usage":{"cost":{"total":0.012}}}}`,
		`{"type":"agent_settled","aborted":false}`,
	}, "\n")

	res, err := ParseOutput([]byte(jsonl))
	if err != nil {
		t.Fatalf("ParseOutput failed: %v", err)
	}
	if res.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want sess-1", res.SessionID)
	}
	if res.Response != "DONE" {
		t.Fatalf("Response = %q, want DONE", res.Response)
	}
	if res.Model != "glm" || res.StopReason != "stop" {
		t.Fatalf("Model=%q StopReason=%q", res.Model, res.StopReason)
	}
	if res.CostUSD != 0.012 {
		t.Fatalf("CostUSD = %v, want 0.012", res.CostUSD)
	}
}

func TestParseOutputErrorsOnEmpty(t *testing.T) {
	if _, err := ParseOutput([]byte("plain text only\n")); err == nil {
		t.Fatal("expected error on non-JSON output")
	}
}

func TestRunFlagsAndSessionID(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	script := filepath.Join(dir, "fake-pi.sh")
	body := `#!/bin/sh
printf '%s\n' "$@" > "` + argsFile + `"
printf '{"type":"session","id":"sess-xyz"}\n'
printf '{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"OK"}]}}\n'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), Options{
		Command:   []string{script},
		Cwd:       dir,
		Prompt:    "-leading-dash",
		SessionID: "sess-xyz",
		Sandbox:   "read-only",
		Model:     "llmgw-glm/glm",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if res.SessionID != "sess-xyz" || res.Response != "OK" {
		t.Fatalf("unexpected result: %+v", res)
	}
	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"-p", "--mode", "json", "--session-id", "sess-xyz",
		"--tools", "read,grep,find,ls", "--model", "llmgw-glm/glm", "--", "-leading-dash",
	}, "\n") + "\n"
	if string(gotArgs) != want {
		t.Fatalf("args mismatch:\ngot:\n%s\nwant:\n%s", gotArgs, want)
	}

	if _, err := Run(context.Background(), Options{
		Command: []string{script},
		Cwd:     dir,
		Prompt:  "edit",
		Sandbox: "workspace-write",
	}); err != nil {
		t.Fatalf("workspace-write Run failed: %v", err)
	}
	gotWriteArgs, _ := os.ReadFile(argsFile)
	if strings.Contains(string(gotWriteArgs), "--tools") {
		t.Fatalf("workspace-write should not pass --tools, got:\n%s", gotWriteArgs)
	}
}
