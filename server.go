package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrUnknownTool 은 등록되지 않은 도구명이 들어왔을 때 반환한다
var ErrUnknownTool = errors.New("알 수 없는 도구")

// ToolSchema 는 MCP tools/list 항목 DTO다
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ReceiveResponse 는 a2a_receive_messages 응답 DTO다
type ReceiveResponse struct {
	AgentID  string    `json:"agent_id"`
	Count    int       `json:"count"`
	Messages []Message `json:"messages"`
}

// TaskListResponse 는 a2a_task_status 전체 조회 응답 DTO다
type TaskListResponse struct {
	Tasks []Task `json:"tasks"`
}

// MCPTools 는 브리지가 노출하는 MCP 도구 스키마 목록이다
func MCPTools() []ToolSchema {
	return []ToolSchema{
		{
			Name:        "a2a_discover_agents",
			Description: "A2A Agent Card 목록(agy, codex), 등록된 명명 세션(sessions), 최근 공유 태스크 현황을 조회합니다.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "acp_delegate_to_agy",
			Description: "ACP(상주 세션 풀·자동 재연결)를 통해 AGY에게 작업을 위임합니다. session_name(-s)과 async(--no-wait) 큐잉을 지원합니다.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt":       map[string]any{"type": "string", "description": "AGY에게 전달할 프롬프트/작업 지시"},
					"session_id":   map[string]any{"type": "string", "description": "이어갈 ACP 세션 ID (선택)"},
					"session_name": map[string]any{"type": "string", "description": "acpx 스타일의 논리 세션 이름 (예: backend, docs)"},
					"mode":         map[string]any{"type": "string", "enum": []string{"auto", "accept-edits", "plan"}},
					"model":        map[string]any{"type": "string", "description": "세션에 적용할 모델 ID (선택)"},
					"async":        map[string]any{"type": "boolean", "description": "true면 백그라운드 큐에 넣고 즉시 task_id 반환(--no-wait)"},
					"cwd":          map[string]any{"type": "string", "description": "작업 디렉터리 (선택)"},
				},
				"required": []string{"prompt"},
			},
		},
		{
			Name:        "a2a_delegate_to_codex",
			Description: "A2A 브리지를 통해 Codex(`codex exec --json`)에게 작업/리뷰를 위임합니다. session_name과 async 큐잉을 지원합니다.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt":       map[string]any{"type": "string", "description": "Codex에게 전달할 프롬프트/작업 지시"},
					"thread_id":    map[string]any{"type": "string", "description": "이어갈 Codex 스레드 ID (선택)"},
					"session_name": map[string]any{"type": "string", "description": "acpx 스타일의 논리 세션 이름 (선택)"},
					"sandbox":      map[string]any{"type": "string", "enum": []string{"read-only", "workspace-write"}},
					"async":        map[string]any{"type": "boolean", "description": "true면 백그라운드 큐에 넣고 즉시 task_id 반환"},
					"cwd":          map[string]any{"type": "string", "description": "작업 디렉터리 (선택)"},
				},
				"required": []string{"prompt"},
			},
		},
		{
			Name:        "acp_session_control",
			Description: "acpx 스타일의 ACP 세션 런타임 제어(status | cancel | set_mode | set_model | close)를 수행합니다.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":  map[string]any{"type": "string", "enum": []string{"status", "cancel", "set_mode", "set_model", "close"}},
					"session": map[string]any{"type": "string", "description": "대상 세션 이름(session_name) 또는 session_id"},
					"mode":    map[string]any{"type": "string", "enum": []string{"auto", "accept-edits", "plan"}},
					"model":   map[string]any{"type": "string", "description": "변경할 모델 ID (set_model 시)"},
					"cwd":     map[string]any{"type": "string", "description": "작업 디렉터리 (선택)"},
				},
				"required": []string{"action"},
			},
		},
		{
			Name:        "a2a_send_message",
			Description: "공유 A2A 메시지 보드에 상대 에이전트(agy 또는 codex) 앞으로 메시지/태스크를 남깁니다.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from_agent": map[string]any{"type": "string", "enum": []string{"agy", "codex", "user"}},
					"to_agent":   map[string]any{"type": "string", "enum": []string{"agy", "codex"}},
					"content":    map[string]any{"type": "string", "description": "전달할 메시지 본문"},
					"intent":     map[string]any{"type": "string", "enum": []string{"delegate", "review", "reply", "chat", "sync"}},
					"task_id":    map[string]any{"type": "string", "description": "연결할 태스크 ID (선택)"},
				},
				"required": []string{"from_agent", "to_agent", "content"},
			},
		},
		{
			Name:        "a2a_receive_messages",
			Description: "공유 A2A 메시지 보드에서 특정 에이전트(agy 또는 codex) 앞으로 도착한 메시지를 읽습니다.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id":    map[string]any{"type": "string", "enum": []string{"agy", "codex"}},
					"unread_only": map[string]any{"type": "boolean"},
					"mark_read":   map[string]any{"type": "boolean"},
				},
				"required": []string{"agent_id"},
			},
		},
		{
			Name:        "a2a_task_status",
			Description: "공유 A2A 태스크 상세 조회 또는 상태(submitted/working/completed/failed) 갱신을 수행합니다.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string"},
					"state":   map[string]any{"type": "string", "enum": []string{"submitted", "working", "input-required", "completed", "failed"}},
				},
			},
		},
	}
}

