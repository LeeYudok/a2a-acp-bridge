package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

var defaultPool = NewSessionPool(nil)

// WorkerStatus 는 상주 ACP 워커 프로세스 상태 스냅샷이다
type WorkerStatus struct {
	SessionID      string `json:"session_id"`
	PID            int    `json:"pid"`
	Cwd            string `json:"cwd"`
	Mode           string `json:"mode,omitempty"`
	Model          string `json:"model,omitempty"`
	ActivePrompt   bool   `json:"active_prompt"`
	SupportsResume bool   `json:"supports_resume"`
	TurnCount      int64  `json:"turn_count"`
}

// SessionControlResult 는 acp_session_control 도구의 응답 DTO다
type SessionControlResult struct {
	Action   string         `json:"action"`
	Session  string         `json:"session,omitempty"`
	Mode     string         `json:"mode,omitempty"`
	Model    string         `json:"model,omitempty"`
	Status   string         `json:"status"`
	Workers  []WorkerStatus `json:"workers,omitempty"`
	Sessions []NamedSession `json:"sessions,omitempty"`
}

type acpWorker struct {
	turnMu         sync.Mutex
	writeMu        sync.Mutex
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

// SessionPool 은 acpx 스타일의 상주 ACP 세션 프로세스 풀이다
type SessionPool struct {
	mu      sync.Mutex
	command []string
	workers map[string]*acpWorker
}

// NewSessionPool 은 지정한 ACP 실행 명령으로 SessionPool 을 생성한다
func NewSessionPool(command []string) *SessionPool {
	return &SessionPool{
		command: command,
		workers: make(map[string]*acpWorker),
	}
}

func (p *SessionPool) resolveCommand() []string {
	if len(p.command) > 0 {
		return p.command
	}
	if env := os.Getenv("A2A_ACP_COMMAND"); env != "" {
		if fields := strings.Fields(env); len(fields) > 0 {
			return fields
		}
	}
	return []string{"agy", "acp", "--dangerously-skip-permissions"}
}

// Prompt 는 상주 워커를 재사용하거나 새로 띄워 ACP 프롬프트 턴을 실행한다
func (p *SessionPool) Prompt(ctx context.Context, cwd, promptText, sessionID string) (AgyDelegateResult, error) {
	w, reused, err := p.acquireWorker(cwd, sessionID)
	if err != nil {
		return AgyDelegateResult{}, err
	}
	res, err := w.runPromptTurn(ctx, promptText, reused)
	if err == nil {
		return res, nil
	}
	p.evictWorker(w.sessionID, w)
	if !reused {
		return AgyDelegateResult{}, err
	}
	w2, _, err2 := p.acquireWorker(cwd, w.sessionID)
	if err2 != nil {
		return AgyDelegateResult{}, err
	}
	return w2.runPromptTurn(ctx, promptText, false)
}

func (p *SessionPool) acquireWorker(cwd, sessionID string) (*acpWorker, bool, error) {
	p.mu.Lock()
	if sessionID != "" {
		if existing, ok := p.workers[sessionID]; ok && existing.isAlive() {
			p.mu.Unlock()
			return existing, true, nil
		}
	}
	p.mu.Unlock()

	created, err := spawnACPWorker(p.resolveCommand(), cwd, sessionID)
	if err != nil {
		return nil, false, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.workers[created.sessionID]; ok && existing.isAlive() {
		created.killProcess()
		return existing, true, nil
	}
	p.workers[created.sessionID] = created
	return created, false, nil
}

func (p *SessionPool) evictWorker(sessionID string, target *acpWorker) {
	p.mu.Lock()
	if cur, ok := p.workers[sessionID]; ok && (target == nil || cur == target) {
		delete(p.workers, sessionID)
		p.mu.Unlock()
		cur.killProcess()
		return
	}
	p.mu.Unlock()
}

// Cancel 은 실행 중인 ACP 세션에 비선점 session/cancel 알림을 전송한다
func (p *SessionPool) Cancel(sessionID string) error {
	p.mu.Lock()
	w, ok := p.workers[sessionID]
	p.mu.Unlock()
	if !ok || !w.isAlive() {
		return fmt.Errorf("%s: %w", sessionID, ErrSessionNotFound)
	}
	return w.sendCancel()
}

// SetMode 는 상주 또는 재연결된 ACP 세션의 실행 모드(auto|accept-edits|plan)를 변경한다
func (p *SessionPool) SetMode(cwd, sessionID, modeID string) error {
	w, _, err := p.acquireWorker(cwd, sessionID)
	if err != nil {
		return err
	}
	w.turnMu.Lock()
	defer w.turnMu.Unlock()
	reqID := int(w.nextReqID.Add(1))
	_, _, _, err = w.sendRPC(reqID, "session/set_mode", map[string]any{
		"sessionId": w.sessionID,
		"modeId":    modeID,
	}, false)
	if err == nil {
		w.mode = modeID
	}
	return err
}

// SetModel 은 ACP 세션의 모델 설정(session/set_config_option 또는 session/set_model)을 변경한다
func (p *SessionPool) SetModel(cwd, sessionID, modelID string) error {
	w, _, err := p.acquireWorker(cwd, sessionID)
	if err != nil {
		return err
	}
	w.turnMu.Lock()
	defer w.turnMu.Unlock()
	reqID := int(w.nextReqID.Add(1))
	_, _, _, err = w.sendRPC(reqID, "session/set_config_option", map[string]any{
		"sessionId": w.sessionID,
		"configId":  "model",
		"value":     modelID,
	}, false)
	if err != nil {
		reqID2 := int(w.nextReqID.Add(1))
		_, _, _, err = w.sendRPC(reqID2, "session/set_model", map[string]any{
			"sessionId": w.sessionID,
			"modelId":   modelID,
		}, false)
	}
	if err == nil {
		w.model = modelID
	}
	return err
}

// CloseSession 은 ACP session/close 를 호출하고 상주 워커 프로세스를 종료한다
func (p *SessionPool) CloseSession(sessionID string) error {
	p.mu.Lock()
	w, ok := p.workers[sessionID]
	if ok {
		delete(p.workers, sessionID)
	}
	p.mu.Unlock()
	if !ok {
		return nil
	}
	w.gracefulClose()
	return nil
}

// CloseAll 은 풀의 모든 상주 ACP 워커 프로세스를 정리한다
func (p *SessionPool) CloseAll() {
	p.mu.Lock()
	all := make([]*acpWorker, 0, len(p.workers))
	for id, w := range p.workers {
		delete(p.workers, id)
		all = append(all, w)
	}
	p.mu.Unlock()
	for _, w := range all {
		w.killProcess()
	}
}

// Snapshots 는 현재 살아있는 모든 ACP 워커의 상태 목록을 반환한다
func (p *SessionPool) Snapshots() []WorkerStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []WorkerStatus{}
	for _, w := range p.workers {
		if !w.isAlive() {
			continue
		}
		pid := 0
		if w.cmd != nil && w.cmd.Process != nil {
			pid = w.cmd.Process.Pid
		}
		out = append(out, WorkerStatus{
			SessionID:      w.sessionID,
			PID:            pid,
			Cwd:            w.cwd,
			Mode:           w.mode,
			Model:          w.model,
			ActivePrompt:   w.activePrompt.Load(),
			SupportsResume: w.supportsResume,
			TurnCount:      w.turnCount.Load(),
		})
	}
	return out
}

func (w *acpWorker) isAlive() bool {
	return w != nil && !w.exited.Load()
}

func (w *acpWorker) sendCancel() error {
	w.cancelling.Store(true)
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	return writeJSONRPC(w.stdin, map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/cancel",
		"params":  map[string]any{"sessionId": w.sessionID},
	})
}

func (w *acpWorker) gracefulClose() {
	if w.supportsClose && w.isAlive() {
		w.turnMu.Lock()
		reqID := int(w.nextReqID.Add(1))
		_, _, _, _ = w.sendRPC(reqID, "session/close", map[string]any{"sessionId": w.sessionID}, true)
		w.turnMu.Unlock()
	}
	w.killProcess()
}

func (w *acpWorker) killProcess() {
	w.exited.Store(true)
	if w.stdin != nil {
		_ = w.stdin.Close()
	}
	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
}
