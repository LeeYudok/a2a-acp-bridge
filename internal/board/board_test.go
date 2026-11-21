package board

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestHelperBoardSubprocessWriter(t *testing.T) {
	if os.Getenv("GO_WANT_BOARD_WRITER") != "1" {
		return
	}
	store := os.Getenv("GO_BOARD_WRITER_STORE")
	workerID := os.Getenv("GO_BOARD_WRITER_ID")
	b := New(store)
	for i := range 20 {
		msg := fmt.Sprintf("proc-%s-msg-%02d", workerID, i)
		if _, err := b.SendMessage("agy", "codex", msg, "sync", ""); err != nil {
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
			b := New(store)
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

	msgs, err := New(store).ReceiveMessages("codex", false, false, 0)
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

	msgs, err := New(store).ReceiveMessages("codex", false, false, 0)
	if err != nil {
		t.Fatalf("ReceiveMessages 실패: %v", err)
	}
	if got, want := len(msgs), procs*perProc; got != want {
		t.Fatalf("멀티프로세스 메시지 수 = %d, 원하는 값 %d", got, want)
	}
	assertValidBoardJSON(t, store)
	assertNoLeftoverTempFiles(t, store)
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
