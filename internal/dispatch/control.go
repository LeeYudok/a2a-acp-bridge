package dispatch

import (
	"fmt"

	"github.com/LeeYudok/a2a-acp-bridge/internal/acp"
	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

// SessionControlResult 는 acp_session_control 응답이다
type SessionControlResult struct {
	Action   string               `json:"action"`
	Session  string               `json:"session,omitempty"`
	Mode     string               `json:"mode,omitempty"`
	Model    string               `json:"model,omitempty"`
	Status   string               `json:"status"`
	Workers  []acp.WorkerStatus   `json:"workers,omitempty"`
	Sessions []board.NamedSession `json:"sessions,omitempty"`
}

func (d *Dispatcher) callSessionControl(args map[string]any) (SessionControlResult, error) {
	pool, err := d.pool()
	if err != nil {
		return SessionControlResult{}, err
	}
	action := strArg(args, "action", "status")
	target := strArg(args, "session", "")
	cwd := strArg(args, "cwd", d.DefaultCwd)
	resolvedID, matchedName, resolvedCwd := d.resolveControlTarget(target, cwd)
	switch action {
	case "status":
		sessions, _ := d.Board.ListSessions(false)
		return SessionControlResult{Action: "status", Status: "ok", Workers: pool.Snapshots(), Sessions: sessions}, nil
	case "cancel":
		return ctrlCancel(pool, resolvedID)
	case "set_mode":
		return d.ctrlSetMode(pool, resolvedCwd, resolvedID, matchedName, strArg(args, "mode", "auto"))
	case "set_model":
		return d.ctrlSetModel(pool, resolvedCwd, resolvedID, matchedName, strArg(args, "model", ""))
	case "close":
		return d.ctrlClose(pool, target, resolvedID)
	default:
		return SessionControlResult{}, fmt.Errorf("알 수 없는 세션 제어 동작: %s", action)
	}
}

// resolveControlTarget 은 세션 이름이나 ID 를 보드의 명명 세션과 맞춰 (세션 ID, 이름, cwd) 를 돌려준다
func (d *Dispatcher) resolveControlTarget(target, defaultCwd string) (string, string, string) {
	if target == "" {
		return "", "", defaultCwd
	}
	if rec, err := d.Board.ResolveSession(target, ""); err == nil {
		cwd := defaultCwd
		if rec.Cwd != "" {
			cwd = rec.Cwd
		}
		return rec.SessionID, rec.Name, cwd
	}
	return target, "", defaultCwd
}

func ctrlCancel(pool *acp.SessionPool, resolvedID string) (SessionControlResult, error) {
	if resolvedID == "" {
		return SessionControlResult{}, fmt.Errorf("cancel: session 인자가 필요합니다")
	}
	if err := pool.Cancel(resolvedID); err != nil {
		return SessionControlResult{}, err
	}
	return SessionControlResult{Action: "cancel", Session: resolvedID, Status: "cancelled"}, nil
}

func (d *Dispatcher) ctrlSetMode(pool *acp.SessionPool, cwd, resolvedID, matchedName, mode string) (SessionControlResult, error) {
	if err := pool.SetMode(cwd, resolvedID, mode); err != nil {
		return SessionControlResult{}, err
	}
	if matchedName != "" {
		_, _ = d.Board.UpsertSession(matchedName, "agy", resolvedID, cwd, mode, "")
	}
	return SessionControlResult{Action: "set_mode", Session: resolvedID, Mode: mode, Status: "updated"}, nil
}

func (d *Dispatcher) ctrlSetModel(pool *acp.SessionPool, cwd, resolvedID, matchedName, model string) (SessionControlResult, error) {
	if model == "" {
		return SessionControlResult{}, fmt.Errorf("set_model: model 인자가 필요합니다")
	}
	if err := pool.SetModel(cwd, resolvedID, model); err != nil {
		return SessionControlResult{}, err
	}
	if matchedName != "" {
		_, _ = d.Board.UpsertSession(matchedName, "agy", resolvedID, cwd, "", model)
	}
	return SessionControlResult{Action: "set_model", Session: resolvedID, Model: model, Status: "updated"}, nil
}

func (d *Dispatcher) ctrlClose(pool *acp.SessionPool, target, resolvedID string) (SessionControlResult, error) {
	if resolvedID != "" {
		_ = pool.CloseSession(resolvedID)
	}
	if target != "" {
		_, _ = d.Board.CloseNamedSession(target)
	}
	return SessionControlResult{Action: "close", Session: resolvedID, Status: "closed"}, nil
}
