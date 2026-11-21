package main

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

// Skill 은 A2A Agent Card 의 개별 역량 항목이다
type Skill struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AgentCard 는 A2A 표준 에이전트 메타데이터다
type AgentCard struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	Protocols    []string        `json:"protocols"`
	Endpoint     string          `json:"endpoint"`
	Capabilities map[string]bool `json:"capabilities"`
	Skills       []Skill         `json:"skills"`
}

// DiscoverResponse 는 a2a_discover_agents 도구 및 /.well-known/agent.json 응답 DTO다
type DiscoverResponse struct {
	Protocol    string         `json:"protocol"`
	Workspace   string         `json:"workspace"`
	Agents      []AgentCard    `json:"agents"`
	Sessions    []NamedSession `json:"sessions"`
	RecentTasks []Task         `json:"recent_tasks"`
}

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
}

// Board 는 파일 기반 A2A 공유 태스크·메시지·세션 저장소다
type Board struct {
	mu   sync.Mutex
	path string
}

// DefaultBoardPath 는 기본 보드 저장 경로를 반환한다
func DefaultBoardPath() (string, error) {
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

// NewBoard 는 주어진 파일 경로를 사용하는 Board 를 생성한다
func NewBoard(path string) *Board {
	return &Board{path: path}
}

// AgentCards 는 AGY와 Codex의 A2A Agent Card 목록을 반환한다
func AgentCards(workspace string, sessions []NamedSession, recent []Task) DiscoverResponse {
	if sessions == nil {
		sessions = []NamedSession{}
	}
	if recent == nil {
		recent = []Task{}
	}
	return DiscoverResponse{
		Protocol:  "A2A/1.0 + ACP/1.0",
		Workspace: workspace,
		Agents: []AgentCard{
			{
				ID:        "agy",
				Name:      "Google Antigravity (AGY)",
				Version:   "1.5.9",
				Protocols: []string{"ACP-stdio-jsonrpc-2.0", "A2A-1.0", "MCP"},
				Endpoint:  "agy acp",
				Capabilities: map[string]bool{
					"streaming":          true,
					"session_resume":     true,
					"session_pool":       true,
					"cooperative_cancel": true,
					"runtime_mode_model": true,
					"async_queue":        true,
					"embedded_context":   true,
					"multi_turn":         true,
				},
				Skills: []Skill{
					{ID: "code-generation", Name: "코드 작성 및 리팩터링"},
					{ID: "architecture-review", Name: "설계 및 아키텍처 검증"},
					{ID: "acp-session", Name: "ACP 상주 세션·취소·모드 제어"},
				},
			},
			{
				ID:        "codex",
				Name:      "OpenAI Codex CLI",
				Version:   "0.169.0",
				Protocols: []string{"Codex-JSONL-exec", "Codex-app-server", "A2A-1.0", "MCP"},
				Endpoint:  "codex exec --json",
				Capabilities: map[string]bool{
					"streaming":      true,
					"session_resume": true,
					"async_queue":    true,
					"code_review":    true,
					"multi_turn":     true,
				},
				Skills: []Skill{
					{ID: "code-review", Name: "교차 코드 리뷰 및 버그 탐지"},
					{ID: "task-execution", Name: "비대화형 작업 수행 및 검증"},
					{ID: "thread-resume", Name: "스레드 기반 다중 턴 협업"},
				},
			},
		},
		Sessions:    sessions,
		RecentTasks: recent,
	}
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
	var msg Message
	err := b.withLock(func() error {
		st, err := b.load()
		if err != nil {
			return err
		}
		now := nowSeconds()
		t, err := upsertTaskInState(&st, fromAgent, toAgent, content, state, taskID, "", now)
		if err != nil {
			return err
		}
		mid, err := newID("msg")
		if err != nil {
			return err
		}
		msg = Message{
			MessageID: mid, TaskID: t.TaskID, FromAgent: fromAgent,
			ToAgent: toAgent, Intent: intent, Content: content, CreatedAt: now,
		}
		st.Messages = append(st.Messages, msg)
		return b.save(st)
	})
	return msg, err
}