// Dispatcher 는 MCP 및 A2A 도구 호출과 상주 ACP 세션 풀을 관리한다
type Dispatcher struct {
	Board      *Board
	Pool       *SessionPool
	DefaultCwd string
	asyncWG    sync.WaitGroup
}

func (d *Dispatcher) pool() *SessionPool {
	if d.Pool != nil {
		return d.Pool
	}
	return defaultPool
}

// Wait 는 비동기(--no-wait)로 큐잉된 모든 백그라운드 작업이 끝날 때까지 대기한다
func (d *Dispatcher) Wait() {
	d.asyncWG.Wait()
}

// CallTool 은 도구 이름과 인자 맵을 받아 실행 결과를 반환한다
func (d *Dispatcher) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	switch name {
	case "a2a_discover_agents":
		tasks, err := d.Board.ListTasks(10)
		if err != nil {
			return nil, err
		}
		sessions, _ := d.Board.ListSessions(false)
		return AgentCards(d.DefaultCwd, sessions, tasks), nil
	case "acp_delegate_to_agy":
		return d.callDelegateAgy(ctx, args)
	case "a2a_delegate_to_codex":
		return d.callDelegateCodex(ctx, args)
	case "acp_session_control":
		return d.callSessionControl(args)
	case "a2a_send_message":
		return d.Board.SendMessage(
			strArg(args, "from_agent", "user"),
			strArg(args, "to_agent", "codex"),
			strArg(args, "content", ""),
			strArg(args, "intent", "delegate"),
			strArg(args, "task_id", ""),
		)
	case "a2a_receive_messages":
		agentID := strArg(args, "agent_id", "agy")
		msgs, err := d.Board.ReceiveMessages(agentID, boolArg(args, "unread_only", true), boolArg(args, "mark_read", true), 20)
		if err != nil {
			return nil, err
		}
		return ReceiveResponse{AgentID: agentID, Count: len(msgs), Messages: msgs}, nil
	case "a2a_task_status":
		return d.callTaskStatus(args)
	default:
		return nil, fmt.Errorf("%s: %w", name, ErrUnknownTool)
	}
}

func (d *Dispatcher) resolveAgentSessionID(agent, explicitID, sessionName string) string {
	if explicitID != "" {
		return explicitID
	}
	if sessionName == "" {
		return ""
	}
	if rec, err := d.Board.ResolveSession(sessionName, agent); err == nil {
		return rec.SessionID
	}
	return ""
}

func (d *Dispatcher) callDelegateAgy(ctx context.Context, args map[string]any) (AgyDelegateResult, error) {
	prompt := strArg(args, "prompt", "")
	cwd := strArg(args, "cwd", d.DefaultCwd)
	sessionName := strArg(args, "session_name", "")
	sid := d.resolveAgentSessionID("agy", strArg(args, "session_id", ""), sessionName)
	mode := strArg(args, "mode", "")
	model := strArg(args, "model", "")
	isAsync := boolArg(args, "async", false)

	msg, err := d.Board.SendMessage("codex", "agy", prompt, "delegate", "")
	if err != nil {
		return AgyDelegateResult{}, err
	}
	if isAsync {
		_, _ = d.Board.UpsertTask("codex", "agy", prompt, "working", msg.TaskID, sid)
		d.asyncWG.Add(1)
		go func() {
			defer d.asyncWG.Done()
			_, _ = d.executeAgyTurn(context.Background(), cwd, prompt, sid, sessionName, mode, model, msg.TaskID)
		}()
		return AgyDelegateResult{
			Agent:       "agy",
			Protocol:    "ACP-stdio-jsonrpc-2.0",
			SessionID:   sid,
			SessionName: sessionName,
			TaskID:      msg.TaskID,
			StopReason:  "queued",
			ToolCalls:   []string{},
			Async:       true,
		}, nil
	}
	return d.executeAgyTurn(ctx, cwd, prompt, sid, sessionName, mode, model, msg.TaskID)
}

func (d *Dispatcher) executeAgyTurn(ctx context.Context, cwd, prompt, sid, sessionName, mode, model, taskID string) (AgyDelegateResult, error) {
	res, err := d.pool().PromptWithOptions(ctx, cwd, prompt, sid, mode, model)
	if err != nil {
		_, _ = d.Board.UpsertTask("codex", "agy", prompt, "failed", taskID, sid)
		return AgyDelegateResult{}, err
	}
	if sessionName != "" {
		_, _ = d.Board.UpsertSession(sessionName, "agy", res.SessionID, cwd, mode, model)
	}
	_, _ = d.Board.SendMessage("agy", "codex", res.Response, "reply", taskID)
	_, _ = d.Board.UpsertTask("codex", "agy", prompt, "completed", taskID, res.SessionID)
	res.TaskID = taskID
	res.SessionName = sessionName
	return res, nil
}
