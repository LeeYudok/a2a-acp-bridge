package dispatch

import (
	"context"

	"github.com/LeeYudok/a2a-acp-bridge/internal/codex"
)

// CodexDelegateResult 는 a2a_delegate_to_codex 응답이다
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

const codexProtocol = "Codex-JSONL-exec"

type codexTurn struct {
	from, cwd, sandbox, prompt, tid, sessionName, taskID string
}

func (d *Dispatcher) callDelegateCodex(ctx context.Context, args map[string]any) (CodexDelegateResult, error) {
	sessionName := strArg(args, "session_name", "")
	tid, cwd := d.resolveAgentSession("codex", args, "thread_id")
	turn := codexTurn{
		from:        d.caller(args),
		cwd:         cwd,
		tid:         tid,
		sandbox:     strArg(args, "sandbox", "workspace-write"),
		prompt:      strArg(args, "prompt", ""),
		sessionName: sessionName,
	}

	msg, err := d.Board.SendMessage(turn.from, "codex", turn.prompt, "delegate", "")
	if err != nil {
		return CodexDelegateResult{}, err
	}
	turn.taskID = msg.TaskID
	if !boolArg(args, "async", false) {
		return d.executeCodexTurn(ctx, turn)
	}
	_, _ = d.Board.UpsertTask(turn.from, "codex", turn.prompt, "working", turn.taskID, turn.tid)
	d.asyncWG.Add(1)
	go func() {
		defer d.asyncWG.Done()
		actx, cancel := d.asyncContext()
		defer cancel()
		_, _ = d.executeCodexTurn(actx, turn)
	}()
	return CodexDelegateResult{
		Agent:       "codex",
		Protocol:    codexProtocol,
		ThreadID:    turn.tid,
		SessionName: turn.sessionName,
		TaskID:      turn.taskID,
		AllMessages: []string{},
		Async:       true,
	}, nil
}

func (d *Dispatcher) executeCodexTurn(ctx context.Context, t codexTurn) (CodexDelegateResult, error) {
	res, err := codex.Run(ctx, t.cwd, t.sandbox, t.prompt, t.tid)
	if err != nil {
		_, _ = d.Board.UpsertTask(t.from, "codex", t.prompt, "failed", t.taskID, t.tid)
		return CodexDelegateResult{}, err
	}
	if t.sessionName != "" && res.ThreadID != "" {
		_, _ = d.Board.UpsertSession(t.sessionName, "codex", res.ThreadID, t.cwd, "", "")
	}
	_, _ = d.Board.SendMessage("codex", t.from, res.Response, "reply", t.taskID)
	_, _ = d.Board.UpsertTask(t.from, "codex", t.prompt, "completed", t.taskID, res.ThreadID)
	return CodexDelegateResult{
		Agent:       "codex",
		Protocol:    codexProtocol,
		ThreadID:    res.ThreadID,
		SessionName: t.sessionName,
		TaskID:      t.taskID,
		Response:    res.Response,
		AllMessages: res.AllMessages,
	}, nil
}
