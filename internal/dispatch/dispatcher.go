// Package dispatch 는 도구 이름을 받아 ACP 세션 풀·Codex 러너·공유 보드로 라우팅한다.
// MCP stdio 서버, A2A HTTP 서버, CLI 가 모두 이 Dispatcher 하나를 거친다.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/LeeYudok/a2a-acp-bridge/internal/acp"
	"github.com/LeeYudok/a2a-acp-bridge/internal/agents"
	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

// ErrUnknownTool 은 등록되지 않은 도구명이 들어왔을 때 반환한다
var ErrUnknownTool = errors.New("알 수 없는 도구")

// ErrNoPool 은 ACP 세션 풀 없이 AGY 위임·세션 제어를 호출했을 때 반환한다
var ErrNoPool = errors.New("ACP 세션 풀이 설정되지 않음")

// ReceiveResponse 는 a2a_receive_messages 응답이다
type ReceiveResponse struct {
	AgentID  string          `json:"agent_id"`
	Count    int             `json:"count"`
	Messages []board.Message `json:"messages"`
}

// TaskListResponse 는 a2a_task_status 전체 조회 응답이다
type TaskListResponse struct {
	Tasks []board.Task `json:"tasks"`
}

// Dispatcher 는 MCP·A2A·CLI 도구 호출을 처리하고 비동기 작업을 추적한다
type Dispatcher struct {
	Board      *board.Board
	Pool       *acp.SessionPool
	DefaultCwd string
	// Caller 는 요청에 from_agent 가 없을 때 쓰는 발신자 ID 다. 비어 있으면 user 로 기록한다.
	Caller  string
	asyncWG sync.WaitGroup
}

// Wait 는 비동기(--no-wait)로 큐잉된 모든 백그라운드 작업이 끝날 때까지 기다린다
func (d *Dispatcher) Wait() {
	d.asyncWG.Wait()
}

// Close 는 비동기 작업을 기다린 뒤 상주 ACP 워커를 모두 정리한다
func (d *Dispatcher) Close() {
	d.Wait()
	if d.Pool != nil {
		d.Pool.CloseAll()
	}
}

// caller 는 요청의 from_agent, Dispatcher.Caller, user 순으로 발신자를 정한다
func (d *Dispatcher) caller(args map[string]any) string {
	def := d.Caller
	if def == "" {
		def = agents.User
	}
	return strArg(args, "from_agent", def)
}

func (d *Dispatcher) pool() (*acp.SessionPool, error) {
	if d.Pool == nil {
		return nil, ErrNoPool
	}
	return d.Pool, nil
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
		return agents.Discover(d.DefaultCwd, sessions, tasks), nil
	case "acp_delegate_to_agy":
		return d.callDelegateAgy(ctx, args)
	case "a2a_delegate_to_codex":
		return d.callDelegateCodex(ctx, args)
	case "a2a_delegate_to_claude":
		return d.callDelegateClaude(ctx, args)
	case "acp_session_control":
		return d.callSessionControl(args)
	case "a2a_send_message":
		return d.Board.SendMessage(
			d.caller(args),
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

func (d *Dispatcher) callTaskStatus(args map[string]any) (any, error) {
	taskID := strArg(args, "task_id", "")
	state := strArg(args, "state", "")
	if taskID == "" {
		tasks, err := d.Board.ListTasks(20)
		if err != nil {
			return nil, err
		}
		return TaskListResponse{Tasks: tasks}, nil
	}
	cur, err := d.Board.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	if state != "" {
		return d.Board.UpsertTask(cur.Creator, cur.Assignee, cur.Title, state, taskID, "")
	}
	return cur, nil
}

func strArg(m map[string]any, k, def string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return def
}

func boolArg(m map[string]any, k string, def bool) bool {
	if v, ok := m[k]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}
