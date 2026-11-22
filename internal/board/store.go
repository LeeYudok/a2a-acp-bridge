package board

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func newID(prefix string) (string, error) {
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("난수 ID 생성 실패: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(buf), nil
}

func nowSeconds() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func (b *Board) withLock(fn func() error) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	unlock, err := lockFile(b.path)
	if err != nil {
		return err
	}
	defer unlock()

	return fn()
}

func (b *Board) load() (boardState, error) {
	st := boardState{Tasks: []Task{}, Messages: []Message{}, Sessions: []NamedSession{}}
	raw, err := os.ReadFile(b.path)
	if errors.Is(err, os.ErrNotExist) || len(raw) == 0 {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("보드 파일 읽기 실패: %w", err)
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, fmt.Errorf("보드 JSON 파싱 실패: %w", err)
	}
	return normalizeBoardState(st), nil
}

func (b *Board) save(st boardState) error {
	st.Coordinator = nil
	return writeAtomicJSON(b.path, st, "보드")
}

func writeAtomicJSON(path string, value any, label string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%s 디렉터리 생성 실패: %w", label, err)
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("%s 직렬화 실패: %w", label, err)
	}
	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("임시 %s 파일 생성 실패: %w", label, err)
	}
	tmpName := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := writeTempFile(tmpFile, raw, label); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("%s 파일 원자 교체 실패: %w", label, err)
	}
	cleanup = false
	return nil
}

func writeTempFile(f *os.File, raw []byte, label string) error {
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("임시 %s 파일 권한 설정 실패: %w", label, err)
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return fmt.Errorf("임시 %s 파일 쓰기 실패: %w", label, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("임시 %s 파일 닫기 실패: %w", label, err)
	}
	return nil
}
