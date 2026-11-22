package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

func TestDelegationHeartbeatRefreshesWorkingTask(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("A2A_POOL_FILE", filepath.Join(dir, "pool.json"))
	b := board.New(filepath.Join(dir, "board.json"))
	msg, err := b.StartDelegation("user", "codex", "work", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := b.GetTask(msg.TaskID)
	if err != nil {
		t.Fatal(err)
	}

	touched := make(chan error, 1)
	stop := runHeartbeat(context.Background(), time.Millisecond, func() {
		err := b.TouchDelegation(msg.TaskID)
		select {
		case touched <- err:
		default:
		}
	})
	defer stop()
	select {
	case err := <-touched:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("heartbeat did not refresh the working task")
	}
	after, err := b.GetTask(msg.TaskID)
	if err != nil || after.UpdatedAt <= before.UpdatedAt || after.State != "working" {
		t.Fatalf("before = %+v, after = %+v, err = %v", before, after, err)
	}
}

func TestCodexKeepsResponseIfBoardCompletionFails(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "board.json")
	t.Setenv("A2A_POOL_FILE", filepath.Join(dir, "pool.json"))
	t.Setenv("A2A_CODEX_AGENT_ID", "")
	t.Setenv("POOL_TEST_BOARD_PATH", boardPath)
	script := filepath.Join(dir, "fake-codex")
	content := `#!/bin/sh
printf 'broken file' > "$POOL_TEST_BOARD_PATH"
printf '%s\n' '{"type":"thread.started","thread_id":"th-result"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"still available"}}'
`
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A2A_CODEX_COMMAND", script)
	d := &Dispatcher{Board: board.New(boardPath), DefaultCwd: dir}
	got, err := d.callDelegateCodex(context.Background(), map[string]any{"prompt": "work"})
	if err != nil || got.Response != "still available" || !strings.Contains(got.BoardWarning, "JSON") {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
}
