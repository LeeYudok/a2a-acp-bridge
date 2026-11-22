package board

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestCoordinatorStatus(t *testing.T) {
	now := time.Unix(1000, 0)
	cases := []struct {
		name  string
		lease *CoordinatorLease
		want  string
	}{
		{name: "없음", lease: nil, want: "vacant"},
		{name: "활성", lease: &CoordinatorLease{Leader: "codex", Term: 1, LeaseUntil: 1001}, want: "active"},
		{name: "만료", lease: &CoordinatorLease{Leader: "codex", Term: 1, LeaseUntil: 999}, want: "expired"},
		{name: "개방 이양", lease: &CoordinatorLease{Leader: "codex", Term: 1, HandedOffTo: "*"}, want: "handed_off"},
	}
	for _, tc := range cases {
		if got := CoordinatorStatus(tc.lease, now); got != tc.want {
			t.Errorf("%s: got=%q, 원하는 값 %q", tc.name, got, tc.want)
		}
	}
}

func TestCoordinatorLeaseSurvivesLegacyBoardRewrite(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "a2a_board.json")
	b := New(boardPath)
	first, err := b.ClaimCoordinator("codex", 0, time.Hour, "초기 지휘", []string{"codex", "agy"}, false)
	if err != nil {
		t.Fatalf("초기 ClaimCoordinator 실패: %v", err)
	}

	// #16 이전 바이너리가 Coordinator 필드를 모른 채 보드 전체를 다시 쓴 상황이다.
	legacyBoard := []byte(`{"tasks":[],"messages":[],"sessions":[]}`)
	if err := os.WriteFile(boardPath, legacyBoard, 0o600); err != nil {
		t.Fatalf("구버전 보드 덮어쓰기 실패: %v", err)
	}

	cur, err := New(boardPath).GetCoordinator()
	if err != nil {
		t.Fatalf("보드 덮어쓰기 뒤 GetCoordinator 실패: %v", err)
	}
	if cur == nil || cur.Term != first.Term || cur.Leader != first.Leader {
		t.Fatalf("구버전 보드 쓰기 뒤 리스가 바뀜: got=%+v want=%+v", cur, first)
	}
	if _, err := New(boardPath).ClaimCoordinator("agy", 0, time.Hour, "term 재사용", nil, false); !errors.Is(err, ErrTermMismatch) {
		t.Fatalf("기존 term 1 을 expected_term=0 으로 재사용하면 ErrTermMismatch 여야 합니다: %v", err)
	}
}

func TestCoordinatorMigratesLegacyBoardLease(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "a2a_board.json")
	legacy := boardState{Coordinator: &CoordinatorLease{
		Leader: "agy", Term: 7, LeaseUntil: float64(time.Now().Add(time.Hour).Unix()), UpdatedAt: 1,
		ActiveAgents: []string{"agy", "codex"},
	}}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("기존 보드 JSON 생성 실패: %v", err)
	}
	if err := os.WriteFile(boardPath, raw, 0o600); err != nil {
		t.Fatalf("기존 보드 쓰기 실패: %v", err)
	}

	cur, err := New(boardPath).GetCoordinator()
	if err != nil || cur == nil || cur.Term != 7 || cur.Leader != "agy" {
		t.Fatalf("기존 보드 리스 마이그레이션 실패: cur=%+v, err=%v", cur, err)
	}
	if _, err := New(boardPath).SendMessage("codex", "agy", "마이그레이션 뒤 보드 쓰기", "sync", ""); err != nil {
		t.Fatalf("마이그레이션 뒤 SendMessage 실패: %v", err)
	}
	rewritten, err := os.ReadFile(boardPath)
	if err != nil {
		t.Fatalf("갱신된 보드 읽기 실패: %v", err)
	}
	if strings.Contains(string(rewritten), `"coordinator"`) {
		t.Fatalf("새 보드 저장이 coordinator 필드를 다시 기록함: %s", rewritten)
	}
}
