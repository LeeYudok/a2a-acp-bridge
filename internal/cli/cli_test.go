package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeeYudok/a2a-acp-bridge/internal/acp"
	"github.com/LeeYudok/a2a-acp-bridge/internal/acp/acptest"
	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
	"github.com/LeeYudok/a2a-acp-bridge/internal/dispatch"
)

func TestMain(m *testing.M) { acptest.Main(m) }

func TestWithCommandTimeout(t *testing.T) {
	longCtx, cancel, err := withCommandTimeout(context.Background(), 5*time.Minute)
	if err != nil {
		t.Fatalf("5분 타임아웃 생성 실패: %v", err)
	}
	defer cancel()
	if dl, ok := longCtx.Deadline(); !ok || time.Until(dl) <= 4*time.Minute {
		t.Fatalf("5분 타임아웃 데드라인 미적용: ok=%v, dl=%v", ok, dl)
	}
	if _, _, err := withCommandTimeout(context.Background(), 0); err == nil {
		t.Fatal("0 타임아웃은 오류를 반환해야 합니다")
	}
}

func TestCLIFlagsTimeoutAndCwd(t *testing.T) {
	pool := acp.NewSessionPool(acptest.Command(t))
	t.Cleanup(pool.CloseAll)
	customDir := t.TempDir()
	d := &dispatch.Dispatcher{Board: board.New(filepath.Join(t.TempDir(), "board.json")), Pool: pool, DefaultCwd: t.TempDir()}
	ctx := context.Background()

	if err := runAskAgyCmd(ctx, d, d.DefaultCwd, []string{
		"-s", "cli-check", "-cwd", customDir, "-timeout", "5m", "-mode", "plan", "CLI 테스트",
	}); err != nil {
		t.Fatalf("runAskAgyCmd 실패: %v", err)
	}
	snaps := pool.Snapshots()
	if len(snaps) != 1 || snaps[0].Cwd != customDir || snaps[0].Mode != "plan" {
		t.Fatalf("워커 스냅샷 불일치: %+v (원하는 cwd=%s)", snaps, customDir)
	}
	if err := runAskAgyCmd(ctx, d, d.DefaultCwd, []string{"-timeout", "0s", "실패"}); err == nil {
		t.Fatal("ask-agy 0s 타임아웃은 실패해야 합니다")
	}
	if err := runAskCodexCmd(ctx, d, d.DefaultCwd, []string{"-timeout", "-1s", "실패"}); err == nil {
		t.Fatal("ask-codex 음수 타임아웃은 실패해야 합니다")
	}
	if err := runSessionCtrlCmd(ctx, d, d.DefaultCwd, "set-mode", []string{
		"-s", "cli-check", "-cwd", customDir, "-timeout", "2m", "-mode", "auto",
	}); err != nil {
		t.Fatalf("runSessionCtrlCmd 실패: %v", err)
	}
	acptest.ListOnly(t)
	if err := runAgySessionsCmd(ctx, pool, d.DefaultCwd, []string{
		"-cwd", customDir, "-timeout", "2m", "-cursor", "previous-page",
	}); err != nil {
		t.Fatalf("runAgySessionsCmd 실패: %v", err)
	}
}
