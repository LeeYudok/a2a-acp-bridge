//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package board

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockBoardFile(boardPath string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(boardPath), 0o755); err != nil {
		return nil, fmt.Errorf("보드 락 디렉터리 생성 실패: %w", err)
	}
	f, err := os.OpenFile(boardPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("보드 락 파일 열기 실패: %w", err)
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("보드 파일 락 획득 실패: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
