package board

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPoolAdmission(t *testing.T) {
	t.Setenv("A2A_POOL_FILE", filepath.Join(t.TempDir(), "pool.json"))
	b := New(filepath.Join(t.TempDir(), "board.json"))
	families := map[string]string{"codex-glm": "glm", "codex-glm-2": "glm", "agy": "agy", "claude": "claude"}
	if _, err := b.SetPool("user", "glm", &PoolBounds{Min: 1, Max: 1}, nil, families); err != nil {
		t.Fatal(err)
	}
	limit := 2
	status, err := b.SetPool("user", "", nil, &limit, families)
	if err != nil || !status.Families["glm"].BelowMin {
		t.Fatalf("status = %+v, %v", status, err)
	}
	first, err := b.StartDelegation("user", "codex-glm", "work", "thread", families)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.StartDelegation("user", "codex-glm-2", "denied", "", families); !errors.Is(err, ErrPoolFull) || !strings.Contains(err.Error(), "family=glm running=1 max=1") {
		t.Fatalf("family denial = %v", err)
	}
	if _, err := b.StartDelegation("user", "agy", "other family", "", families); err != nil {
		t.Fatal(err)
	}
	if _, err := b.StartDelegation("user", "claude", "denied", "", families); !errors.Is(err, ErrPoolFull) || !strings.Contains(err.Error(), "total_running=2 max_total=2") {
		t.Fatalf("total denial = %v", err)
	}
	raw, status, err := b.SnapshotWithPool(families)
	if err != nil || len(raw.Tasks) != 2 || len(raw.Messages) != 2 || status.Running != 2 || *status.Families["claude"].FreeSlots != 0 {
		t.Fatalf("snapshot = %+v, pool = %+v, err = %v", raw, status, err)
	}
	if _, err := b.CompleteDelegation("codex-glm", "user", "done", first.TaskID, "thread"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.StartDelegation("user", "codex-glm-2", "released slot", "", families); err != nil {
		t.Fatal(err)
	}
}

func TestPoolMissingFileAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.json")
	t.Setenv("A2A_POOL_FILE", path)
	b := New(filepath.Join(t.TempDir(), "board.json"))
	families := map[string]string{"codex": "codex"}
	for range 3 {
		if _, err := b.StartDelegation("user", "codex", "unlimited", "", families); err != nil {
			t.Fatal(err)
		}
	}
	status, err := b.PoolStatus(families)
	if err != nil || status.Running != 3 || status.MaxTotal != nil || status.Families["codex"].Max != nil {
		t.Fatalf("status = %+v, %v", status, err)
	}
	for _, invalid := range []PoolBounds{{Min: -1, Max: 2}, {Min: 2, Max: 1}, {Max: 0}} {
		if _, err := b.SetPool("user", "codex", &invalid, nil, families); err == nil {
			t.Errorf("accepted %+v", invalid)
		}
	}
	zero := 0
	if _, err := b.SetPool("user", "", nil, &zero, families); err == nil {
		t.Error("accepted zero max_total")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("settings file after rejected updates = %v", err)
	}
}

func TestPoolCoordinatorPermission(t *testing.T) {
	t.Setenv("A2A_POOL_FILE", filepath.Join(t.TempDir(), "pool.json"))
	b := New(filepath.Join(t.TempDir(), "board.json"))
	limit := 1
	if _, err := b.ClaimCoordinator("claude", 0, time.Minute, "", nil, false); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"", "codex"} {
		if _, err := b.SetPool(actor, "", nil, &limit, nil); !errors.Is(err, ErrPoolSetForbidden) {
			t.Errorf("actor %q: %v", actor, err)
		}
	}
	if _, err := b.SetPool("claude", "", nil, &limit, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.HandoffCoordinator("claude", "codex", 1, "", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetPool("claude", "", nil, &limit, nil); !errors.Is(err, ErrPoolSetForbidden) {
		t.Fatalf("former leader = %v", err)
	}
}
