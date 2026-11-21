package acp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// ErrSessionNotFound 는 풀에 해당 세션의 살아있는 워커가 없을 때 반환한다
var ErrSessionNotFound = errors.New("세션을 찾을 수 없음")

// TurnResult 는 ACP 프롬프트 턴 하나의 결과다
type TurnResult struct {
	SessionID     string
	Response      string
	ToolCalls     []string
	StopReason    string
	ReusedProcess bool
}

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

// SessionPool 은 acpx 스타일의 상주 ACP 세션 프로세스 풀이다
type SessionPool struct {
	mu      sync.Mutex
	command []string
	workers map[string]*worker
}

// NewSessionPool 은 지정한 ACP 실행 명령으로 SessionPool 을 만든다.
// command 가 비어 있으면 A2A_ACP_COMMAND, 그다음 `agy acp --dangerously-skip-permissions` 를 쓴다.
func NewSessionPool(command []string) *SessionPool {
	return &SessionPool{
		command: command,
		workers: make(map[string]*worker),
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
func (p *SessionPool) Prompt(ctx context.Context, cwd, promptText, sessionID string) (TurnResult, error) {
	return p.PromptWithOptions(ctx, cwd, promptText, sessionID, "", "")
}

// PromptWithOptions 는 필요 시 모드·모델을 선적용한 뒤 상주 워커에서 프롬프트 턴을 실행한다.
// 재사용한 워커가 실패하면 한 번 새 워커로 다시 시도한다.
func (p *SessionPool) PromptWithOptions(ctx context.Context, cwd, promptText, sessionID, mode, model string) (TurnResult, error) {
	w, reused, err := p.acquireWorker(cwd, sessionID)
	if err != nil {
		return TurnResult{}, err
	}
	res, err := w.runPromptTurn(ctx, promptText, reused, mode, model)
	if err == nil {
		return res, nil
	}
	p.evictWorker(w.sessionID, w)
	if !reused {
		return TurnResult{}, err
	}
	w2, _, err2 := p.acquireWorker(cwd, w.sessionID)
	if err2 != nil {
		return TurnResult{}, err
	}
	return w2.runPromptTurn(ctx, promptText, false, mode, model)
}

func (p *SessionPool) acquireWorker(cwd, sessionID string) (*worker, bool, error) {
	p.mu.Lock()
	if sessionID != "" {
		if existing, ok := p.workers[sessionID]; ok && existing.isAlive() {
			p.mu.Unlock()
			return existing, true, nil
		}
	}
	p.mu.Unlock()

	created, err := spawnWorker(p.resolveCommand(), cwd, sessionID)
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

func (p *SessionPool) evictWorker(sessionID string, target *worker) {
	p.mu.Lock()
	if cur, ok := p.workers[sessionID]; ok && (target == nil || cur == target) {
		delete(p.workers, sessionID)
		p.mu.Unlock()
		cur.killProcess()
		return
	}
	p.mu.Unlock()
}

// Cancel 은 실행 중인 ACP 세션에 비선점 session/cancel 알림을 보낸다
func (p *SessionPool) Cancel(sessionID string) error {
	p.mu.Lock()
	w, ok := p.workers[sessionID]
	p.mu.Unlock()
	if !ok || !w.isAlive() {
		return fmt.Errorf("%s: %w", sessionID, ErrSessionNotFound)
	}
	return w.sendCancel()
}

// SetMode 는 상주 또는 재연결한 ACP 세션의 실행 모드(auto|accept-edits|plan)를 바꾼다
func (p *SessionPool) SetMode(cwd, sessionID, modeID string) error {
	w, _, err := p.acquireWorker(cwd, sessionID)
	if err != nil {
		return err
	}
	w.turnMu.Lock()
	defer w.turnMu.Unlock()
	return w.applyModeLocked(modeID)
}

// SetModel 은 ACP 세션의 모델(session/set_config_option 또는 session/set_model)을 바꾼다
func (p *SessionPool) SetModel(cwd, sessionID, modelID string) error {
	w, _, err := p.acquireWorker(cwd, sessionID)
	if err != nil {
		return err
	}
	w.turnMu.Lock()
	defer w.turnMu.Unlock()
	return w.applyModelLocked(modelID)
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
	all := make([]*worker, 0, len(p.workers))
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
			Mode:           w.getMode(),
			Model:          w.getModel(),
			ActivePrompt:   w.activePrompt.Load(),
			SupportsResume: w.supportsResume,
			TurnCount:      w.turnCount.Load(),
		})
	}
	return out
}
