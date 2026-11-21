package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/LeeYudok/a2a-acp-bridge/internal/agents"
	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

func TestCoordinatorLeaseMCPDispatch(t *testing.T) {
	b := board.New(filepath.Join(t.TempDir(), "board.json"))
	d := &Dispatcher{Board: b, Caller: "agy", DefaultCwd: t.TempDir()}
	ctx := context.Background()

	// 초기 상태: coordinator 없음
	res, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{"action": "status"})
	if err != nil {
		t.Fatalf("status 실패: %v", err)
	}
	if statusMap, ok := res.(map[string]any); !ok || statusMap["coordinator"].(*board.CoordinatorLease) != nil {
		t.Fatalf("초기 coordinator 는 nil 이어야 합니다: %#v", res)
	}

	// term 1 선점
	claimed, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":        "claim",
		"expected_term": float64(0),
		"ttl_seconds":   float64(1200),
		"reason":        "초기 지휘",
		"active_agents": []any{"agy", "codex", "claude", "hermes"},
	})
	if err != nil {
		t.Fatalf("claim 실패: %v", err)
	}
	lease := claimed.(board.CoordinatorLease)
	if lease.Term != 1 || lease.Leader != "agy" {
		t.Fatalf("claim 결과 불일치: %+v", lease)
	}

	// discover 결과에도 coordinator 리스가 노출돼야 함
	discAny, err := d.CallTool(ctx, "a2a_discover_agents", map[string]any{})
	if err != nil {
		t.Fatalf("discover 실패: %v", err)
	}
	disc := discAny.(agents.Discovery)
	if disc.Coordinator == nil || disc.Coordinator.Leader != "agy" || disc.Coordinator.Term != 1 {
		t.Fatalf("Discovery.Coordinator 미노출: %+v", disc.Coordinator)
	}

	// force 페일오버로 hermes 가 term 2 승계
	failedOver, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":        "claim",
		"agent_id":      "hermes",
		"expected_term": float64(1),
		"reason":        "agy 응답 타임아웃",
		"force":         true,
	})
	if err != nil {
		t.Fatalf("failover claim 실패: %v", err)
	}
	lease2 := failedOver.(board.CoordinatorLease)
	if lease2.Term != 2 || lease2.Leader != "hermes" {
		t.Fatalf("페일오버 불일치: %+v", lease2)
	}

	// 잘못된 action 은 오류 반환
	if _, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{"action": "bogus"}); err == nil {
		t.Fatal("잘못된 action 은 오류여야 합니다")
	}

	// CAS 불일치 확인
	if _, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":        "claim",
		"agent_id":      "opencode",
		"expected_term": float64(1),
		"force":         true,
	}); !errors.Is(err, board.ErrTermMismatch) {
		t.Fatalf("expected_term 불일치는 ErrTermMismatch 여야 합니다: %v", err)
	}
}
