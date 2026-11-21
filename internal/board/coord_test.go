package board

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCoordinatorLeaseLifecycleAndFailover(t *testing.T) {
	b := New(filepath.Join(t.TempDir(), "board.json"))

	cur, err := b.GetCoordinator()
	if err != nil || cur != nil {
		t.Fatalf("초기 상태는 nil 이어야 합니다: cur=%v, err=%v", cur, err)
	}

	// term 1 선점
	l1, err := b.ClaimCoordinator("agy", 0, 30*time.Minute, "초기 지휘", []string{"agy", "codex", "claude"}, false)
	if err != nil {
		t.Fatalf("ClaimCoordinator(agy) 실패: %v", err)
	}
	if l1.Term != 1 || l1.Leader != "agy" {
		t.Fatalf("예상과 다른 리스: %+v", l1)
	}

	// 활성 리스 중 force=false 인 타 에이전트 승계는 차단되어야 함
	if _, err := b.ClaimCoordinator("codex", 1, 30*time.Minute, "무단 탈취", nil, false); !errors.Is(err, ErrLeaseActive) {
		t.Fatalf("활성 리스 중 승계 시도는 ErrLeaseActive 여야 합니다: %v", err)
	}

	// 활성 리스를 force=true 로 승계할 때 사유가 비어 있으면 차단되어야 함
	if _, err := b.ClaimCoordinator("codex", 1, 30*time.Minute, "   ", nil, true); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("사유 없는 강제 승계는 ErrReasonRequired 여야 합니다: %v", err)
	}

	// 잘못된 expected_term 은 ErrTermMismatch 반환
	if _, err := b.ClaimCoordinator("codex", 0, 30*time.Minute, "구버전 CAS", nil, true); !errors.Is(err, ErrTermMismatch) {
		t.Fatalf("expectedTerm 불일치는 ErrTermMismatch 여야 합니다: %v", err)
	}

	// 지휘자가 아닌 에이전트의 renew/handoff 차단
	if _, err := b.RenewCoordinator("codex", 1, 10*time.Minute); !errors.Is(err, ErrNotCoordinator) {
		t.Fatalf("비지휘자 Renew 는 ErrNotCoordinator 여야 합니다: %v", err)
	}

	// 잘못된 expected_term 으로 renew/handoff 시도 차단
	if _, err := b.RenewCoordinator("agy", 0, 10*time.Minute); !errors.Is(err, ErrTermMismatch) {
		t.Fatalf("구버전 term renew 는 ErrTermMismatch 여야 합니다: %v", err)
	}

	// 현 지휘자 정상 CAS 리스 연장
	if _, err := b.RenewCoordinator("agy", 1, 45*time.Minute); err != nil {
		t.Fatalf("RenewCoordinator(agy) 실패: %v", err)
	}

	// 장애 시 force=true + 사유 있음으로 N-에이전트(hermes) 승계 가능
	l2, err := b.ClaimCoordinator("hermes", 1, 20*time.Minute, "agy 응답 없음", []string{"codex", "claude", "hermes"}, true)
	if err != nil {
		t.Fatalf("force failover 실패: %v", err)
	}
	if l2.Term != 2 || l2.Leader != "hermes" || l2.PreviousLeader != "agy" {
		t.Fatalf("페일오버 결과 불일치: %+v", l2)
	}

	// 개방 이양(*) 후 이전 지휘자의 늦은 renew / 재handoff 는 차단돼야 함
	openHandoff, err := b.HandoffCoordinator("hermes", "", 2, "할당량 소진", 0)
	if err != nil || openHandoff.HandedOffTo != "*" {
		t.Fatalf("개방 이양 실패: %+v, err=%v", openHandoff, err)
	}
	if _, err := b.RenewCoordinator("hermes", 2, 10*time.Minute); !errors.Is(err, ErrLeaseHandedOff) {
		t.Fatalf("개방 이양 뒤 renew 는 ErrLeaseHandedOff 여야 합니다: %v", err)
	}
	if _, err := b.HandoffCoordinator("hermes", "codex", 2, "취소 시도", 10*time.Minute); !errors.Is(err, ErrLeaseHandedOff) {
		t.Fatalf("개방 이양 뒤 재이양은 ErrLeaseHandedOff 여야 합니다: %v", err)
	}

	// 개방 이양 후 opencode 가 대기 없이 승계
	l3, err := b.ClaimCoordinator("opencode", 2, 15*time.Minute, "이양 승계", nil, false)
	if err != nil || l3.Term != 3 || l3.Leader != "opencode" {
		t.Fatalf("개방 이양 승계 실패: %+v, err=%v", l3, err)
	}

	// 직접 이양
	l4, err := b.HandoffCoordinator("opencode", "claude", 3, "직접 이양", 30*time.Minute)
	if err != nil || l4.Term != 4 || l4.Leader != "claude" || l4.PreviousLeader != "opencode" {
		t.Fatalf("직접 이양 실패: %+v, err=%v", l4, err)
	}

	// TTL 만료 후에는 renew/handoff 불가, claim 으로만 term+1 승계 가능
	l5, err := b.HandoffCoordinator("claude", "agy", 4, "단기 리스", 1*time.Millisecond)
	if err != nil || l5.Term != 5 {
		t.Fatalf("단기 이양 실패: %+v, err=%v", l5, err)
	}
	time.Sleep(15 * time.Millisecond)
	if _, err := b.RenewCoordinator("agy", 5, 10*time.Minute); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("만료된 리스 renew 는 ErrLeaseExpired 여야 합니다: %v", err)
	}
	if _, err := b.HandoffCoordinator("agy", "codex", 5, "만료 이양", 10*time.Minute); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("만료된 리스 handoff 는 ErrLeaseExpired 여야 합니다: %v", err)
	}
	l6, err := b.ClaimCoordinator("codex", 5, 15*time.Minute, "만료 승계", nil, false)
	if err != nil || l6.Term != 6 || l6.Leader != "codex" {
		t.Fatalf("만료 후 claim 승계 실패: %+v, err=%v", l6, err)
	}
}

func TestCoordinatorConcurrentClaimSplitBrainPrevention(t *testing.T) {
	store := filepath.Join(t.TempDir(), "board.json")
	candidates := []string{"agy", "codex", "claude", "hermes", "opencode", "agent-6", "agent-7", "agent-8"}

	var wg sync.WaitGroup
	winners := make(chan string, len(candidates))
	for _, c := range candidates {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if lease, err := New(store).ClaimCoordinator(id, 0, time.Hour, "동시 경쟁", candidates, false); err == nil && lease.Term == 1 {
				winners <- id
			}
		}(c)
	}
	wg.Wait()
	close(winners)

	if got := len(winners); got != 1 {
		t.Fatalf("CAS expected_term=0 동시 경쟁 승자는 정확히 1명이어야 합니다: got=%d", got)
	}
	cur, err := New(store).GetCoordinator()
	if err != nil || cur == nil || cur.Term != 1 {
		t.Fatalf("저장된 최종 리스 확인 실패: cur=%v, err=%v", cur, err)
	}
}
