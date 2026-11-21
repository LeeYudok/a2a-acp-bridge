package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHelperBoardSubprocessWriter(t *testing.T) {
	if os.Getenv("GO_WANT_BOARD_WRITER") != "1" {
		return
	}
	store := os.Getenv("GO_BOARD_WRITER_STORE")
	workerID := os.Getenv("GO_BOARD_WRITER_ID")
	board := NewBoard(store)
	for i := range 20 {
		msg := fmt.Sprintf("proc-%s-msg-%02d", workerID, i)
		if _, err := board.SendMessage("agy", "codex", msg, "sync", ""); err != nil {
			fmt.Fprintf(os.Stderr, "subprocess SendMessage 실패: %v\n", err)
			os.Exit(1)
		}
	}
	os.Exit(0)
}

func TestBoardConcurrentWritesGoroutines(t *testing.T) {
	store := filepath.Join(t.TempDir(), "board.json")
	const workers = 16
	const perWorker = 10

	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for w := range workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			b := NewBoard(store)
			for i := range perWorker {
				body := fmt.Sprintf("worker-%02d-msg-%02d", id, i)
				if _, err := b.SendMessage("agy", "codex", body, "sync", ""); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("동시 SendMessage 실패: %v", err)
	}

	msgs, err := NewBoard(store).ReceiveMessages("codex", false, false, 0)
	if err != nil {
		t.Fatalf("ReceiveMessages 실패: %v", err)
	}
	if got, want := len(msgs), workers*perWorker; got != want {
		t.Fatalf("메시지 수 = %d, 원하는 값 %d", got, want)
	}
	assertNoLeftoverTempFiles(t, store)
}

func TestBoardConcurrentWritesMultiProcess(t *testing.T) {
	store := filepath.Join(t.TempDir(), "board.json")
	const procs = 4
	const perProc = 20

	cmds := make([]*exec.Cmd, 0, procs)
	for p := range procs {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperBoardSubprocessWriter$")
		cmd.Env = append(os.Environ(),
			"GO_WANT_BOARD_WRITER=1",
			"GO_BOARD_WRITER_STORE="+store,
			fmt.Sprintf("GO_BOARD_WRITER_ID=%d", p),
		)
		if err := cmd.Start(); err != nil {
			t.Fatalf("서브프로세스 %d 시작 실패: %v", p, err)
		}
		cmds = append(cmds, cmd)
	}
	for i, cmd := range cmds {
		if outErr := cmd.Wait(); outErr != nil {
			t.Fatalf("서브프로세스 %d 실패: %v", i, outErr)
		}
	}

	msgs, err := NewBoard(store).ReceiveMessages("codex", false, false, 0)
	if err != nil {
		t.Fatalf("ReceiveMessages 실패: %v", err)
	}
	if got, want := len(msgs), procs*perProc; got != want {
		t.Fatalf("멀티프로세스 메시지 수 = %d, 원하는 값 %d", got, want)
	}
	assertValidBoardJSON(t, store)
	assertNoLeftoverTempFiles(t, store)
}

func TestACPDelegateAppliesModeAndModelBeforePrompt(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "acp_methods.log")
	t.Setenv("GO_WANT_MOCK_ACP", "1")
	t.Setenv("GO_WANT_MOCK_ACP_LOG", logPath)

	pool := NewSessionPool([]string{os.Args[0], "-test.run=^TestHelperMockACPServer$"})
	t.Cleanup(pool.CloseAll)
	d := &Dispatcher{Board: NewBoard(filepath.Join(t.TempDir(), "board.json")), Pool: pool, DefaultCwd: t.TempDir()}
	ctx := context.Background()

	if _, err := d.CallTool(ctx, "acp_delegate_to_agy", map[string]any{
		"prompt": "t1", "session_name": "sync-check", "mode": "plan", "model": "gemini-4-neon",
	}); err != nil {
		t.Fatalf("1턴 위임 실패: %v", err)
	}
	methods1 := readMethodLog(t, logPath)
	modeIdx := slices.Index(methods1, "session/set_mode")
	modelIdx := slices.Index(methods1, "session/set_config_option")
	promptIdx := slices.Index(methods1, "session/prompt")
	if modeIdx < 0 || modelIdx < 0 || promptIdx < 0 || modeIdx > promptIdx || modelIdx > promptIdx {
		t.Fatalf("1턴 호출 순서 잘못됨: %v", methods1)
	}

	if _, err := d.CallTool(ctx, "acp_delegate_to_agy", map[string]any{
		"prompt": "t2", "session_name": "sync-check", "mode": "plan", "model": "gemini-4-neon",
	}); err != nil {
		t.Fatalf("2턴 위임 실패: %v", err)
	}
	methods2 := readMethodLog(t, logPath)
	if diff := methods2[len(methods1):]; !slices.Equal(diff, []string{"session/prompt"}) {
		t.Fatalf("2턴 동일 모드·모델 재호출 발생: %v", diff)
	}

	if _, err := d.CallTool(ctx, "acp_delegate_to_agy", map[string]any{
		"prompt": "t3", "session_name": "sync-check", "mode": "auto",
	}); err != nil {
		t.Fatalf("3턴 위임 실패: %v", err)
	}
	methods3 := readMethodLog(t, logPath)[len(methods2):]
	if !slices.Equal(methods3, []string{"session/set_mode", "session/prompt"}) {
		t.Fatalf("3턴 모드 변경 순서 잘못됨: %v", methods3)
	}
}

