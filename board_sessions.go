package main

import (
	"fmt"
	"slices"
)

// ReceiveMessages 는 대상 에이전트의 수신 메시지를 조회하고 필요 시 읽음 처리한다
func (b *Board) ReceiveMessages(agentID string, unreadOnly, markRead bool, limit int) ([]Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	st, err := b.load()
	if err != nil {
		return nil, err
	}
	now := nowSeconds()
	out := []Message{}
	changed := false
	for i := range st.Messages {
		m := &st.Messages[i]
		if m.ToAgent != agentID {
			continue
		}
		if unreadOnly && m.ReadAt > 0 {
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
		if err := b.save(st); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetTask 는 특정 task_id 의 상세 정보와 메시지 목록을 반환한다
func (b *Board) GetTask(taskID string) (Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	st, err := b.load()
	if err != nil {
		return Task{}, err
	}
	for _, t := range st.Tasks {
		if t.TaskID == taskID {
			return attachMessages(t, st.Messages), nil
		}
	}
	return Task{}, fmt.Errorf("%s: %w", taskID, ErrTaskNotFound)
}

// ListTasks 는 최근 수정 순으로 태스크 목록을 반환한다
func (b *Board) ListTasks(limit int) ([]Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	st, err := b.load()
	if err != nil {
		return nil, err
	}
	cloned := slices.Clone(st.Tasks)
	slices.Reverse(cloned)
	if limit > 0 && len(cloned) > limit {
		cloned = cloned[:limit]
	}
	return cloned, nil
}

// UpsertSession 은 이름 기반 세션(acpx -s <name>)을 생성하거나 갱신한다
func (b *Board) UpsertSession(name, agent, sessionID, cwd, mode, model string) (NamedSession, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	st, err := b.load()
	if err != nil {
		return NamedSession{}, err
	}
	now := nowSeconds()
	for i := range st.Sessions {
		s := &st.Sessions[i]
		if s.Name != name || (agent != "" && s.Agent != agent) {
			continue
		}
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
		if err := b.save(st); err != nil {
			return NamedSession{}, err
		}
		return *s, nil
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
		return NamedSession{}, err
	}
	return created, nil
}

// ResolveSession 은 세션 이름 또는 세션 ID로 저장된 NamedSession 을 찾는다
func (b *Board) ResolveSession(nameOrID, agent string) (NamedSession, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	st, err := b.load()
	if err != nil {
		return NamedSession{}, err
	}
	for _, s := range st.Sessions {
		if s.Closed {
			continue
		}
		if (s.Name == nameOrID || s.SessionID == nameOrID) && (agent == "" || s.Agent == agent) {
			return s, nil
		}
	}
	return NamedSession{}, fmt.Errorf("%s: %w", nameOrID, ErrSessionNotFound)
}

// CloseNamedSession 은 이름 또는 세션 ID에 해당하는 세션을 닫힘(closed) 처리한다
func (b *Board) CloseNamedSession(nameOrID string) (NamedSession, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	st, err := b.load()
	if err != nil {
		return NamedSession{}, err
	}
	now := nowSeconds()
	for i := range st.Sessions {
		s := &st.Sessions[i]
		if s.Name == nameOrID || s.SessionID == nameOrID {
			s.Closed = true
			s.UpdatedAt = now
			if err := b.save(st); err != nil {
				return NamedSession{}, err
			}
			return *s, nil
		}
	}
	return NamedSession{}, fmt.Errorf("%s: %w", nameOrID, ErrSessionNotFound)
}

// ListSessions 는 등록된 NamedSession 목록을 반환한다
func (b *Board) ListSessions(includeClosed bool) ([]NamedSession, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	st, err := b.load()
	if err != nil {
		return nil, err
	}
	out := []NamedSession{}
	for _, s := range st.Sessions {
		if !includeClosed && s.Closed {
			continue
		}
		out = append(out, s)
	}
	return out, nil
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
