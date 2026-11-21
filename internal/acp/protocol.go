// Package acp 는 ACP(Agent Client Protocol) stdio JSON-RPC 클라이언트다.
// 에이전트 프로세스(`agy acp`)를 상주 워커로 띄우고 세션 풀로 재사용한다.
package acp

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type updateParams struct {
	Update struct {
		SessionUpdate string `json:"sessionUpdate"`
		Name          string `json:"name"`
		Content       struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"update"`
}

// CollectUpdate 는 ACP session/update 알림 한 줄에서 텍스트 조각과 도구 호출명을 추출한다
func CollectUpdate(line string, chunks *[]string, tools *[]string) (*int, json.RawMessage) {
	env, ok := parseEnvelope(line)
	if !ok {
		return nil, nil
	}
	if env.Method == "session/update" && len(env.Params) > 0 && chunks != nil && tools != nil {
		appendUpdateFields(env.Params, chunks, tools)
	}
	return env.ID, env.Result
}

func parseEnvelope(line string) (envelope, bool) {
	var env envelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return envelope{}, false
	}
	return env, true
}

func appendUpdateFields(rawParams json.RawMessage, chunks *[]string, tools *[]string) {
	var up updateParams
	if err := json.Unmarshal(rawParams, &up); err != nil {
		return
	}
	switch up.Update.SessionUpdate {
	case "agent_message_chunk":
		if up.Update.Content.Type == "text" && up.Update.Content.Text != "" {
			*chunks = append(*chunks, up.Update.Content.Text)
		}
	case "tool_call":
		if up.Update.Name != "" {
			*tools = append(*tools, up.Update.Name)
		}
	}
}

// replyToAgentRequest 는 에이전트가 보낸 역방향 JSON-RPC 요청(예: session/request_permission)에 응답한다.
// 요청이 아니면 false 를 반환한다.
func replyToAgentRequest(stdin io.Writer, env envelope, cancelling bool) bool {
	if env.ID == nil || env.Method == "" {
		return false
	}
	if env.Method == "session/request_permission" {
		_ = writeJSONRPC(stdin, map[string]any{
			"jsonrpc": "2.0",
			"id":      *env.ID,
			"result":  map[string]any{"outcome": selectPermissionOutcome(env.Params, cancelling)},
		})
		return true
	}
	_ = writeJSONRPC(stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      *env.ID,
		"error":   map[string]any{"code": -32601, "message": "Method not found: " + env.Method},
	})
	return true
}

func selectPermissionOutcome(rawParams json.RawMessage, cancelling bool) map[string]any {
	if cancelling {
		return map[string]any{"outcome": "cancelled"}
	}
	var req struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	optionID := "allow"
	if json.Unmarshal(rawParams, &req) == nil && len(req.Options) > 0 {
		optionID = req.Options[0].OptionID
		for _, opt := range req.Options {
			if strings.HasPrefix(opt.Kind, "allow") && opt.OptionID != "" {
				optionID = opt.OptionID
				break
			}
		}
	}
	return map[string]any{"outcome": "selected", "optionId": optionID}
}

func writeJSONRPC(w io.Writer, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("RPC 직렬화 실패: %w", err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("RPC 전송 실패: %w", err)
	}
	return nil
}
