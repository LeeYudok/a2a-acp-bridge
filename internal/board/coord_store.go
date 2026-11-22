package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

type coordState struct {
	Coordinator *CoordinatorLease `json:"coordinator,omitempty"`
}

func (b *Board) coordPath() string {
	return filepath.Join(filepath.Dir(b.path), "coord.json")
}

func (b *Board) withCoordLock(fn func() error) error {
	b.coordMu.Lock()
	defer b.coordMu.Unlock()

	unlock, err := lockFile(b.coordPath())
	if err != nil {
		return err
	}
	defer unlock()

	return fn()
}

func (b *Board) loadCoordFile() (*CoordinatorLease, bool, error) {
	raw, err := os.ReadFile(b.coordPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("coord 파일 읽기 실패: %w", err)
	}
	if len(raw) == 0 {
		return nil, true, nil
	}
	var st coordState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, true, fmt.Errorf("coord JSON 파싱 실패: %w", err)
	}
	return cloneCoordinator(st.Coordinator), true, nil
}

func (b *Board) saveCoordFile(lease *CoordinatorLease) error {
	return writeAtomicJSON(b.coordPath(), coordState{Coordinator: cloneCoordinator(lease)}, "coord")
}

// ensureCoordMigrated 는 #20 이전 보드의 리스를 coord.json 으로 한 번만 옮긴다.
// 보드와 coord 락을 동시에 잡지 않아 기존 보드 쓰기와 교착되지 않는다.
func (b *Board) ensureCoordMigrated() error {
	_, exists, err := b.loadCoordFile()
	if err != nil || exists {
		return err
	}

	var legacy *CoordinatorLease
	err = b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		legacy = cloneCoordinator(st.Coordinator)
		return nil
	})
	if err != nil || legacy == nil {
		return err
	}

	return b.withCoordLock(func() error {
		_, exists, err := b.loadCoordFile()
		if err != nil || exists {
			return err
		}
		return b.saveCoordFile(legacy)
	})
}

func cloneCoordinator(lease *CoordinatorLease) *CoordinatorLease {
	if lease == nil {
		return nil
	}
	copied := *lease
	copied.ActiveAgents = slices.Clone(lease.ActiveAgents)
	return &copied
}
