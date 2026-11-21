package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LeeYudok/a2a-acp-bridge/internal/gate"
)

func (w *worker) runPromptTurn(ctx context.Context, promptText string, reused bool, mode, model string) (TurnResult, error) {
	w.turnMu.Lock()
	defer w.turnMu.Unlock()

	if err := w.applyTurnOptionsLocked(mode, model); err != nil {
		return TurnResult{}, err
	}
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
		return TurnResult{}, err
	}
	if user := gate.ActiveUser(w.cwd); user != "" && gate.NeedsReply(text) {
		if t2, tc2, sr2, err2 := w.promptOnce(gate.Followup(user, promptText)); err2 == nil && t2 != "" {
			text, tools, stopReason = t2, tc2, sr2
		}
	}
	w.turnCount.Add(1)
	return TurnResult{
		SessionID:     w.sessionID,
		Response:      text,
		ToolCalls:     tools,
		StopReason:    stopReason,
		ReusedProcess: reused,
	}, nil
}

func (w *worker) promptOnce(text string) (string, []string, string, error) {
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

func (w *worker) sendRPC(reqID int, method string, params any, suppressUpdates bool) (json.RawMessage, string, []string, error) {
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

func (w *worker) readUntilResponse(reqID int, suppressUpdates bool) (json.RawMessage, string, []string, error) {
	chunks := []string{}
	tools := []string{}
	for w.reader.Scan() {
		line := strings.TrimSpace(w.reader.Text())
		if line == "" {
			continue
		}
		env, ok := parseEnvelope(line)
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

func (w *worker) replyIfAgentRequest(env envelope) bool {
	if env.ID == nil || env.Method == "" {
		return false
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	return replyToAgentRequest(w.stdin, env, w.cancelling.Load())
}

func (w *worker) applyTurnOptionsLocked(mode, model string) error {
	if mode != "" && w.getMode() != mode {
		if err := w.applyModeLocked(mode); err != nil {
			return fmt.Errorf("ACP 실행 모드(%s) 적용 실패: %w", mode, err)
		}
	}
	if model != "" && w.getModel() != model {
		if err := w.applyModelLocked(model); err != nil {
			return fmt.Errorf("ACP 모델(%s) 적용 실패: %w", model, err)
		}
	}
	return nil
}

func (w *worker) applyModeLocked(modeID string) error {
	reqID := int(w.nextReqID.Add(1))
	_, _, _, err := w.sendRPC(reqID, "session/set_mode", map[string]any{"sessionId": w.sessionID, "modeId": modeID}, false)
	if err == nil {
		w.setModeValue(modeID)
	}
	return err
}

func (w *worker) applyModelLocked(modelID string) error {
	reqID := int(w.nextReqID.Add(1))
	_, _, _, err := w.sendRPC(reqID, "session/set_config_option", map[string]any{"sessionId": w.sessionID, "configId": "model", "value": modelID}, false)
	if err != nil {
		reqID2 := int(w.nextReqID.Add(1))
		_, _, _, err = w.sendRPC(reqID2, "session/set_model", map[string]any{"sessionId": w.sessionID, "modelId": modelID}, false)
	}
	if err == nil {
		w.setModelValue(modelID)
	}
	return err
}
