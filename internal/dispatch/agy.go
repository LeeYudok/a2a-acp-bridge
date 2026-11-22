package dispatch

import (
	"context"
)

// AgyDelegateResult 는 acp_delegate_to_agy 응답이다
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

const agyProtocol = "ACP-stdio-jsonrpc-2.0"

type agyTurn struct {
	from, cwd, prompt, sid, sessionName, mode, model, taskID string
}

func (d *Dispatcher) callDelegateAgy(ctx context.Context, args map[string]any) (AgyDelegateResult, error) {
	sessionName := strArg(args, "session_name", "")
	sid, cwd := d.resolveAgentSession("agy", args, "session_id")
	turn := agyTurn{
		from:        d.caller(args),
		cwd:         cwd,
		sid:         sid,
		prompt:      strArg(args, "prompt", ""),
		sessionName: sessionName,
		mode:        strArg(args, "mode", ""),
		model:       strArg(args, "model", ""),
	}
	if _, err := d.pool(); err != nil {
		return AgyDelegateResult{}, err
	}

	msg, err := d.startDelegation(turn.from, "agy", turn.prompt, turn.sid)
	if err != nil {
		return AgyDelegateResult{}, err
	}
	turn.taskID = msg.TaskID
	if !boolArg(args, "async", false) {
		return d.executeAgyTurn(ctx, turn)
	}
	d.asyncWG.Add(1)
	go func() {
		defer d.asyncWG.Done()
		actx, cancel := d.asyncContext()
		defer cancel()
		_, _ = d.executeAgyTurn(actx, turn)
	}()
	return AgyDelegateResult{
		Agent:       "agy",
		Protocol:    agyProtocol,
		SessionID:   turn.sid,
		SessionName: turn.sessionName,
		TaskID:      turn.taskID,
		StopReason:  "queued",
		ToolCalls:   []string{},
		Async:       true,
	}, nil
}

func (d *Dispatcher) executeAgyTurn(ctx context.Context, t agyTurn) (AgyDelegateResult, error) {
	pool, err := d.pool()
	if err != nil {
		return AgyDelegateResult{}, err
	}
	res, err := pool.PromptWithOptions(ctx, t.cwd, t.prompt, t.sid, t.mode, t.model)
	if err != nil {
		_, _ = d.Board.UpsertTask(t.from, "agy", t.prompt, "failed", t.taskID, t.sid)
		return AgyDelegateResult{}, err
	}
	if t.sessionName != "" {
		_, _ = d.Board.UpsertSession(t.sessionName, "agy", res.SessionID, t.cwd, t.mode, t.model)
	}
	if _, err := d.Board.CompleteDelegation("agy", t.from, res.Response, t.taskID, res.SessionID); err != nil {
		return AgyDelegateResult{}, err
	}
	return AgyDelegateResult{
		Agent:         "agy",
		Protocol:      agyProtocol,
		SessionID:     res.SessionID,
		SessionName:   t.sessionName,
		TaskID:        t.taskID,
		Response:      res.Response,
		ToolCalls:     res.ToolCalls,
		StopReason:    res.StopReason,
		ReusedProcess: res.ReusedProcess,
	}, nil
}
