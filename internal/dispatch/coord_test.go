package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

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

	// expected_term 누락·소수·잘못된 타입 거부
	if _, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":   "renew",
		"agent_id": "hermes",
	}); err == nil {
		t.Fatal("expected_term 누락 renew 는 오류여야 합니다")
	}
	if _, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":        "renew",
		"agent_id":      "hermes",
		"expected_term": float64(1.9),
	}); err == nil {
		t.Fatal("소수 expected_term(1.9) 은 거부돼야 합니다")
	}
	if _, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":        "claim",
		"agent_id":      "codex",
		"expected_term": "2",
	}); err == nil {
		t.Fatal("문자열 expected_term 은 거부돼야 합니다")
	}

	// renew/handoff 도 expected_term 을 검증해야 함
	if _, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":        "renew",
		"agent_id":      "hermes",
		"expected_term": float64(1),
	}); !errors.Is(err, board.ErrTermMismatch) {
		t.Fatalf("구버전 expected_term renew 는 ErrTermMismatch 여야 합니다: %v", err)
	}
	if _, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":        "renew",
		"agent_id":      "hermes",
		"expected_term": float64(2),
		"ttl_seconds":   float64(900),
	}); err != nil {
		t.Fatalf("정상 expected_term renew 실패: %v", err)
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

	// json.Number 는 2^53 초과 uint64 도 정밀도 손실 없이 파싱하고, lossy float64·TTL 오버플로는 거부해야 함
	if _, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":        "claim",
		"agent_id":      "codex",
		"expected_term": json.Number("18446744073709551615"),
		"force":         true,
	}); !errors.Is(err, board.ErrTermMismatch) {
		t.Fatalf("json.Number MaxUint64 는 정밀 파싱되어 ErrTermMismatch 여야 합니다: %v", err)
	}
	if _, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":        "claim",
		"agent_id":      "codex",
		"expected_term": float64(uint64(1)<<53 + 2),
		"force":         true,
	}); err == nil {
		t.Fatal("2^53 초과 float64 expected_term 은 정밀도 손실로 거부돼야 합니다")
	}
	if _, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":        "claim",
		"agent_id":      "codex",
		"expected_term": float64(2),
		"ttl_seconds":   uint64(math.MaxUint64),
	}); err == nil {
		t.Fatal("MaxUint64 ttl_seconds 는 Duration 오버플로 방지로 거부돼야 합니다")
	}
	if _, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":        "claim",
		"agent_id":      "codex",
		"expected_term": float64(2),
		"ttl_seconds":   json.Number("9223372037"),
	}); err == nil {
		t.Fatal("Duration 최댓값 초과 ttl_seconds 는 거부돼야 합니다")
	}
	if _, err := d.CallTool(ctx, "a2a_coordinator_lease", map[string]any{
		"action":        "claim",
		"agent_id":      "codex",
		"expected_term": float64(2),
		"ttl_seconds":   0,
	}); err == nil {
		t.Fatal("claim 의 ttl_seconds=0 은 거부돼야 합니다")
	}
	if _, err := parseLeaseTTL(map[string]any{"ttl_seconds": int64(300)}, "renew"); err != nil {
		t.Fatalf("정상 ttl_seconds 파싱 실패: %v", err)
	}
	_ = time.Second
}
