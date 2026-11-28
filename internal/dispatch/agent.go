package dispatch

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/LeeYudok/a2a-acp-bridge/internal/acp"
	"github.com/LeeYudok/a2a-acp-bridge/internal/agents"
	"github.com/LeeYudok/a2a-acp-bridge/internal/pi"
)

// PiDelegateResult 는 Pi 에이전트 위임 결과다.
type PiDelegateResult struct {
	Agent        string  `json:"agent"`
	Protocol     string  `json:"protocol"`
	SessionID    string  `json:"session_id"`
	SessionName  string  `json:"session_name,omitempty"`
	TaskID       string  `json:"task_id,omitempty"`
	Sandbox      string  `json:"sandbox"`
	Model        string  `json:"model,omitempty"`
	Response     string  `json:"response"`
	StopReason   string  `json:"stop_reason,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	BoardWarning string  `json:"board_warning,omitempty"`
	Async        bool    `json:"async,omitempty"`
}

// CommandDelegateResult 는 범용 CLI 명령 위임 결과다.
type CommandDelegateResult struct {
	Agent        string `json:"agent"`
	Protocol     string `json:"protocol"`
	TaskID       string `json:"task_id,omitempty"`
	Response     string `json:"response"`
	BoardWarning string `json:"board_warning,omitempty"`
	Async        bool   `json:"async,omitempty"`
}

const piProtocol = "Pi-print-json"

func (d *Dispatcher) callDelegateAgent(ctx context.Context, args map[string]any) (any, error) {
	agentID := strArg(args, "agent_id", "")
	if agentID == "" {
		return nil, fmt.Errorf("a2a_delegate_to_agent: agent_id 가 필요합니다")
	}
	card, ok := agents.Find(agentID)
	if !ok {
		return nil, fmt.Errorf("등록되지 않은 에이전트 ID: %s", agentID)
	}
	if !card.Executable || card.Executor == nil || len(card.Executor.Command) == 0 {
		return nil, fmt.Errorf("에이전트 %q 에 실행 가능한 executor 가 설정되지 않았습니다", agentID)
	}
	switch card.Executor.Type {
	case "pi":
		return d.callDelegatePi(ctx, card, args)
	case "codex":
		return d.callDelegateCodex(ctx, args)
	case "claude":
		return d.callDelegateClaude(ctx, args)
	case "acp":
		if card.ID == "agy" {
			return d.callDelegateAgy(ctx, args)
		}
		return d.callDelegateExtraACP(ctx, card, args)
	case "command":
		return d.callDelegateCommand(ctx, card, args)
	default:
		return nil, fmt.Errorf("지원하지 않는 executor.type %q (에이전트: %s)", card.Executor.Type, agentID)
	}
}

type piTurn struct {
	agentID, from, cwd, sandbox, model, prompt, sid, sessionName, taskID string
	command                                                              []string
}

func resolvePiSandbox(args map[string]any) string {
	if s := strArg(args, "sandbox", ""); s != "" {
		return s
	}
	pm := strArg(args, "permission_mode", "")
	mode := strArg(args, "mode", "")
	if pm == "acceptEdits" || mode == "accept-edits" || mode == "auto" {
		return "workspace-write"
	}
	return pi.DefaultSandbox
}

func (d *Dispatcher) callDelegatePi(ctx context.Context, card agents.Card, args map[string]any) (PiDelegateResult, error) {
	sessionName := strArg(args, "session_name", "")
	sid, cwd := d.resolveAgentSession(card.ID, args, "session_id")
	turn := piTurn{
		agentID:     card.ID,
		from:        d.caller(args),
		cwd:         cwd,
		sandbox:     resolvePiSandbox(args),
		model:       strArg(args, "model", ""),
		prompt:      strArg(args, "prompt", ""),
		sid:         sid,
		sessionName: sessionName,
		command:     card.Executor.Command,
	}
	msg, err := d.startDelegation(turn.from, turn.agentID, turn.prompt, turn.sid)
	if err != nil {
		return PiDelegateResult{}, err
	}
	turn.taskID = msg.TaskID
	if !boolArg(args, "async", false) {
		return d.executePiTurn(ctx, turn)
	}
	d.asyncWG.Add(1)
	go func() {
		defer d.asyncWG.Done()
		actx, cancel := d.asyncContext()
		defer cancel()
		_, _ = d.executePiTurn(actx, turn)
	}()
	return PiDelegateResult{
		Agent:       turn.agentID,
		Protocol:    piProtocol,
		SessionID:   turn.sid,
		SessionName: turn.sessionName,
		TaskID:      turn.taskID,
		Sandbox:     turn.sandbox,
		Model:       turn.model,
		Async:       true,
	}, nil
}

func (d *Dispatcher) executePiTurn(ctx context.Context, t piTurn) (PiDelegateResult, error) {
	stopHeartbeat := d.keepDelegationAlive(ctx, t.taskID)
	defer stopHeartbeat()
	res, err := pi.Run(ctx, pi.Options{
		Command:   t.command,
		Cwd:       t.cwd,
		Prompt:    t.prompt,
		SessionID: t.sid,
		Sandbox:   t.sandbox,
		Model:     t.model,
	})
	if err != nil {
		_, _ = d.Board.UpsertTask(t.from, t.agentID, t.prompt, "failed", t.taskID, t.sid)
		return PiDelegateResult{}, err
	}
	if t.sessionName != "" && res.SessionID != "" {
		_, _ = d.Board.UpsertSession(t.sessionName, t.agentID, res.SessionID, t.cwd, t.sandbox, res.Model)
	}
	warning := ""
	if _, err := d.Board.CompleteDelegation(t.agentID, t.from, res.Response, t.taskID, res.SessionID); err != nil {
		warning = err.Error()
	}
	return PiDelegateResult{
		Agent:        t.agentID,
		Protocol:     piProtocol,
		SessionID:    res.SessionID,
		SessionName:  t.sessionName,
		TaskID:       t.taskID,
		Sandbox:      t.sandbox,
		Model:        res.Model,
		Response:     res.Response,
		StopReason:   res.StopReason,
		CostUSD:      res.CostUSD,
		BoardWarning: warning,
	}, nil
}

func (d *Dispatcher) getExtraPool(card agents.Card) *acp.SessionPool {
	d.extraPoolsMu.Lock()
	defer d.extraPoolsMu.Unlock()
	if d.extraPools == nil {
		d.extraPools = make(map[string]*acp.SessionPool)
	}
	if p, ok := d.extraPools[card.ID]; ok {
		return p
	}
	p := acp.NewSessionPool(card.Executor.Command)
	d.extraPools[card.ID] = p
	return p
}

func (d *Dispatcher) callDelegateExtraACP(ctx context.Context, card agents.Card, args map[string]any) (AgyDelegateResult, error) {
	sessionName := strArg(args, "session_name", "")
	sid, cwd := d.resolveAgentSession(card.ID, args, "session_id")
	turn := agyTurn{
		from:        d.caller(args),
		cwd:         cwd,
		sid:         sid,
		prompt:      strArg(args, "prompt", ""),
		sessionName: sessionName,
		mode:        strArg(args, "mode", ""),
		model:       strArg(args, "model", ""),
	}
	msg, err := d.startDelegation(turn.from, card.ID, turn.prompt, turn.sid)
	if err != nil {
		return AgyDelegateResult{}, err
	}
	turn.taskID = msg.TaskID
	pool := d.getExtraPool(card)
	if !boolArg(args, "async", false) {
		return d.executeExtraACPTurn(ctx, card.ID, pool, turn)
	}
	d.asyncWG.Add(1)
	go func() {
		defer d.asyncWG.Done()
		actx, cancel := d.asyncContext()
		defer cancel()
		_, _ = d.executeExtraACPTurn(actx, card.ID, pool, turn)
	}()
	return AgyDelegateResult{
		Agent:       card.ID,
		Protocol:    agyProtocol,
		SessionID:   turn.sid,
		SessionName: turn.sessionName,
		TaskID:      turn.taskID,
		StopReason:  "queued",
		ToolCalls:   []string{},
		Async:       true,
	}, nil
}

func (d *Dispatcher) executeExtraACPTurn(ctx context.Context, agentID string, pool *acp.SessionPool, t agyTurn) (AgyDelegateResult, error) {
	stopHeartbeat := d.keepDelegationAlive(ctx, t.taskID)
	defer stopHeartbeat()
	res, err := pool.PromptWithOptions(ctx, t.cwd, t.prompt, t.sid, t.mode, t.model)
	if err != nil {
		_, _ = d.Board.UpsertTask(t.from, agentID, t.prompt, "failed", t.taskID, t.sid)
		return AgyDelegateResult{}, err
	}
	if t.sessionName != "" {
		_, _ = d.Board.UpsertSession(t.sessionName, agentID, res.SessionID, t.cwd, t.mode, t.model)
	}
	warning := ""
	if _, err := d.Board.CompleteDelegation(agentID, t.from, res.Response, t.taskID, res.SessionID); err != nil {
		warning = err.Error()
	}
	return AgyDelegateResult{
		Agent:         agentID,
		Protocol:      agyProtocol,
		SessionID:     res.SessionID,
		SessionName:   t.sessionName,
		TaskID:        t.taskID,
		Response:      res.Response,
		ToolCalls:     res.ToolCalls,
		StopReason:    res.StopReason,
		ReusedProcess: res.ReusedProcess,
		BoardWarning:  warning,
	}, nil
}

func (d *Dispatcher) callDelegateCommand(ctx context.Context, card agents.Card, args map[string]any) (CommandDelegateResult, error) {
	from := d.caller(args)
	prompt := strArg(args, "prompt", "")
	cwd := strArg(args, "cwd", d.DefaultCwd)
	msg, err := d.startDelegation(from, card.ID, prompt, "")
	if err != nil {
		return CommandDelegateResult{}, err
	}
	run := func(runCtx context.Context) (CommandDelegateResult, error) {
		stopHeartbeat := d.keepDelegationAlive(runCtx, msg.TaskID)
		defer stopHeartbeat()
		cmdArgs := append([]string{}, card.Executor.Command[1:]...)
		useStdin := card.Executor.PromptMode == "stdin"
		if !useStdin {
			cmdArgs = append(cmdArgs, prompt)
		}
		cmd := exec.CommandContext(runCtx, card.Executor.Command[0], cmdArgs...)
		cmd.Dir = cwd
		if useStdin {
			cmd.Stdin = strings.NewReader(prompt)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			_, _ = d.Board.UpsertTask(from, card.ID, prompt, "failed", msg.TaskID, "")
			return CommandDelegateResult{}, fmt.Errorf("%s: %w (%s)", card.ID, err, strings.TrimSpace(stderr.String()))
		}
		resp := strings.TrimSpace(stdout.String())
		warning := ""
		if _, err := d.Board.CompleteDelegation(card.ID, from, resp, msg.TaskID, ""); err != nil {
			warning = err.Error()
		}
		return CommandDelegateResult{Agent: card.ID, Protocol: "CLI-oneshot", TaskID: msg.TaskID, Response: resp, BoardWarning: warning}, nil
	}
	if !boolArg(args, "async", false) {
		return run(ctx)
	}
	d.asyncWG.Add(1)
	go func() {
		defer d.asyncWG.Done()
		actx, cancel := d.asyncContext()
		defer cancel()
		_, _ = run(actx)
	}()
	return CommandDelegateResult{Agent: card.ID, Protocol: "CLI-oneshot", TaskID: msg.TaskID, Async: true}, nil
}