func TestCLIFlagsTimeoutCwdAndSyncIntent(t *testing.T) {
	if !hasSyncIntent(MCPTools()) {
		t.Fatal("a2a_send_message intent enum에 sync가 없습니다")
	}
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

	t.Setenv("GO_WANT_MOCK_ACP", "1")
	pool := NewSessionPool([]string{os.Args[0], "-test.run=^TestHelperMockACPServer$"})
	t.Cleanup(pool.CloseAll)
	customDir := t.TempDir()
	d := &Dispatcher{Board: NewBoard(filepath.Join(t.TempDir(), "board.json")), Pool: pool, DefaultCwd: t.TempDir()}

	if err := runAskAgyCmd(context.Background(), d, d.DefaultCwd, []string{
		"-s", "cli-check", "-cwd", customDir, "-timeout", "5m", "-mode", "plan", "CLI 테스트",
	}); err != nil {
		t.Fatalf("runAskAgyCmd 실패: %v", err)
	}
	snaps := pool.Snapshots()
	if len(snaps) != 1 || snaps[0].Cwd != customDir || snaps[0].Mode != "plan" {
		t.Fatalf("워커 스냅샷 불일치: %+v (원하는 cwd=%s)", snaps, customDir)
	}
	if err := runAskAgyCmd(context.Background(), d, d.DefaultCwd, []string{"-timeout", "0s", "실패"}); err == nil {
		t.Fatal("ask-agy 0s 타임아웃은 실패해야 합니다")
	}
	if err := runAskCodexCmd(context.Background(), d, d.DefaultCwd, []string{"-timeout", "-1s", "실패"}); err == nil {
		t.Fatal("ask-codex 음수 타임아웃은 실패해야 합니다")
	}
	if err := runSessionCtrlCmd(context.Background(), d, d.DefaultCwd, "set-mode", []string{
		"-s", "cli-check", "-cwd", customDir, "-timeout", "2m", "-mode", "auto",
	}); err != nil {
		t.Fatalf("runSessionCtrlCmd 실패: %v", err)
	}
	t.Setenv("GO_WANT_MOCK_ACP_LIST_ONLY", "1")
	if err := runAgySessionsCmd(context.Background(), pool, d.DefaultCwd, []string{
		"-cwd", customDir, "-timeout", "2m", "-cursor", "previous-page",
	}); err != nil {
		t.Fatalf("runAgySessionsCmd 실패: %v", err)
	}
}

func hasSyncIntent(tools []ToolSchema) bool {
	for _, tool := range tools {
		if tool.Name != "a2a_send_message" {
			continue
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		intent, _ := props["intent"].(map[string]any)
		enums, _ := intent["enum"].([]string)
		return slices.Contains(enums, "sync")
	}
	return false
}

func readMethodLog(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ACP 로그 읽기 실패: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func assertValidBoardJSON(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("보드 파일 읽기 실패: %v", err)
	}
	var st boardState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("보드 JSON 손상: %v", err)
	}
}

func assertNoLeftoverTempFiles(t *testing.T, store string) {
	t.Helper()
	matches, err := filepath.Glob(store + ".tmp.*")
	if err != nil {
		t.Fatalf("임시 파일 검색 실패: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("정리되지 않은 임시 파일 발견: %v", matches)
	}
}
