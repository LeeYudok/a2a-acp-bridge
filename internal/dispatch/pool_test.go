package dispatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeeYudok/a2a-acp-bridge/internal/acp"
	"github.com/LeeYudok/a2a-acp-bridge/internal/agents"
	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

func TestDelegationToolsRejectFullPool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("A2A_POOL_FILE", filepath.Join(dir, "pool.json"))
	t.Setenv("A2A_AGENTS_FILE", filepath.Join(dir, "agents.json"))
	t.Setenv("A2A_CODEX_AGENT_ID", "codex-glm-2")
	if err := os.WriteFile(agents.ExtraAgentsPath(), []byte(`[
		{"id":"codex-glm","family":"codex-glm"},
		{"id":"codex-glm-2","family":"codex-glm"}
	]`), 0o600); err != nil {
		t.Fatal(err)
	}
	b := board.New(filepath.Join(dir, "board.json"))
	d := &Dispatcher{Board: b, Pool: acp.NewSessionPool(nil)}
	ctx := context.Background()
	if _, err := d.CallPool(map[string]any{"action": "set", "family": "codex-glm", "min": 0, "max": 1}, "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.UpsertTask("user", "codex-glm", "running", "working", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CallTool(ctx, "a2a_delegate_to_codex", map[string]any{"prompt": "x"}); !errors.Is(err, board.ErrPoolFull) {
		t.Fatalf("codex route error = %v", err)
	}
	if _, err := d.CallPool(map[string]any{"action": "set", "max_total": 1}, "user"); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"a2a_delegate_to_codex", "a2a_delegate_to_claude", "acp_delegate_to_agy"} {
		for _, async := range []bool{false, true} {
			if _, err := d.CallTool(ctx, tool, map[string]any{"prompt": "x", "async": async}); !errors.Is(err, board.ErrPoolFull) {
				t.Errorf("%s async=%t error = %v", tool, async, err)
			}
		}
	}
	tasks, err := b.ListTasks(0)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks = %v, %v", tasks, err)
	}
}

func TestPoolToolIgnoresRequestIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("A2A_POOL_FILE", filepath.Join(dir, "pool.json"))
	d := &Dispatcher{Board: board.New(filepath.Join(dir, "board.json")), Caller: "user"}
	args := map[string]any{"action": "set", "max_total": 2, "from_agent": "user"}
	if _, err := d.CallTool(context.Background(), "a2a_pool", args); !errors.Is(err, board.ErrPoolSetForbidden) {
		t.Fatalf("set error = %v", err)
	}
}
