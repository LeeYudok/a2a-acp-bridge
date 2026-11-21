package acp

import (
	"context"
	"encoding/json"
	"fmt"
)

// SessionInfo 는 ACP session/list 응답의 세션 한 건이다
type SessionInfo struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// SessionPage 는 ACP session/list 응답 한 페이지다
type SessionPage struct {
	Sessions   []SessionInfo `json:"sessions"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// ListSessions 는 에이전트의 기존 세션 목록을 조회한다. 세션을 열지 않고, 조회용 워커는 풀에 남기지 않는다.
func (p *SessionPool) ListSessions(ctx context.Context, cwd, cursor string) (SessionPage, error) {
	w, err := startWorker(ctx, p.resolveCommand(), cwd)
	if err != nil {
		return SessionPage{}, err
	}
	defer w.killProcess()

	params := map[string]string{"cwd": cwd}
	if cursor != "" {
		params["cursor"] = cursor
	}
	reqID := int(w.nextReqID.Add(1))
	raw, _, _, err := w.sendRPC(reqID, "session/list", params, true)
	if err != nil {
		return SessionPage{}, fmt.Errorf("ACP session/list 실패: %w", err)
	}

	page := SessionPage{Sessions: []SessionInfo{}}
	if err := json.Unmarshal(raw, &page); err != nil {
		return SessionPage{}, fmt.Errorf("ACP 세션 목록 파싱 실패: %w", err)
	}
	return page, nil
}
