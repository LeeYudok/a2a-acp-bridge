package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func (d *Dispatcher) callDelegateCodex(ctx context.Context, args map[string]any) (CodexDelegateResult, error) {
	prompt := strArg(args, "prompt", "")
	cwd := strArg(args, "cwd", d.DefaultCwd)
	sessionName := strArg(args, "session_name", "")
	tid := d.resolveAgentSessionID("codex", strArg(args, "thread_id", ""), sessionName)
	sandbox := strArg(args, "sandbox", "workspace-write")
	isAsync := boolArg(args, "async", false)

	msg, err := d.Board.SendMessage("agy", "codex", prompt, "delegate", "")
	if err != nil {
		return CodexDelegateResult{}, err
	}
	if isAsync {
		_, _ = d.Board.UpsertTask("agy", "codex", prompt, "working", msg.TaskID, tid)
		d.asyncWG.Add(1)
		go func() {
			defer d.asyncWG.Done()
			_, _ = d.executeCodexTurn(context.Background(), cwd, sandbox, prompt, tid, sessionName, msg.TaskID)
		}()
		return CodexDelegateResult{
			Agent:       "codex",
			Protocol:    "Codex-JSONL-exec",
			ThreadID:    tid,
			SessionName: sessionName,
			TaskID:      msg.TaskID,
			AllMessages: []string{},
			Async:       true,
		}, nil
	}
	return d.executeCodexTurn(ctx, cwd, sandbox, prompt, tid, sessionName, msg.TaskID)
}

func (d *Dispatcher) executeCodexTurn(ctx context.Context, cwd, sandbox, prompt, tid, sessionName, taskID string) (CodexDelegateResult, error) {
	res, err := DelegateToCodex(ctx, cwd, sandbox, prompt, tid)
	if err != nil {
		_, _ = d.Board.UpsertTask("agy", "codex", prompt, "failed", taskID, tid)
		return CodexDelegateResult{}, err
	}
	if sessionName != "" && res.ThreadID != "" {
		_, _ = d.Board.UpsertSession(sessionName, "codex", res.ThreadID, cwd, "", "")
	}
	_, _ = d.Board.SendMessage("codex", "agy", res.Response, "reply", taskID)
	_, _ = d.Board.UpsertTask("agy", "codex", prompt, "completed", taskID, res.ThreadID)
	res.TaskID = taskID
	res.SessionName = sessionName
	return res, nil
}

func (d *Dispatcher) callSessionControl(args map[string]any) (SessionControlResult, error) {
	action := strArg(args, "action", "status")
	target := strArg(args, "session", "")
	cwd := strArg(args, "cwd", d.DefaultCwd)
	resolvedID, matchedName, resolvedCwd := d.resolveControlTarget(target, cwd)
	switch action {
	case "status":
		sessions, _ := d.Board.ListSessions(false)
		return SessionControlResult{Action: "status", Status: "ok", Workers: d.pool().Snapshots(), Sessions: sessions}, nil
	case "cancel":
		return d.ctrlCancel(resolvedID)
	case "set_mode":
		return d.ctrlSetMode(resolvedCwd, resolvedID, matchedName, strArg(args, "mode", "auto"))
	case "set_model":
		return d.ctrlSetModel(resolvedCwd, resolvedID, matchedName, strArg(args, "model", ""))
	case "close":
		return d.ctrlClose(target, resolvedID)
	default:
		return SessionControlResult{}, fmt.Errorf("알 수 없는 세션 제어 동작: %s", action)
	}
}

func (d *Dispatcher) resolveControlTarget(target, defaultCwd string) (string, string, string) {
	if target == "" {
		return "", "", defaultCwd
	}
	if rec, err := d.Board.ResolveSession(target, ""); err == nil {
		cwd := defaultCwd
		if rec.Cwd != "" {
			cwd = rec.Cwd
		}
		return rec.SessionID, rec.Name, cwd
	}
	return target, "", defaultCwd
}

