package dispatch

import (
	"context"
	"fmt"
	"slices"

	"github.com/LeeYudok/a2a-acp-bridge/internal/claude"
)

// ClaudeDelegateResult 는 a2a_delegate_to_claude 응답이다
type ClaudeDelegateResult struct {
	Agent          string  `json:"agent"`
	Protocol       string  `json:"protocol"`
	SessionID      string  `json:"session_id"`
	SessionName    string  `json:"session_name,omitempty"`
	TaskID         string  `json:"task_id,omitempty"`
	PermissionMode string  `json:"permission_mode"`
	Response       string  `json:"response"`
	StopReason     string  `json:"stop_reason,omitempty"`
	CostUSD        float64 `json:"cost_usd,omitempty"`
	Async          bool    `json:"async,omitempty"`
}

const claudeProtocol = "Claude-Code-print-json"

type claudeTurn struct {
	from, cwd, mode, prompt, sid, sessionName, taskID string
}

func (d *Dispatcher) callDelegateClaude(ctx context.Context, args map[string]any) (ClaudeDelegateResult, error) {
	sessionName := strArg(args, "session_name", "")
	sid, cwd := d.resolveAgentSession("claude", args, "session_id")
	turn := claudeTurn{
		from:        d.caller(args),
		cwd:         cwd,
		sid:         sid,
		mode:        strArg(args, "permission_mode", claude.DefaultPermissionMode),
		prompt:      strArg(args, "prompt", ""),
		sessionName: sessionName,
	}
	if !slices.Contains(claude.PermissionModes, turn.mode) {
		return ClaudeDelegateResult{}, fmt.Errorf("permission_mode 는 %v 중 하나여야 합니다: %s", claude.PermissionModes, turn.mode)
	}

	msg, err := d.startDelegation(turn.from, "claude", turn.prompt, turn.sid)
	if err != nil {
		return ClaudeDelegateResult{}, err
	}
	turn.taskID = msg.TaskID
	if !boolArg(args, "async", false) {
		return d.executeClaudeTurn(ctx, turn)
	}
	d.asyncWG.Add(1)
	go func() {
		defer d.asyncWG.Done()
		actx, cancel := d.asyncContext()
		defer cancel()
		_, _ = d.executeClaudeTurn(actx, turn)
	}()
	return ClaudeDelegateResult{
		Agent:          "claude",
		Protocol:       claudeProtocol,
		SessionID:      turn.sid,
		SessionName:    turn.sessionName,
		TaskID:         turn.taskID,
		PermissionMode: turn.mode,
		Async:          true,
	}, nil
}

func (d *Dispatcher) executeClaudeTurn(ctx context.Context, t claudeTurn) (ClaudeDelegateResult, error) {
	res, err := claude.Run(ctx, t.cwd, t.mode, t.prompt, t.sid)
	if err != nil {
		_, _ = d.Board.UpsertTask(t.from, "claude", t.prompt, "failed", t.taskID, t.sid)
		return ClaudeDelegateResult{}, err
	}
	if t.sessionName != "" && res.SessionID != "" {
		_, _ = d.Board.UpsertSession(t.sessionName, "claude", res.SessionID, t.cwd, t.mode, "")
	}
	if _, err := d.Board.CompleteDelegation("claude", t.from, res.Response, t.taskID, res.SessionID); err != nil {
		return ClaudeDelegateResult{}, err
	}
	return ClaudeDelegateResult{
		Agent:          "claude",
		Protocol:       claudeProtocol,
		SessionID:      res.SessionID,
		SessionName:    t.sessionName,
		TaskID:         t.taskID,
		PermissionMode: t.mode,
		Response:       res.Response,
		StopReason:     res.StopReason,
		CostUSD:        res.CostUSD,
	}, nil
}
