// Package board 는 에이전트가 함께 쓰는 파일 기반 A2A 보드(태스크·메시지·명명 세션)와 지휘권 리스를 관리한다.
// 보드와 coord.json은 파일별 뮤텍스·OS 락을 잡고, 임시 파일 교체로 원자 저장한다.
package board

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrTaskNotFound 는 요청한 task_id 가 보드에 없을 때 반환한다
var ErrTaskNotFound = errors.New("태스크를 찾을 수 없음")

// ErrSessionNotFound 는 요청한 세션 이름 또는 ID가 없을 때 반환한다
var ErrSessionNotFound = errors.New("세션을 찾을 수 없음")

// Artifact 는 태스크 산출물 메타데이터다
type Artifact struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

// Message 는 에이전트 간 송수신 메시지다
type Message struct {
	MessageID string  `json:"message_id"`
	TaskID    string  `json:"task_id"`
	FromAgent string  `json:"from_agent"`
	ToAgent   string  `json:"to_agent"`
	Intent    string  `json:"intent"`
	Content   string  `json:"content"`
	ReadAt    float64 `json:"read_at,omitempty"`
	CreatedAt float64 `json:"created_at"`
}

// Task 는 공유 A2A 작업 단위다
type Task struct {
	TaskID     string     `json:"task_id"`
	Creator    string     `json:"creator"`
	Assignee   string     `json:"assignee"`
	Title      string     `json:"title"`
	State      string     `json:"state"`
	SessionRef string     `json:"session_ref"`
	Artifacts  []Artifact `json:"artifacts"`
	Messages   []Message  `json:"messages,omitempty"`
	CreatedAt  float64    `json:"created_at"`
	UpdatedAt  float64    `json:"updated_at"`
}

// NamedSession 은 acpx 스타일의 이름 기반 영속 세션 레코드다
type NamedSession struct {
	Name      string  `json:"name"`
	Agent     string  `json:"agent"`
	SessionID string  `json:"session_id"`
	Cwd       string  `json:"cwd"`
	Mode      string  `json:"mode,omitempty"`
	Model     string  `json:"model,omitempty"`
	Closed    bool    `json:"closed,omitempty"`
	CreatedAt float64 `json:"created_at"`
	UpdatedAt float64 `json:"updated_at"`
}

type boardState struct {
	Tasks    []Task         `json:"tasks"`
	Messages []Message      `json:"messages"`
	Sessions []NamedSession `json:"sessions"`

	// Coordinator 는 #20 이전 보드에 남은 리스를 읽기 위한 필드다. save 는 이 값을 쓰지 않는다.
	Coordinator *CoordinatorLease `json:"coordinator,omitempty"`
}

// Board 는 파일 기반 A2A 공유 태스크·메시지·세션 저장소다
type Board struct {
	mu      sync.Mutex
	coordMu sync.Mutex
	path    string
}

// DefaultPath 는 기본 보드 저장 경로를 반환한다. A2A_BRIDGE_STORE 가 있으면 그 값을 쓴다
func DefaultPath() (string, error) {
	if env := os.Getenv("A2A_BRIDGE_STORE"); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("홈 디렉터리 확인 실패: %w", err)
	}
	dir := filepath.Join(home, ".local", "share", "a2a-acp-bridge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("보드 디렉터리 생성 실패: %w", err)
	}
	return filepath.Join(dir, "a2a_board.json"), nil
}

// New 는 주어진 파일 경로를 쓰는 Board 를 만든다
func New(path string) *Board {
	return &Board{path: path}
}

func upsertTaskInState(st *boardState, creator, assignee, title, state, taskID, sessionRef string, now float64) (Task, error) {
	var err error
	if taskID == "" {
		if taskID, err = newID("task"); err != nil {
			return Task{}, err
		}
	}
	for i := range st.Tasks {
		if st.Tasks[i].TaskID != taskID {
			continue
		}
		if state != "" {
			st.Tasks[i].State = state
		}
		if sessionRef != "" {
			st.Tasks[i].SessionRef = sessionRef
		}
		st.Tasks[i].UpdatedAt = now
		return attachMessages(st.Tasks[i], st.Messages), nil
	}
	if title == "" {
		title = taskID
	}
	if state == "" {
		state = "submitted"
	}
	created := Task{
		TaskID: taskID, Creator: creator, Assignee: assignee,
		Title: truncateRunes(title, 60), State: state, SessionRef: sessionRef,
		Artifacts: []Artifact{}, CreatedAt: now, UpdatedAt: now,
	}
	st.Tasks = append(st.Tasks, created)
	return attachMessages(created, st.Messages), nil
}

// UpsertTask 는 태스크를 생성하거나 상태를 갱신한다
func (b *Board) UpsertTask(creator, assignee, title, state, taskID, sessionRef string) (Task, error) {
	var out Task
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		t, err := upsertTaskInState(&st, creator, assignee, title, state, taskID, sessionRef, nowSeconds())
		if err != nil {
			return err
		}
		if err := b.save(st); err != nil {
			return err
		}
		out = t
		return nil
	})
	return out, err
}

// SendMessage 는 공유 보드에 새 메시지를 기록하고 관련 태스크를 함께 갱신한다
func (b *Board) SendMessage(fromAgent, toAgent, content, intent, taskID string) (Message, error) {
	state := "submitted"
	if intent == "reply" {
		state = "working"
	}
	if intent == "" {
		intent = "delegate"
	}
	return b.recordMessage(fromAgent, toAgent, content, intent, taskID, state, "")
}

// CompleteDelegation records the reply and releases capacity in the same transaction.
func (b *Board) CompleteDelegation(from, to, content, taskID, session string) (Message, error) {
	return b.recordMessage(from, to, content, "reply", taskID, "completed", session)
}

func (b *Board) recordMessage(fromAgent, toAgent, content, intent, taskID, state, session string) (Message, error) {
	var msg Message
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		msg, err = addMessage(&st, fromAgent, toAgent, content, intent, taskID, state, session)
		if err != nil {
			return err
		}
		return b.save(st)
	})
	return msg, err
}

func addMessage(st *boardState, from, to, content, intent, taskID, state, session string) (Message, error) {
	now := nowSeconds()
	task, err := upsertTaskInState(st, from, to, content, state, taskID, session, now)
	if err != nil {
		return Message{}, err
	}
	id, err := newID("msg")
	if err != nil {
		return Message{}, err
	}
	msg := Message{
		MessageID: id, TaskID: task.TaskID, FromAgent: from, ToAgent: to,
		Intent: intent, Content: content, CreatedAt: now,
	}
	st.Messages = append(st.Messages, msg)
	return msg, nil
}