func (d *Dispatcher) ctrlCancel(resolvedID string) (SessionControlResult, error) {
	if resolvedID == "" {
		return SessionControlResult{}, fmt.Errorf("cancel: session 인자가 필요합니다")
	}
	if err := d.pool().Cancel(resolvedID); err != nil {
		return SessionControlResult{}, err
	}
	return SessionControlResult{Action: "cancel", Session: resolvedID, Status: "cancelled"}, nil
}

func (d *Dispatcher) ctrlSetMode(cwd, resolvedID, matchedName, mode string) (SessionControlResult, error) {
	if err := d.pool().SetMode(cwd, resolvedID, mode); err != nil {
		return SessionControlResult{}, err
	}
	if matchedName != "" {
		_, _ = d.Board.UpsertSession(matchedName, "agy", resolvedID, cwd, mode, "")
	}
	return SessionControlResult{Action: "set_mode", Session: resolvedID, Mode: mode, Status: "updated"}, nil
}

func (d *Dispatcher) ctrlSetModel(cwd, resolvedID, matchedName, model string) (SessionControlResult, error) {
	if model == "" {
		return SessionControlResult{}, fmt.Errorf("set_model: model 인자가 필요합니다")
	}
	if err := d.pool().SetModel(cwd, resolvedID, model); err != nil {
		return SessionControlResult{}, err
	}
	if matchedName != "" {
		_, _ = d.Board.UpsertSession(matchedName, "agy", resolvedID, cwd, "", model)
	}
	return SessionControlResult{Action: "set_model", Session: resolvedID, Model: model, Status: "updated"}, nil
}

func (d *Dispatcher) ctrlClose(target, resolvedID string) (SessionControlResult, error) {
	if resolvedID != "" {
		_ = d.pool().CloseSession(resolvedID)
	}
	if target != "" {
		_, _ = d.Board.CloseNamedSession(target)
	}
	return SessionControlResult{Action: "close", Session: resolvedID, Status: "closed"}, nil
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

// MCPRequest 는 MCP JSON-RPC 2.0 요청 구조체다
type MCPRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

// HandleMCPRequest 는 단일 MCP JSON-RPC 2.0 요청을 처리하고 응답 맵을 반환한다
func HandleMCPRequest(ctx context.Context, d *Dispatcher, req MCPRequest) map[string]any {
	if strings.HasPrefix(req.Method, "notifications/") {
		return nil
	}
	switch req.Method {
	case "initialize":
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "a2a-acp-bridge", "version": "2.0.0"},
			},
		}
	case "ping":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}}
	case "tools/list":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": MCPTools()}}
	case "tools/call":
		return executeMCPToolCall(ctx, d, req.ID, req.Params)
	default:
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"error":   map[string]any{"code": -32601, "message": "Method not found: " + req.Method},
		}
	}
}

func executeMCPToolCall(ctx context.Context, d *Dispatcher, reqID any, params map[string]any) map[string]any {
	name := strArg(params, "name", "")
	args, _ := params["arguments"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	res, err := d.CallTool(ctx, name, args)
	if err != nil {
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      reqID,
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": "Error: " + err.Error()}},
				"isError": true,
			},
		}
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(b)}},
			"isError": false,
		},
	}
}

// RunStdioMCPServer 는 표준 입출력으로 MCP JSON-RPC 2.0 서버 루프를 실행한다
func RunStdioMCPServer(ctx context.Context, d *Dispatcher, in io.Reader, out io.Writer) error {
	defer func() {
		d.Wait()
		d.pool().CloseAll()
	}()
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req MCPRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		resp := HandleMCPRequest(ctx, d, req)
		if resp == nil {
			continue
		}
		b, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		if _, err := out.Write(append(b, '\n')); err != nil {
			return fmt.Errorf("MCP stdout 쓰기 실패: %w", err)
		}
	}
	return sc.Err()
}
