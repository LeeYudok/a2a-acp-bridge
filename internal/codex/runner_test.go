package codex

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

func TestRunUsesTargetCodexHome(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	script := filepath.Join(t.TempDir(), "codex-home")
	body := "#!/bin/sh\n" +
		"session='<unset>'; [ \"${CODEX_SESSION_ID+x}\" = x ] && session=\"$CODEX_SESSION_ID\"\n" +
		"thread='<unset>'; [ \"${CODEX_THREAD_ID+x}\" = x ] && thread=\"$CODEX_THREAD_ID\"\n" +
		"tool='<unset>'; [ \"${CODEX_TOOL_CALL_ID+x}\" = x ] && tool=\"$CODEX_TOOL_CALL_ID\"\n" +
		`printf '%s\n' '{"type":"thread.started","thread_id":"th-home"}'` + "\n" +
		`printf '{"type":"item.completed","item":{"type":"agent_message","text":"home=%s;session=%s;thread=%s;tool=%s"}}\n' "$CODEX_HOME" "$session" "$thread" "$tool"` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	parentHome := filepath.Join(t.TempDir(), "deepseek-home")
	targetHome := filepath.Join(t.TempDir(), "official-home ")
	workDir := t.TempDir()
	t.Setenv("A2A_CODEX_COMMAND", script)
	t.Setenv("A2A_CODEX_HOME", targetHome)
	t.Setenv("CODEX_HOME", parentHome)
	t.Setenv("CODEX_SESSION_ID", "parent-session")
	t.Setenv("CODEX_THREAD_ID", "parent-thread")
	t.Setenv("CODEX_TOOL_CALL_ID", "parent-tool")

	res, err := Run(context.Background(), workDir, "read-only", "질문", "")
	if err != nil {
		t.Fatalf("Run 실패: %v", err)
	}
	want := "home=" + targetHome + ";session=<unset>;thread=<unset>;tool=<unset>"
	if res.Response != want {
		t.Fatalf("자식 환경 = %q, 원하는 값 %q", res.Response, want)
	}
	if got := os.Getenv("CODEX_HOME"); got != parentHome {
		t.Fatalf("부모 CODEX_HOME = %q, 원하는 값 %q", got, parentHome)
	}
}

func TestChildEnvSetsPWD(t *testing.T) {
	workDir := t.TempDir()
	env := childEnv(workDir)
	if got := envValue(env, "PWD"); got != workDir {
		t.Fatalf("PWD = %q, 원하는 값 %q", got, workDir)
	}
}

func TestRunKeepsParentCodexHomeWithoutTarget(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	script := filepath.Join(t.TempDir(), "codex-home")
	body := "#!/bin/sh\n" +
		`printf '%s\n' '{"type":"thread.started","thread_id":"th-parent-home"}'` + "\n" +
		`printf '{"type":"item.completed","item":{"type":"agent_message","text":"%s"}}\n' "$CODEX_HOME"` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	parentHome := filepath.Join(t.TempDir(), "parent-home")
	t.Setenv("A2A_CODEX_COMMAND", script)
	t.Setenv("A2A_CODEX_HOME", "")
	t.Setenv("CODEX_HOME", parentHome)

	res, err := Run(context.Background(), t.TempDir(), "read-only", "질문", "")
	if err != nil {
		t.Fatalf("Run 실패: %v", err)
	}
	if res.Response != parentHome {
		t.Fatalf("자식 CODEX_HOME = %q, 원하는 값 %q", res.Response, parentHome)
	}
}

func TestResumeUsesSafeReadOnlyDefault(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	script := filepath.Join(t.TempDir(), "codex-resume")
	body := "#!/bin/sh\n" +
		`printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'` + "\n" +
		`printf '{"type":"item.completed","item":{"type":"agent_message","text":"%s"}}\n' "$*"` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_CODEX_COMMAND", script)
	_, msgs, err := runOnce(context.Background(), t.TempDir(), "", "질문", "thread-1")
	if err != nil {
		t.Fatalf("runOnce 실패: %v", err)
	}
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "-s read-only exec resume ") || !strings.Contains(msgs[0], "thread-1") {
		t.Fatalf("재개 인자 = %q, 원하는 값 -s read-only exec resume ... thread-1", msgs)
	}
}

func TestResumePassesExplicitSandbox(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	script := filepath.Join(t.TempDir(), "codex-resume")
	body := "#!/bin/sh\n" +
		`printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'` + "\n" +
		`printf '{"type":"item.completed","item":{"type":"agent_message","text":"%s"}}\n' "$*"` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_CODEX_COMMAND", script)
	_, msgs, err := runOnce(context.Background(), t.TempDir(), "read-only", "질문", "thread-1")
	if err != nil {
		t.Fatalf("runOnce 실패: %v", err)
	}
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "-s read-only exec resume ") || !strings.Contains(msgs[0], "thread-1") {
		t.Fatalf("재개 인자 = %q, 원하는 값 -s read-only exec resume ... thread-1", msgs)
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
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

const stderrEventID = `"thread_id":"stderr"`

func TestRunOnceStreamsAndErrors(t *testing.T) {
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
			if err := os.WriteFile(script, []byte("#!/bin/sh\n"+tc.script+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("A2A_CODEX_COMMAND", "codex-test")
			t.Setenv("PATH", filepath.Dir(script))
			id, _, err := runOnce(context.Background(), "", "", "", "")
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

func TestRunOnceCanceled(t *testing.T) {
	t.Setenv("A2A_CODEX_COMMAND", os.Args[0])
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := runOnce(ctx, "", "", "", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, expected context.Canceled", err)
	}
}

func TestRunPropagatesCommandFailure(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fail-codex")
	body := "#!/bin/sh\necho boom >&2\nexit 3\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_CODEX_COMMAND", script)
	_, err := Run(context.Background(), t.TempDir(), "read-only", "질문", "")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Run error = %v, expected command failure with stderr", err)
	}
}

func TestRunPropagatesFollowupFailure(t *testing.T) {
	cwd := t.TempDir()
	gateDir := filepath.Join(cwd, ".claude", "state", "personal-gate")
	if err := os.MkdirAll(gateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gateDir, "current"), []byte("dok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "gate-codex")
	body := "#!/bin/sh\n" +
		"case \"$*\" in *resume*) echo followup-failed >&2; exit 1;; esac\n" +
		`echo '{"type":"thread.started","thread_id":"th-gate"}'` + "\n" +
		`echo '{"type":"item.completed","item":{"type":"agent_message","text":"사용자를 확인해 주세요"}}'` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_CODEX_COMMAND", script)
	_, err := Run(context.Background(), cwd, "read-only", "질문", "")
	if err == nil || !strings.Contains(err.Error(), "followup-failed") {
		t.Fatalf("Run error = %v, expected follow-up failure to propagate", err)
	}
}
