package board

import (
	"path/filepath"
	"testing"
)

func TestSnapshotReturnsDeepCopyWithoutMutatingBoard(t *testing.T) {
	b := New(filepath.Join(t.TempDir(), "board.json"))
	if _, err := b.SendMessage("agy", "codex", "스냅샷 테스트", "sync", ""); err != nil {
		t.Fatalf("SendMessage 실패: %v", err)
	}
	snap, err := b.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot 실패: %v", err)
	}
	if got, want := len(snap.Tasks), 1; got != want {
		t.Fatalf("태스크 수 불일치: got=%d want=%d", got, want)
	}
	if got, want := len(snap.Messages), 1; got != want {
		t.Fatalf("메시지 수 불일치: got=%d want=%d", got, want)
	}
	if snap.Messages[0].Content != "스냅샷 테스트" {
		t.Fatalf("메시지 내용 불일치: %q", snap.Messages[0].Content)
	}
	snap.Messages[0].Content = "사본을 바꿔 봄"
	snap.Tasks[0].State = "completed"
	again, err := b.Snapshot()
	if err != nil {
		t.Fatalf("재조회 Snapshot 실패: %v", err)
	}
	if again.Messages[0].Content != "스냅샷 테스트" {
		t.Fatalf("사본 수정이 보드에 반영됨: %q", again.Messages[0].Content)
	}
	if again.Tasks[0].State == "completed" {
		t.Fatalf("사본 상태 수정이 보드에 반영됨: %q", again.Tasks[0].State)
	}
}

func TestSnapshotIncludesCoordinatorDeepCopy(t *testing.T) {
	b := New(filepath.Join(t.TempDir(), "board.json"))
	if _, err := b.ClaimCoordinator("agy", 0, DefaultLeaseTTL, "초기 지휘", []string{"agy", "codex"}, false); err != nil {
		t.Fatalf("ClaimCoordinator 실패: %v", err)
	}
	snap, err := b.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot 실패: %v", err)
	}
	if snap.Coordinator == nil || snap.Coordinator.Leader != "agy" || snap.Coordinator.Term != 1 {
		t.Fatalf("Snapshot coordinator = %+v", snap.Coordinator)
	}
	snap.Coordinator.Leader = "codex"
	snap.Coordinator.ActiveAgents[0] = "claude"
	again, err := b.Snapshot()
	if err != nil {
		t.Fatalf("재조회 Snapshot 실패: %v", err)
	}
	if again.Coordinator == nil || again.Coordinator.Leader != "agy" || again.Coordinator.ActiveAgents[0] != "agy" {
		t.Fatalf("Snapshot 사본 수정이 보드에 반영됨: %+v", again.Coordinator)
	}
}
