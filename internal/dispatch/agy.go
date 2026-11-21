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
	turn := agyTurn{
		from:        d.caller(args),
		cwd:         strArg(args, "cwd", d.DefaultCwd),
		prompt:      strArg(args, "prompt", ""),
		sid:         d.resolveAgentSessionID("agy", strArg(args, "session_id", ""), sessionName),
		sessionName: sessionName,
		mode:        strArg(args, "mode", ""),
		model:       strArg(args, "model", ""),
	}
	if _, err := d.pool(); err != nil {
		return AgyDelegateResult{}, err
	}

	msg, err := d.Board.SendMessage(turn.from, "agy", turn.prompt, "delegate", "")
	if err != nil {
		return AgyDelegateResult{}, err
	}
	turn.taskID = msg.TaskID
	if !boolArg(args, "async", false) {
		return d.executeAgyTurn(ctx, turn)
	}
	_, _ = d.Board.UpsertTask(turn.from, "agy", turn.prompt, "working", turn.taskID, turn.sid)
	d.asyncWG.Add(1)
	go func() {
		defer d.asyncWG.Done()
		_, _ = d.executeAgyTurn(context.Background(), turn)
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
	_, _ = d.Board.SendMessage("agy", t.from, res.Response, "reply", t.taskID)
	_, _ = d.Board.UpsertTask(t.from, "agy", t.prompt, "completed", t.taskID, res.SessionID)
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
