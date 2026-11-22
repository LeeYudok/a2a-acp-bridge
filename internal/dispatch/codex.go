package dispatch

import (
	"context"
	"fmt"
	"os"

	"github.com/LeeYudok/a2a-acp-bridge/internal/agents"

	"github.com/LeeYudok/a2a-acp-bridge/internal/codex"
)

// CodexDelegateResult 는 a2a_delegate_to_codex 응답이다
type CodexDelegateResult struct {
	Agent        string   `json:"agent"`
	Protocol     string   `json:"protocol"`
	ThreadID     string   `json:"thread_id"`
	SessionName  string   `json:"session_name,omitempty"`
	TaskID       string   `json:"task_id,omitempty"`
	Response     string   `json:"response"`
	AllMessages  []string `json:"all_messages"`
	BoardWarning string   `json:"board_warning,omitempty"`
	Async        bool     `json:"async,omitempty"`
}

const codexProtocol = "Codex-JSONL-exec"

type codexTurn struct {
	from, target, cwd, sandbox, prompt, tid, sessionName, taskID string
}

func (d *Dispatcher) callDelegateCodex(ctx context.Context, args map[string]any) (CodexDelegateResult, error) {
	target := os.Getenv("A2A_CODEX_AGENT_ID")
	if target == "" {
		target = "codex"
	}
	families := agents.Families(agents.Cards())
	if _, ok := families[target]; !ok {
		return CodexDelegateResult{}, fmt.Errorf("등록되지 않은 A2A_CODEX_AGENT_ID %q", target)
	}
	sessionName := strArg(args, "session_name", "")
	tid, cwd := d.resolveAgentSession(target, args, "thread_id")
	sandbox := strArg(args, "sandbox", "")
	if sandbox == "" && sessionName != "" {
		if rec, err := d.Board.ResolveSession(sessionName, target); err == nil {
			sandbox = rec.Mode
		}
	}
	turn := codexTurn{
		from:        d.caller(args),
		target:      target,
		cwd:         cwd,
		tid:         tid,
		sandbox:     sandbox,
		prompt:      strArg(args, "prompt", ""),
		sessionName: sessionName,
	}

	msg, err := d.Board.StartDelegation(turn.from, target, turn.prompt, turn.tid, families)
	if err != nil {
		return CodexDelegateResult{}, err
	}
	turn.taskID = msg.TaskID
	if !boolArg(args, "async", false) {
		return d.executeCodexTurn(ctx, turn)
	}
	d.asyncWG.Add(1)
	go func() {
		defer d.asyncWG.Done()
		actx, cancel := d.asyncContext()
		defer cancel()
		_, _ = d.executeCodexTurn(actx, turn)
	}()
	return CodexDelegateResult{
		Agent:       turn.target,
		Protocol:    codexProtocol,
		ThreadID:    turn.tid,
		SessionName: turn.sessionName,
		TaskID:      turn.taskID,
		AllMessages: []string{},
		Async:       true,
	}, nil
}

func (d *Dispatcher) executeCodexTurn(ctx context.Context, t codexTurn) (CodexDelegateResult, error) {
	stopHeartbeat := d.keepDelegationAlive(ctx, t.taskID)
	defer stopHeartbeat()
	sandbox := codex.EffectiveSandbox(t.sandbox, t.tid)
	res, err := codex.Run(ctx, t.cwd, sandbox, t.prompt, t.tid)
	if err != nil {
		_, _ = d.Board.UpsertTask(t.from, t.target, t.prompt, "failed", t.taskID, t.tid)
		return CodexDelegateResult{}, err
	}
	if t.sessionName != "" && res.ThreadID != "" {
		_, _ = d.Board.UpsertSession(t.sessionName, t.target, res.ThreadID, t.cwd, sandbox, "")
	}
	warning := ""
	if _, err := d.Board.CompleteDelegation(t.target, t.from, res.Response, t.taskID, res.ThreadID); err != nil {
		warning = err.Error()
	}
	return CodexDelegateResult{
		Agent:        t.target,
		Protocol:     codexProtocol,
		ThreadID:     res.ThreadID,
		SessionName:  t.sessionName,
		TaskID:       t.taskID,
		BoardWarning: warning,
		Response:     res.Response,
		AllMessages:  res.AllMessages,
	}, nil
}
