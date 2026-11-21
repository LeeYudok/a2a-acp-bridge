package board

import (
	"fmt"
	"slices"
)

// ReceiveMessages 는 대상 에이전트의 수신 메시지를 조회하고 필요 시 읽음 처리한다
func (b *Board) ReceiveMessages(agentID string, unreadOnly, markRead bool, limit int) ([]Message, error) {
	var out []Message
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		now := nowSeconds()
		out = []Message{}
		changed := false
		for i := range st.Messages {
			m := &st.Messages[i]
			if m.ToAgent != agentID || (unreadOnly && m.ReadAt > 0) {
				continue
			}
			if markRead && m.ReadAt == 0 {
				m.ReadAt = now
				changed = true
			}
			out = append(out, *m)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		if changed {
			return b.save(st)
		}
		return nil
	})
	return out, err
}

// GetTask 는 특정 task_id 의 상세 정보와 메시지 목록을 반환한다
func (b *Board) GetTask(taskID string) (Task, error) {
	var out Task
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		for _, t := range st.Tasks {
			if t.TaskID == taskID {
				out = attachMessages(t, st.Messages)
				return nil
			}
		}
		return fmt.Errorf("%s: %w", taskID, ErrTaskNotFound)
	})
	return out, err
}

// ListTasks 는 최근 수정 순으로 태스크 목록을 반환한다
func (b *Board) ListTasks(limit int) ([]Task, error) {
	var out []Task
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		cloned := slices.Clone(st.Tasks)
		slices.Reverse(cloned)
		if limit > 0 && len(cloned) > limit {
			cloned = cloned[:limit]
		}
		out = cloned
		return nil
	})
	return out, err
}

// UpsertSession 은 이름 기반 세션(acpx -s <name>)을 생성하거나 갱신한다
func (b *Board) UpsertSession(name, agent, sessionID, cwd, mode, model string) (NamedSession, error) {
	var out NamedSession
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		now := nowSeconds()
		for i := range st.Sessions {
			s := &st.Sessions[i]
			if s.Name != name || (agent != "" && s.Agent != agent) {
				continue
			}
			applySessionUpdates(s, sessionID, cwd, mode, model, now)
			if err := b.save(st); err != nil {
				return err
			}
			out = *s
			return nil
		}
		created := NamedSession{
			Name:      name,
			Agent:     agent,
			SessionID: sessionID,
			Cwd:       cwd,
			Mode:      mode,
			Model:     model,
			CreatedAt: now,
			UpdatedAt: now,
		}
		st.Sessions = append(st.Sessions, created)
		if err := b.save(st); err != nil {
			return err
		}
		out = created
		return nil
	})
	return out, err
}

func applySessionUpdates(s *NamedSession, sessionID, cwd, mode, model string, now float64) {
	if sessionID != "" {
		s.SessionID = sessionID
	}
	if cwd != "" {
		s.Cwd = cwd
	}
	if mode != "" {
		s.Mode = mode
	}
	if model != "" {
		s.Model = model
	}
	s.Closed = false
	s.UpdatedAt = now
}

// ResolveSession 은 세션 이름 또는 세션 ID로 저장된 NamedSession 을 찾는다
func (b *Board) ResolveSession(nameOrID, agent string) (NamedSession, error) {
	var out NamedSession
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		for _, s := range st.Sessions {
			if s.Closed {
				continue
			}
			if (s.Name == nameOrID || s.SessionID == nameOrID) && (agent == "" || s.Agent == agent) {
				out = s
				return nil
			}
		}
		return fmt.Errorf("%s: %w", nameOrID, ErrSessionNotFound)
	})
	return out, err
}

// CloseNamedSession 은 이름 또는 세션 ID에 해당하는 세션을 닫힘(closed) 처리한다
func (b *Board) CloseNamedSession(nameOrID string) (NamedSession, error) {
	var out NamedSession
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		now := nowSeconds()
		for i := range st.Sessions {
			s := &st.Sessions[i]
			if s.Name == nameOrID || s.SessionID == nameOrID {
				s.Closed = true
				s.UpdatedAt = now
				if err := b.save(st); err != nil {
					return err
				}
				out = *s
				return nil
			}
		}
		return fmt.Errorf("%s: %w", nameOrID, ErrSessionNotFound)
	})
	return out, err
}

// ListSessions 는 등록된 NamedSession 목록을 반환한다
func (b *Board) ListSessions(includeClosed bool) ([]NamedSession, error) {
	var out []NamedSession
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		out = []NamedSession{}
		for _, s := range st.Sessions {
			if !includeClosed && s.Closed {
				continue
			}
			out = append(out, s)
		}
		return nil
	})
	return out, err
}

func normalizeBoardState(st boardState) boardState {
	if st.Tasks == nil {
		st.Tasks = []Task{}
	}
	if st.Messages == nil {
		st.Messages = []Message{}
	}
	if st.Sessions == nil {
		st.Sessions = []NamedSession{}
	}
	return st
}

func attachMessages(t Task, all []Message) Task {
	msgs := []Message{}
	for _, m := range all {
		if m.TaskID == t.TaskID {
			msgs = append(msgs, m)
		}
	}
	t.Messages = msgs
	if t.Artifacts == nil {
		t.Artifacts = []Artifact{}
	}
	return t
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
