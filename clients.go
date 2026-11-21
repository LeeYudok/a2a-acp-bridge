package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AgyDelegateResult 는 ACP 를 통한 AGY 위임 호출 결과 DTO다
type AgyDelegateResult struct {
	Agent         string   `json:"agent"`
	Protocol      string   `json:"protocol"`
	SessionID     string   `json:"session_id"`
	SessionName   string   `json:"session_name,omitempty"`
	TaskID        string   `json:"task_id,omitempty"`
	Response      string   `json:"response"`
	ToolCalls     []string `json:"tool_calls"`
	StopReason    string   `json:"stop_reason"`
	ReusedProcess bool     `json:"reused_process"`
	Async         bool     `json:"async,omitempty"`
}

// CodexDelegateResult 는 Codex 위임 호출 결과 DTO다
type CodexDelegateResult struct {
	Agent       string   `json:"agent"`
	Protocol    string   `json:"protocol"`
	ThreadID    string   `json:"thread_id"`
	SessionName string   `json:"session_name,omitempty"`
	TaskID      string   `json:"task_id,omitempty"`
	Response    string   `json:"response"`
	AllMessages []string `json:"all_messages"`
	Async       bool     `json:"async,omitempty"`
}

type rpcErrorObj struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErrorObj    `json:"error,omitempty"`
}

type acpUpdateParams struct {
	Update struct {
		SessionUpdate string `json:"sessionUpdate"`
		Name          string `json:"name"`
		Content       struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"update"`
}

func readActivePersonalUser(cwd string) string {
	p := filepath.Join(cwd, ".claude", "state", "personal-gate", "current")
	raw, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func needsPersonalGateReply(text string) bool {
	markers := []string{"사용자를 확인", "사용자가 누구인지", "누구이신가요", "personal-layer.sh use"}
	for _, m := range markers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// CollectACPUpdate 는 ACP session/update 알림에서 텍스트 조각과 도구 호출명을 추출한다
func CollectACPUpdate(line string, chunks *[]string, tools *[]string) (*int, json.RawMessage) {
	env, ok := parseACPEnvelope(line)
	if !ok {
		return nil, nil
	}
	if env.Method == "session/update" && len(env.Params) > 0 && chunks != nil && tools != nil {
		appendUpdateFields(env.Params, chunks, tools)
	}
	return env.ID, env.Result
}

func parseACPEnvelope(line string) (jsonRPCEnvelope, bool) {
	var env jsonRPCEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return jsonRPCEnvelope{}, false
	}
	return env, true
}

func appendUpdateFields(rawParams json.RawMessage, chunks *[]string, tools *[]string) {
	var up acpUpdateParams
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

// handleAgentToClientRPC 는 에이전트가 클라이언트로 보낸 역방향 JSON-RPC 요청(예: session/request_permission)에 응답한다
func handleAgentToClientRPC(stdin io.Writer, env jsonRPCEnvelope, cancelling bool) bool {
	if env.ID == nil || env.Method == "" {
		return false
	}
	if env.Method == "session/request_permission" {
		outcome := selectPermissionOutcome(env.Params, cancelling)
		_ = writeJSONRPC(stdin, map[string]any{
			"jsonrpc": "2.0",
			"id":      *env.ID,
			"result":  map[string]any{"outcome": outcome},
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

// DelegateToAgy 는 기본 세션 풀을 통해 ACP JSON-RPC 2.0 세션에서 프롬프트를 실행한다
func DelegateToAgy(ctx context.Context, cwd, promptText, sessionID string) (AgyDelegateResult, error) {
	return defaultPool.Prompt(ctx, cwd, promptText, sessionID)
}

// ParseCodexJSONL 은 `codex exec --json` 의 JSONL 출력에서 thread_id 와 메시지를 추출한다
func ParseCodexJSONL(stdout, initialThreadID string) (string, []string, error) {
	threadID := initialThreadID
	msgs := []string{}
	sc := bufio.NewScanner(strings.NewReader(stdout))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNumber := 0
	for sc.Scan() {
		lineNumber++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var evt struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Item     struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := json.Unmarshal(line, &evt); err != nil {
			return "", nil, fmt.Errorf("codex JSONL line %d: %w", lineNumber, err)
		}
		switch evt.Type {
		case "thread.started":
			if evt.ThreadID != "" {
				threadID = evt.ThreadID
			}
		case "item.completed":
			if evt.Item.Type == "agent_message" && evt.Item.Text != "" {
				msgs = append(msgs, evt.Item.Text)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", nil, fmt.Errorf("scan codex JSONL line %d: %w", lineNumber+1, err)
	}
	return threadID, msgs, nil
}

// DelegateToCodex 는 `codex exec --json` (또는 resume)으로 작업을 위임한다
func DelegateToCodex(ctx context.Context, cwd, sandbox, promptText, threadID string) (CodexDelegateResult, error) {
	tid, msgs, err := runCodexOnce(ctx, cwd, sandbox, promptText, threadID)
	if err != nil {
		return CodexDelegateResult{}, err
	}
	last := ""
	if len(msgs) > 0 {
		last = msgs[len(msgs)-1]
	}
	if activeUser := readActivePersonalUser(cwd); activeUser != "" && tid != "" && needsPersonalGateReply(last) {
		followup := fmt.Sprintf("%s\n(`.claude/scripts/personal-layer.sh use %s` 단독 실행 후 원래 요청에 답하세요: %s)", activeUser, activeUser, promptText)
		tid2, msgs2, err := runCodexOnce(ctx, cwd, sandbox, followup, tid)
		if err != nil {
			return CodexDelegateResult{}, err
		}
		if len(msgs2) > 0 {
			tid, msgs, last = tid2, msgs2, msgs2[len(msgs2)-1]
		}
	}
	return CodexDelegateResult{
		Agent:       "codex",
		Protocol:    "Codex-JSONL-exec",
		ThreadID:    tid,
		Response:    last,
		AllMessages: msgs,
	}, nil
}

func runCodexOnce(ctx context.Context, cwd, sandbox, promptText, threadID string) (string, []string, error) {
	if sandbox == "" {
		sandbox = "workspace-write"
	}
	args := []string{"exec", "--skip-git-repo-check", "--json", "-s", sandbox, promptText}
	if threadID != "" {
		args = []string{"exec", "resume", "--skip-git-repo-check", "--json", threadID, promptText}
	}
	cmdName := "codex"
	if parts := strings.Fields(os.Getenv("A2A_CODEX_COMMAND")); len(parts) > 0 {
		cmdName = parts[0]
		args = append(parts[1:], args...)
	}
	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if err := ctx.Err(); err != nil {
		return "", nil, codexOutputError("codex exec", err, stderr.String())
	}
	if runErr != nil {
		return "", nil, codexOutputError("codex exec", runErr, stderr.String())
	}
	tid, msgs, err := ParseCodexJSONL(stdout.String(), threadID)
	if err != nil {
		return "", nil, codexOutputError("parse codex exec stdout", err, stderr.String())
	}
	if tid == "" && len(msgs) == 0 {
		return "", nil, codexOutputError("parse codex exec stdout", errors.New("no thread ID or agent messages"), stderr.String())
	}
	return tid, msgs, nil
}

func codexOutputError(operation string, err error, stderr string) error {
	if diagnostic := strings.TrimSpace(stderr); diagnostic != "" {
		return fmt.Errorf("%s: %w (stderr: %s)", operation, err, diagnostic)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
