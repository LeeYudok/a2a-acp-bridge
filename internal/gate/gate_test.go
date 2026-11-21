package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActiveUserAndNeedsReply(t *testing.T) {
	cwd := t.TempDir()
	if got := ActiveUser(cwd); got != "" {
		t.Fatalf("게이트 파일이 없으면 빈 문자열이어야 함: %q", got)
	}
	dir := filepath.Join(cwd, ".claude", "state", "personal-gate")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "current"), []byte("dok123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ActiveUser(cwd); got != "dok123" {
		t.Fatalf("ActiveUser = %q", got)
	}
	if !NeedsReply("먼저 사용자를 확인해 주세요") || NeedsReply("작업을 마쳤습니다") {
		t.Fatal("NeedsReply 판정이 틀림")
	}
	if f := Followup("dok123", "원래 질문"); !strings.Contains(f, "personal-layer.sh use dok123") || !strings.HasSuffix(f, "원래 질문)") {
		t.Fatalf("Followup = %q", f)
	}
}
