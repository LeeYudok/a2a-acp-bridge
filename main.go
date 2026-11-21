package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

const defaultCommandTimeout = 3 * time.Minute

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "오류: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("서브커맨드가 필요합니다 (mcp | serve-a2a | cards | ask-agy | agy-sessions | ask-codex | sessions | cancel | set-mode | set-model | close | send | inbox)")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("현재 디렉터리 확인 실패: %w", err)
	}
	storePath, err := DefaultBoardPath()
	if err != nil {
		return err
	}
	dispatcher := &Dispatcher{
		Board:      NewBoard(storePath),
		Pool:       defaultPool,
		DefaultCwd: cwd,
	}
	defer func() {
		dispatcher.Wait()
		defaultPool.CloseAll()
	}()

	return dispatchSubcommand(context.Background(), dispatcher, cwd, args[0], args[1:])
}

func withCommandTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc, error) {
	if d <= 0 {
		return nil, nil, fmt.Errorf("timeout은 0보다 커야 합니다: %s", d)
	}
	ctx, cancel := context.WithTimeout(parent, d)
	return ctx, cancel, nil
}

func resolveWorkDir(flagVal, fallback string) string {
	if flagVal != "" {
		return flagVal
	}
	return fallback
}

func dispatchSubcommand(ctx context.Context, d *Dispatcher, cwd, sub string, rest []string) error {
	switch sub {
	case "mcp":
		return RunStdioMCPServer(ctx, d, os.Stdin, os.Stdout)
	case "serve-a2a":
		return runServeCmd(d, rest)
	case "cards":
		cmdCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
		defer cancel()
		res, err := d.CallTool(cmdCtx, "a2a_discover_agents", map[string]any{})
		return printResult(res, err)
	case "ask-agy":
		return runAskAgyCmd(ctx, d, cwd, rest)
	case "agy-sessions":
		return runAgySessionsCmd(ctx, d.pool(), cwd, rest)
	case "ask-codex":
		return runAskCodexCmd(ctx, d, cwd, rest)
	case "sessions", "status", "cancel", "set-mode", "set-model", "close":
		return runSessionCtrlCmd(ctx, d, cwd, sub, rest)
	case "send":
		return runSendCmd(ctx, d, rest)
	case "inbox":
		return runInboxCmd(ctx, d, rest)
	default:
		return fmt.Errorf("알 수 없는 서브커맨드: %s", sub)
	}
}

func runServeCmd(d *Dispatcher, rest []string) error {
	fs := flag.NewFlagSet("serve-a2a", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8765", "HTTP 리슨 주소")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	return RunA2AHTTPServer(d, *addr)
}

func runAskAgyCmd(ctx context.Context, d *Dispatcher, cwd string, rest []string) error {
	fs := flag.NewFlagSet("ask-agy", flag.ContinueOnError)
	sid := fs.String("session-id", "", "이어갈 ACP 세션 ID")
	sname := fs.String("s", "", "acpx 스타일의 명명 세션 (예: backend, docs)")
	mode := fs.String("mode", "", "실행 모드 (auto|accept-edits|plan)")
	model := fs.String("model", "", "모델 ID")
	noWait := fs.Bool("no-wait", false, "비동기 큐 등록 후 즉시 반환")
	workDir := fs.String("cwd", cwd, "작업 디렉터리")
	timeout := fs.Duration("timeout", defaultCommandTimeout, "실행 타임아웃 (예: 3m, 10m)")
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
	})
	return printResult(res, err)
}

func runAskCodexCmd(ctx context.Context, d *Dispatcher, cwd string, rest []string) error {
	fs := flag.NewFlagSet("ask-codex", flag.ContinueOnError)
	tid := fs.String("thread-id", "", "이어갈 Codex 스레드 ID")
	sname := fs.String("s", "", "acpx 스타일의 명명 세션")
	sandbox := fs.String("sandbox", "workspace-write", "샌드박스 모드")
	noWait := fs.Bool("no-wait", false, "비동기 큐 등록 후 즉시 반환")
	workDir := fs.String("cwd", cwd, "작업 디렉터리")
	timeout := fs.Duration("timeout", defaultCommandTimeout, "실행 타임아웃 (예: 3m, 10m)")
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
	})
	return printResult(res, err)
}

func runSessionCtrlCmd(ctx context.Context, d *Dispatcher, cwd, sub string, rest []string) error {
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
	actionMap := map[string]string{
		"sessions":  "status",
		"status":    "status",
		"cancel":    "cancel",
		"set-mode":  "set_mode",
		"set-model": "set_model",
		"close":     "close",
	}
	res, err := d.CallTool(cmdCtx, "acp_session_control", map[string]any{
		"action":  actionMap[sub],
		"session": target,
		"mode":    *mode,
		"model":   *model,
		"cwd":     resolveWorkDir(*workDir, cwd),
	})
	return printResult(res, err)
}

func runSendCmd(ctx context.Context, d *Dispatcher, rest []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	from := fs.String("from", "user", "발신 에이전트 (agy|codex|user)")
	to := fs.String("to", "codex", "수신 에이전트 (agy|codex)")
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

func runInboxCmd(ctx context.Context, d *Dispatcher, rest []string) error {
	fs := flag.NewFlagSet("inbox", flag.ContinueOnError)
	all := fs.Bool("all", false, "읽은 메시지 포함 조회")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("inbox: 에이전트 ID(agy|codex)가 필요합니다")
	}
	cmdCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	res, err := d.CallTool(cmdCtx, "a2a_receive_messages", map[string]any{
		"agent_id":    fs.Arg(0),
		"unread_only": !*all,
	})
	return printResult(res, err)
}

func printResult(v any, err error) error {
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("JSON 출력 실패: %w", err)
	}
	fmt.Println(string(b))
	return nil
}
