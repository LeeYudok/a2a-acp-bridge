package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/LeeYudok/a2a-acp-bridge/internal/acp"
	"github.com/LeeYudok/a2a-acp-bridge/internal/dispatch"
)

func runAskAgyCmd(ctx context.Context, d *dispatch.Dispatcher, cwd string, rest []string) error {
	fs := flag.NewFlagSet("ask-agy", flag.ContinueOnError)
	sid := fs.String("session-id", "", "이어갈 ACP 세션 ID")
	sname := fs.String("s", "", "acpx 스타일의 명명 세션 (예: backend, docs)")
	mode := fs.String("mode", "", "실행 모드 (auto|accept-edits|plan)")
	model := fs.String("model", "", "모델 ID")
	noWait := fs.Bool("no-wait", false, "비동기 큐 등록 후 즉시 반환")
	workDir := fs.String("cwd", cwd, "작업 디렉터리")
	timeout := fs.Duration("timeout", defaultCommandTimeout, "실행 타임아웃 (예: 3m, 10m)")
	from := fs.String("from", "", "보드에 기록할 발신자 (agy|codex|claude|user, 기본: A2A_AGENT_ID 또는 user)")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("ask-agy: 프롬프트 인자가 필요합니다")
	}
	cmdCtx, cancel, err := withCommandTimeout(ctx, *timeout)
	if err != nil {
		return fmt.Errorf("ask-agy: %w", err)
	}
	defer cancel()
	res, err := d.CallTool(cmdCtx, "acp_delegate_to_agy", map[string]any{
		"prompt":       fs.Arg(0),
		"session_id":   *sid,
		"session_name": *sname,
		"mode":         *mode,
		"model":        *model,
		"async":        *noWait,
		"cwd":          resolveWorkDir(*workDir, cwd),
		"from_agent":   *from,
	})
	return printResult(res, err)
}

func runAgySessionsCmd(ctx context.Context, pool *acp.SessionPool, cwd string, args []string) error {
	fs := flag.NewFlagSet("agy-sessions", flag.ContinueOnError)
	cursor := fs.String("cursor", "", "AGY가 반환한 다음 목록 커서")
	workDir := fs.String("cwd", cwd, "세션을 조회할 작업 디렉터리")
	timeout := fs.Duration("timeout", defaultCommandTimeout, "실행 타임아웃 (예: 3m, 10m)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("agy-sessions는 위치 인자를 받지 않습니다")
	}
	cmdCtx, cancel, err := withCommandTimeout(ctx, *timeout)
	if err != nil {
		return fmt.Errorf("agy-sessions: %w", err)
	}
	defer cancel()
	page, err := pool.ListSessions(cmdCtx, resolveWorkDir(*workDir, cwd), *cursor)
	return printResult(page, err)
}

func runAskCodexCmd(ctx context.Context, d *dispatch.Dispatcher, cwd string, rest []string) error {
	fs := flag.NewFlagSet("ask-codex", flag.ContinueOnError)
	tid := fs.String("thread-id", "", "이어갈 Codex 스레드 ID")
	sname := fs.String("s", "", "acpx 스타일의 명명 세션")
	sandbox := fs.String("sandbox", "workspace-write", "샌드박스 모드")
	noWait := fs.Bool("no-wait", false, "비동기 큐 등록 후 즉시 반환")
	workDir := fs.String("cwd", cwd, "작업 디렉터리")
	timeout := fs.Duration("timeout", defaultCommandTimeout, "실행 타임아웃 (예: 3m, 10m)")
	from := fs.String("from", "", "보드에 기록할 발신자 (agy|codex|claude|user, 기본: A2A_AGENT_ID 또는 user)")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("ask-codex: 프롬프트 인자가 필요합니다")
	}
	cmdCtx, cancel, err := withCommandTimeout(ctx, *timeout)
	if err != nil {
		return fmt.Errorf("ask-codex: %w", err)
	}
	defer cancel()
	res, err := d.CallTool(cmdCtx, "a2a_delegate_to_codex", map[string]any{
		"prompt":       fs.Arg(0),
		"thread_id":    *tid,
		"session_name": *sname,
		"sandbox":      *sandbox,
		"async":        *noWait,
		"cwd":          resolveWorkDir(*workDir, cwd),
		"from_agent":   *from,
	})
	return printResult(res, err)
}

var sessionActions = map[string]string{
	"sessions":  "status",
	"status":    "status",
	"cancel":    "cancel",
	"set-mode":  "set_mode",
	"set-model": "set_model",
	"close":     "close",
}

func runSessionCtrlCmd(ctx context.Context, d *dispatch.Dispatcher, cwd, sub string, rest []string) error {
	fs := flag.NewFlagSet(sub, flag.ContinueOnError)
	session := fs.String("s", "", "대상 세션 이름 또는 session_id")
	mode := fs.String("mode", "auto", "모드 (auto|accept-edits|plan)")
	model := fs.String("model", "", "모델 ID")
	workDir := fs.String("cwd", cwd, "작업 디렉터리")
	timeout := fs.Duration("timeout", defaultCommandTimeout, "실행 타임아웃 (예: 3m, 10m)")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	cmdCtx, cancel, err := withCommandTimeout(ctx, *timeout)
	if err != nil {
		return fmt.Errorf("%s: %w", sub, err)
	}
	defer cancel()
	target := *session
	if target == "" && fs.NArg() > 0 {
		target = fs.Arg(0)
	}
	res, err := d.CallTool(cmdCtx, "acp_session_control", map[string]any{
		"action":  sessionActions[sub],
		"session": target,
		"mode":    *mode,
		"model":   *model,
		"cwd":     resolveWorkDir(*workDir, cwd),
	})
	return printResult(res, err)
}

func runSendCmd(ctx context.Context, d *dispatch.Dispatcher, rest []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	from := fs.String("from", "", "발신 에이전트 (agy|codex|claude|user, 기본: A2A_AGENT_ID 또는 user)")
	to := fs.String("to", "codex", "수신 에이전트 (agy|codex|claude)")
	intent := fs.String("intent", "delegate", "메시지 의도 (delegate|review|reply|chat|sync)")
	taskID := fs.String("task-id", "", "태스크 ID")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("send: 메시지 본문이 필요합니다")
	}
	cmdCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	res, err := d.CallTool(cmdCtx, "a2a_send_message", map[string]any{
		"from_agent": *from,
		"to_agent":   *to,
		"intent":     *intent,
		"task_id":    *taskID,
		"content":    fs.Arg(0),
	})
	return printResult(res, err)
}

func runInboxCmd(ctx context.Context, d *dispatch.Dispatcher, rest []string) error {
	fs := flag.NewFlagSet("inbox", flag.ContinueOnError)
	all := fs.Bool("all", false, "읽은 메시지 포함 조회")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("inbox: 에이전트 ID(agy|codex|claude|user)가 필요합니다")
	}
	cmdCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	res, err := d.CallTool(cmdCtx, "a2a_receive_messages", map[string]any{
		"agent_id":    fs.Arg(0),
		"unread_only": !*all,
	})
	return printResult(res, err)
}
