package board

import "encoding/json"

// Snapshot 은 보드 전체(태스크·메시지·세션)의 깊은 사본이다
type Snapshot struct {
	Tasks    []Task         `json:"tasks"`
	Messages []Message      `json:"messages"`
	Sessions []NamedSession `json:"sessions"`
}

// Snapshot 은 현재 보드 상태의 깊은 사본을 반환한다. 저장소를 바꾸지 않는다
func (b *Board) Snapshot() (Snapshot, error) {
	var out Snapshot
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		raw, err := json.Marshal(normalizeBoardState(st))
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &out)
	})
	return out, err
}
