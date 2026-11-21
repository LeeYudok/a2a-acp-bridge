package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// worker 는 ACP 에이전트 자식 프로세스 하나와 그 위에 열린 세션 하나다.
// turnMu 는 턴을 직렬화하고, writeMu 는 stdin 쓰기만 잡아서 턴 도중에도 session/cancel 을 넣을 수 있게 한다.
type worker struct {
	turnMu         sync.Mutex
	writeMu        sync.Mutex
	metaMu         sync.RWMutex
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	reader         *bufio.Scanner
	sessionID      string
	cwd            string
	mode           string
	model          string
	supportsResume bool
	supportsClose  bool
	nextReqID      atomic.Int64
	turnCount      atomic.Int64
	activePrompt   atomic.Bool
	cancelling     atomic.Bool
	exited         atomic.Bool
}

func spawnWorker(command []string, cwd, sessionID string) (*worker, error) {
	w, err := startWorker(context.Background(), command, cwd)
	if err != nil {
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

func startWorker(ctx context.Context, command []string, cwd string) (*worker, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("ACP 실행 명령이 비어 있음")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
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
	w := &worker{
		cmd:    cmd,
		stdin:  stdin,
		reader: reader,
		cwd:    cwd,
	}
	go func() {
		_ = cmd.Wait()
		w.exited.Store(true)
	}()

	if err := w.initialize(); err != nil {
		w.killProcess()
		return nil, err
	}
	return w, nil
}

func (w *worker) initialize() error {
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

func (w *worker) openSession(sessionID string) (string, error) {
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

// tryReconnectSession 은 session/resume 을 먼저 시도하고, 안 되면 session/load 로 폴백한다.
// load 중 쏟아지는 과거 session/update 는 sendRPC 의 suppressUpdates 로 버린다.
func (w *worker) tryReconnectSession(sessionID string) (string, bool) {
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

func (w *worker) getMode() string {
	w.metaMu.RLock()
	defer w.metaMu.RUnlock()
	return w.mode
}

func (w *worker) setModeValue(modeID string) {
	w.metaMu.Lock()
	w.mode = modeID
	w.metaMu.Unlock()
}

func (w *worker) getModel() string {
	w.metaMu.RLock()
	defer w.metaMu.RUnlock()
	return w.model
}

func (w *worker) setModelValue(modelID string) {
	w.metaMu.Lock()
	w.model = modelID
	w.metaMu.Unlock()
}

func (w *worker) isAlive() bool {
	return w != nil && !w.exited.Load()
}

func (w *worker) sendCancel() error {
	w.cancelling.Store(true)
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	return writeJSONRPC(w.stdin, map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/cancel",
		"params":  map[string]any{"sessionId": w.sessionID},
	})
}

func (w *worker) gracefulClose() {
	if w.supportsClose && w.isAlive() {
		w.turnMu.Lock()
		reqID := int(w.nextReqID.Add(1))
		_, _, _, _ = w.sendRPC(reqID, "session/close", map[string]any{"sessionId": w.sessionID}, true)
		w.turnMu.Unlock()
	}
	w.killProcess()
}

func (w *worker) killProcess() {
	w.exited.Store(true)
	if w.stdin != nil {
		_ = w.stdin.Close()
	}
	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
}
