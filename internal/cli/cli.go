// Package cli 는 a2a-acp-bridge 서브커맨드와 플래그를 해석해 Dispatcher 를 호출한다.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/LeeYudok/a2a-acp-bridge/internal/a2a"
	"github.com/LeeYudok/a2a-acp-bridge/internal/acp"
	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
	"github.com/LeeYudok/a2a-acp-bridge/internal/dispatch"
	"github.com/LeeYudok/a2a-acp-bridge/internal/mcp"
)

const defaultCommandTimeout = 3 * time.Minute

const usage = "서브커맨드가 필요합니다 (mcp | serve-a2a | cards | ask-agy | agy-sessions | ask-codex | ask-claude | sessions | cancel | set-mode | set-model | close | send | inbox | coord)"

// Run 은 os.Args[1:] 를 받아 서브커맨드를 실행한다
func Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("현재 디렉터리 확인 실패: %w", err)
	}
	storePath, err := board.DefaultPath()
	if err != nil {
		return err
	}
	d := &dispatch.Dispatcher{
		Board:      board.New(storePath),
		Pool:       acp.NewSessionPool(nil),
		DefaultCwd: cwd,
		Caller:     os.Getenv("A2A_AGENT_ID"),
	}
	defer d.Close()

	return dispatchSubcommand(context.Background(), d, cwd, args[0], args[1:])
}

func dispatchSubcommand(ctx context.Context, d *dispatch.Dispatcher, cwd, sub string, rest []string) error {
	switch sub {
	case "mcp":
		return mcp.Serve(ctx, d, os.Stdin, os.Stdout)
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
		return runAgySessionsCmd(ctx, d.Pool, cwd, rest)
	case "ask-codex":
		return runAskCodexCmd(ctx, d, cwd, rest)
	case "ask-claude":
		return runAskClaudeCmd(ctx, d, cwd, rest)
	case "sessions", "status", "cancel", "set-mode", "set-model", "close":
		return runSessionCtrlCmd(ctx, d, cwd, sub, rest)
	case "send":
		return runSendCmd(ctx, d, rest)
	case "inbox":
		return runInboxCmd(ctx, d, rest)
	case "coord":
		return runCoordCmd(ctx, d, rest)
	default:
		return fmt.Errorf("알 수 없는 서브커맨드: %s", sub)
	}
}

func runServeCmd(d *dispatch.Dispatcher, rest []string) error {
	fs := flag.NewFlagSet("serve-a2a", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8765", "HTTP 리슨 주소")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	return a2a.ListenAndServe(d, *addr)
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

// explicitFlag 는 플래그를 명령줄에서 직접 줬을 때만 값을 돌려준다. 안 줬으면 빈 문자열이다.
// 위임 cwd 를 비워 두면 dispatch 가 명명 세션에 저장된 디렉터리, 그다음 현재 디렉터리를 쓴다.
func explicitFlag(fs *flag.FlagSet, name, val string) string {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	if set {
		return val
	}
	return ""
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
