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

	// 잘못된 expected_term 은 ErrTermMismatch 반환
	if _, err := b.ClaimCoordinator("codex", 0, 30*time.Minute, "구버전 CAS", nil, true); !errors.Is(err, ErrTermMismatch) {
		t.Fatalf("expectedTerm 불일치는 ErrTermMismatch 여야 합니다: %v", err)
	}

	// 지휘자가 아닌 에이전트의 renew/handoff 차단
	if _, err := b.RenewCoordinator("codex", 10*time.Minute); !errors.Is(err, ErrNotCoordinator) {
		t.Fatalf("비지휘자 Renew 는 ErrNotCoordinator 여야 합니다: %v", err)
	}

	// 현 지휘자 리스 연장
	if _, err := b.RenewCoordinator("agy", 45*time.Minute); err != nil {
		t.Fatalf("RenewCoordinator(agy) 실패: %v", err)
	}

	// 장애 시 force=true 페일오버로 N-에이전트(hermes) 승계 가능
	l2, err := b.ClaimCoordinator("hermes", 1, 20*time.Minute, "agy 응답 없음", []string{"codex", "claude", "hermes"}, true)
	if err != nil {
		t.Fatalf("force failover 실패: %v", err)
	}
	if l2.Term != 2 || l2.Leader != "hermes" || l2.PreviousLeader != "agy" {
		t.Fatalf("페일오버 결과 불일치: %+v", l2)
	}

	// 개방 이양(*) 후 대기 없이 opencode 가 승계
	openHandoff, err := b.HandoffCoordinator("hermes", "", "할당량 소진", 0)
	if err != nil || openHandoff.HandedOffTo != "*" {
		t.Fatalf("개방 이양 실패: %+v, err=%v", openHandoff, err)
	}
	l3, err := b.ClaimCoordinator("opencode", 2, 15*time.Minute, "이양 승계", nil, false)
	if err != nil || l3.Term != 3 || l3.Leader != "opencode" {
		t.Fatalf("개방 이양 승계 실패: %+v, err=%v", l3, err)
	}

	// 직접 이양
	l4, err := b.HandoffCoordinator("opencode", "claude", "직접 이양", 30*time.Minute)
	if err != nil || l4.Term != 4 || l4.Leader != "claude" || l4.PreviousLeader != "opencode" {
		t.Fatalf("직접 이양 실패: %+v, err=%v", l4, err)
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
