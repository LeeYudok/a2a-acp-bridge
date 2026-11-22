// Package agents 는 브리지가 아는 에이전트의 A2A Agent Card 를 정의한다.
package agents

import "github.com/LeeYudok/a2a-acp-bridge/internal/board"

// Skill 은 A2A Agent Card 의 개별 역량 항목이다
type Skill struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Card 는 A2A 표준 에이전트 메타데이터다
type Card struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	Protocols    []string        `json:"protocols"`
	Endpoint     string          `json:"endpoint"`
	Capabilities map[string]bool `json:"capabilities"`
	Skills       []Skill         `json:"skills"`
}

// Discovery 는 a2a_discover_agents 도구와 /.well-known/agent.json 의 응답이다
type Discovery struct {
	Protocol         string                  `json:"protocol"`
	Workspace        string                  `json:"workspace"`
	Agents           []Card                  `json:"agents"`
	Sessions         []board.NamedSession    `json:"sessions"`
	RecentTasks      []board.Task            `json:"recent_tasks"`
	Coordinator      *board.CoordinatorLease `json:"coordinator,omitempty"`
	RegistryWarnings []RegistryWarning       `json:"registry_warnings,omitempty"`
}

// Discover 는 Agent Card 목록에 열린 명명 세션과 최근 태스크를 붙여 반환한다
func Discover(workspace string, sessions []board.NamedSession, recent []board.Task) Discovery {
	if sessions == nil {
		sessions = []board.NamedSession{}
	}
	if recent == nil {
		recent = []board.Task{}
	}
	cards, warnings := CardsWithWarnings()
	return Discovery{
		Protocol:         "A2A/1.0 + ACP/1.0",
		Workspace:        workspace,
		Agents:           cards,
		Sessions:         sessions,
		RecentTasks:      recent,
		RegistryWarnings: warnings,
	}
}

func builtinCards() []Card {
	return []Card{
		{
			ID:        "agy",
			Name:      "Google Antigravity (AGY)",
			Version:   "1.5.14",
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
			Version:   "0.170.0",
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
		{
			ID:        "claude",
			Name:      "Claude Code",
			Version:   "2.1.330",
			Protocols: []string{"Claude-Code-print-json", "A2A-1.0", "MCP"},
			Endpoint:  "claude -p --output-format json",
			Capabilities: map[string]bool{
				"async_queue":     true,
				"board_messaging": true,
				"code_review":     true,
				"multi_turn":      true,
				"session_resume":  true,
			},
			Skills: []Skill{
				{ID: "code-review", Name: "교차 코드 리뷰 및 구조 개선"},
				{ID: "task-execution", Name: "보드 요청 수행 및 이어받기"},
			},
		},
	}
}
