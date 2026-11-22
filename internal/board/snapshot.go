package board

import "encoding/json"

// Snapshot 은 보드 전체(태스크·메시지·세션·지휘권 리스)의 깊은 사본이다
type Snapshot struct {
	Tasks       []Task            `json:"tasks"`
	Messages    []Message         `json:"messages"`
	Sessions    []NamedSession    `json:"sessions"`
	Coordinator *CoordinatorLease `json:"coordinator,omitempty"`
}

// Snapshot 은 현재 보드 상태의 깊은 사본을 반환한다. 저장소를 바꾸지 않는다.
// coord.json 이 아직 없을 때 기존 보드의 리스를 읽기만 하며 마이그레이션하지 않는다.
func (b *Board) Snapshot() (Snapshot, error) {
	return b.snapshot(nil)
}

// SnapshotWithPool reads pool counts from the same tasks as the snapshot.
func (b *Board) SnapshotWithPool(families map[string]string) (Snapshot, PoolStatus, error) {
	var pool PoolStatus
	path, err := b.poolConfigPath()
	if err != nil {
		return Snapshot{}, pool, err
	}
	out, err := b.snapshot(func(tasks []Task) error {
		cfg, err := loadPool(path)
		if err != nil {
			return err
		}
		pool = cfg.status(tasks, families)
		return nil
	})
	return out, pool, err
}

func (b *Board) snapshot(readPool func([]Task) error) (Snapshot, error) {
	var out Snapshot
	var legacy *CoordinatorLease
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		if readPool != nil {
			if err := readPool(st.Tasks); err != nil {
				return err
			}
		}
		legacy = cloneCoordinator(st.Coordinator)
		st.Coordinator = nil
		raw, err := json.Marshal(normalizeBoardState(st))
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &out)
	})
	if err != nil {
		return out, err
	}
	lease, exists, err := b.loadCoordFile()
	if err != nil {
		return out, err
	}
	if exists {
		out.Coordinator = lease
	} else {
		out.Coordinator = legacy
	}
	return out, nil
}
