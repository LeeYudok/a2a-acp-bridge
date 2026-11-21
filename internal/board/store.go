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

	unlock, err := lockBoardFile(b.path)
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
	dir := filepath.Dir(b.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("보드 디렉터리 생성 실패: %w", err)
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("보드 직렬화 실패: %w", err)
	}
	tmpFile, err := os.CreateTemp(dir, filepath.Base(b.path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("임시 보드 파일 생성 실패: %w", err)
	}
	tmpName := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := writeBoardTempFile(tmpFile, raw); err != nil {
		return err
	}
	if err := os.Rename(tmpName, b.path); err != nil {
		return fmt.Errorf("보드 파일 원자 교체 실패: %w", err)
	}
	cleanup = false
	return nil
}

func writeBoardTempFile(f *os.File, raw []byte) error {
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("임시 보드 파일 권한 설정 실패: %w", err)
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return fmt.Errorf("임시 보드 파일 쓰기 실패: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("임시 보드 파일 닫기 실패: %w", err)
	}
	return nil
}
