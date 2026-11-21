package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
)

type agySessionInfo struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type agySessionPage struct {
	Sessions   []agySessionInfo `json:"sessions"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

func runAgySessionsCmd(ctx context.Context, pool *SessionPool, cwd string, args []string) error {
	flags := flag.NewFlagSet("agy-sessions", flag.ContinueOnError)
	cursor := flags.String("cursor", "", "AGY가 반환한 다음 목록 커서")
	workDir := flags.String("cwd", cwd, "세션을 조회할 작업 디렉터리")
	timeout := flags.Duration("timeout", defaultCommandTimeout, "실행 타임아웃 (예: 3m, 10m)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("agy-sessions는 위치 인자를 받지 않습니다")
	}
	cmdCtx, cancel, err := withCommandTimeout(ctx, *timeout)
	if err != nil {
		return fmt.Errorf("agy-sessions: %w", err)
	}
	defer cancel()
	page, err := pool.listAgySessions(cmdCtx, resolveWorkDir(*workDir, cwd), *cursor)
	return printResult(page, err)
}

func (p *SessionPool) listAgySessions(ctx context.Context, cwd, cursor string) (agySessionPage, error) {
	w, err := startACPWorker(ctx, p.resolveCommand(), cwd)
	if err != nil {
		return agySessionPage{}, err
	}
	defer w.killProcess()

	params := map[string]string{"cwd": cwd}
	if cursor != "" {
		params["cursor"] = cursor
	}
	reqID := int(w.nextReqID.Add(1))
	raw, _, _, err := w.sendRPC(reqID, "session/list", params, true)
	if err != nil {
		return agySessionPage{}, fmt.Errorf("ACP session/list 실패: %w", err)
	}

	page := agySessionPage{Sessions: []agySessionInfo{}}
	if err := json.Unmarshal(raw, &page); err != nil {
		return agySessionPage{}, fmt.Errorf("ACP 세션 목록 파싱 실패: %w", err)
	}
	return page, nil
}
