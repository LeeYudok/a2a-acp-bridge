package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

func spawnACPWorker(command []string, cwd, sessionID string) (*acpWorker, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("ACP 실행 명령이 비어 있음")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = cwd
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("agy stdin 파이프 실패: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("agy stdout 파이프 실패: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("agy acp 시작 실패: %w", err)
	}

	reader := bufio.NewScanner(stdout)
	reader.Buffer(make([]byte, 64*1024), 4*1024*1024)
	w := &acpWorker{
		cmd:    cmd,
		stdin:  stdin,
		reader: reader,
		cwd:    cwd,
	}
	go func() {
		_ = cmd.Wait()
		w.exited.Store(true)
	}()

	if err := w.initializeACP(); err != nil {
		w.killProcess()
		return nil, err
	}
	sid, err := w.openSession(sessionID)
	if err != nil {
		w.killProcess()
		return nil, err
	}
	w.sessionID = sid
	return w, nil
}

func (w *acpWorker) initializeACP() error {
	reqID := int(w.nextReqID.Add(1))
	initParams := map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo":         map[string]any{"name": "a2a-acp-bridge", "version": "2.0.0"},
	}
	raw, _, _, err := w.sendRPC(reqID, "initialize", initParams, true)
	if err != nil {
		return fmt.Errorf("ACP initialize 실패: %w", err)
	}
	var initRes struct {
		AgentCapabilities struct {
			SessionCapabilities map[string]json.RawMessage `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	if json.Unmarshal(raw, &initRes) == nil {
		_, w.supportsResume = initRes.AgentCapabilities.SessionCapabilities["resume"]
		_, w.supportsClose = initRes.AgentCapabilities.SessionCapabilities["close"]
	}
	return nil
}

func (w *acpWorker) openSession(sessionID string) (string, error) {
	if sessionID != "" {
		if sid, ok := w.tryReconnectSession(sessionID); ok {
			return sid, nil
		}
	}
	reqID := int(w.nextReqID.Add(1))
	raw, _, _, err := w.sendRPC(reqID, "session/new", map[string]any{
		"cwd":        w.cwd,
		"mcpServers": []any{},
	}, true)
	if err != nil {
		return "", fmt.Errorf("ACP session/new 실패: %w", err)
	}
	return parseSessionID(raw, sessionID, true)
}

func (w *acpWorker) tryReconnectSession(sessionID string) (string, bool) {
	params := map[string]any{
		"sessionId":  sessionID,
		"cwd":        w.cwd,
		"mcpServers": []any{},
	}
	if w.supportsResume {
		reqID := int(w.nextReqID.Add(1))
		if raw, _, _, err := w.sendRPC(reqID, "session/resume", params, true); err == nil {
			if sid, err := parseSessionID(raw, sessionID, false); err == nil {
				return sid, true
			}
		}
	}
	reqID := int(w.nextReqID.Add(1))
	if raw, _, _, err := w.sendRPC(reqID, "session/load", params, true); err == nil {
		if sid, err := parseSessionID(raw, sessionID, false); err == nil {
			return sid, true
		}
	}
	return "", false
}

func parseSessionID(raw json.RawMessage, fallback string, strict bool) (string, error) {
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &res); err == nil && res.SessionID != "" {
		return res.SessionID, nil
	}
	if !strict && fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("ACP sessionId 파싱 실패: %s", string(raw))
}

func (w *acpWorker) runPromptTurn(ctx context.Context, promptText string, reused bool) (AgyDelegateResult, error) {
	w.turnMu.Lock()
	defer w.turnMu.Unlock()

	w.activePrompt.Store(true)
	w.cancelling.Store(false)
	turnDone := make(chan struct{})
	defer func() {
		close(turnDone)
		w.activePrompt.Store(false)
		w.cancelling.Store(false)
	}()

	go func() {
		select {
		case <-ctx.Done():
			_ = w.sendCancel()
		case <-turnDone:
		}
	}()

	text, tools, stopReason, err := w.promptOnce(promptText)
	if err != nil {
		return AgyDelegateResult{}, err
	}
	if activeUser := readActivePersonalUser(w.cwd); activeUser != "" && needsPersonalGateReply(text) {
		confirm := fmt.Sprintf("%s\n(`.claude/scripts/personal-layer.sh use %s` 단독 실행 후 다음 요청에 답하세요: %s)", activeUser, activeUser, promptText)
		if t2, tc2, sr2, err2 := w.promptOnce(confirm); err2 == nil && t2 != "" {
			text, tools, stopReason = t2, tc2, sr2
		}
	}
	w.turnCount.Add(1)
	return AgyDelegateResult{
		Agent:         "agy",
		Protocol:      "ACP-stdio-jsonrpc-2.0",
		SessionID:     w.sessionID,
		Response:      text,
		ToolCalls:     tools,
		StopReason:    stopReason,
		ReusedProcess: reused,
	}, nil
}

func (w *acpWorker) promptOnce(text string) (string, []string, string, error) {
	reqID := int(w.nextReqID.Add(1))
	raw, msgText, tools, err := w.sendRPC(reqID, "session/prompt", map[string]any{
		"sessionId": w.sessionID,
		"prompt":    []map[string]string{{"type": "text", "text": text}},
	}, false)
	if err != nil {
		return "", nil, "", fmt.Errorf("ACP session/prompt 실패: %w", err)
	}
	var res struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(raw, &res)
	if res.StopReason == "" && w.cancelling.Load() {
		res.StopReason = "cancelled"
	}
	return msgText, tools, res.StopReason, nil
}

func (w *acpWorker) sendRPC(reqID int, method string, params any, suppressUpdates bool) (json.RawMessage, string, []string, error) {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  method,
		"params":  params,
	}
	w.writeMu.Lock()
	err := writeJSONRPC(w.stdin, req)
	w.writeMu.Unlock()
	if err != nil {
		return nil, "", nil, err
	}
	return w.readUntilResponse(reqID, suppressUpdates)
}

func (w *acpWorker) readUntilResponse(reqID int, suppressUpdates bool) (json.RawMessage, string, []string, error) {
	chunks := []string{}
	tools := []string{}
	for w.reader.Scan() {
		line := strings.TrimSpace(w.reader.Text())
		if line == "" {
			continue
		}
		env, ok := parseACPEnvelope(line)
		if !ok {
			continue
		}
		if w.replyIfAgentRequest(env) {
			continue
		}
		if !suppressUpdates && env.Method == "session/update" && len(env.Params) > 0 {
			appendUpdateFields(env.Params, &chunks, &tools)
		}
		if env.ID != nil && *env.ID == reqID {
			if env.Error != nil {
				return nil, "", nil, fmt.Errorf("ACP RPC 에러 (%d): %s", env.Error.Code, env.Error.Message)
			}
			return env.Result, strings.TrimSpace(strings.Join(chunks, "")), tools, nil
		}
	}
	if err := w.reader.Err(); err != nil {
		return nil, "", nil, fmt.Errorf("ACP 스트림 읽기 실패: %w", err)
	}
	return nil, "", nil, fmt.Errorf("ACP 프로세스 연결 종료 (reqID=%d)", reqID)
}

func (w *acpWorker) replyIfAgentRequest(env jsonRPCEnvelope) bool {
	if env.ID == nil || env.Method == "" {
		return false
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	return handleAgentToClientRPC(w.stdin, env, w.cancelling.Load())
}
