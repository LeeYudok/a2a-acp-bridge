package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
	"github.com/LeeYudok/a2a-acp-bridge/internal/dispatch"
)

func runCoordCmd(ctx context.Context, d *dispatch.Dispatcher, rest []string) error {
	if len(rest) == 0 {
		return runCoordAction(ctx, d, "status", nil)
	}
	action := rest[0]
	if strings.HasPrefix(action, "-") {
		action = "status"
		return runCoordAction(ctx, d, action, rest)
	}
	return runCoordAction(ctx, d, action, rest[1:])
}

func runCoordAction(ctx context.Context, d *dispatch.Dispatcher, action string, rest []string) error {
	fs := flag.NewFlagSet("coord "+action, flag.ContinueOnError)
	agentID := fs.String("agent", "", "실행 주체 에이전트 ID (기본: A2A_AGENT_ID 또는 user)")
	toAgent := fs.String("to", "", "이양 대상 에이전트 ID (handoff 전용, 비우면 개방 이양)")
	expectedTerm := fs.Uint64("expected-term", 0, "CAS 검증용 현재 term 번호")
	ttl := fs.Duration("ttl", board.DefaultLeaseTTL, "리스 유효 시간 (예: 60m, 15m)")
	reason := fs.String("reason", "", "선점·승계·이양 사유")
	agentsList := fs.String("agents", "", "활성 에이전트 목록 (쉼표 구분, 예: agy,codex,claude,hermes)")
	force := fs.Bool("force", false, "만료 전 강제 페일오버 허용")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if *ttl <= 0 && action != "handoff" {
		return fmt.Errorf("coord %s: ttl 은 0보다 커야 합니다: %s", action, *ttl)
	}

	args := map[string]any{
		"action":        action,
		"agent_id":      *agentID,
		"to_agent":      *toAgent,
		"expected_term": *expectedTerm,
		"ttl_seconds":   int(*ttl / time.Second),
		"reason":        *reason,
		"force":         *force,
	}
	if *agentsList != "" {
		args["active_agents"] = *agentsList
	}
	cmdCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	res, err := d.CallTool(cmdCtx, "a2a_coordinator_lease", args)
	return printResult(res, err)
}
