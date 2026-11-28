// Package pi 는 `pi -p --mode json` 을 실행하고 JSONL 이벤트를 해석한다.
package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// DefaultSandbox 는 Pi 위임의 기본 샌드박스 모드다.
const DefaultSandbox = "read-only"

// Sandboxes 는 Pi 위임이 허용하는 샌드박스 목록이다.
var Sandboxes = []string{"read-only", "workspace-write"}

const (
	readOnlyTools = "read,grep,find,ls"
	maxJSONLLine  = 10 * 1024 * 1024
	waitDelay     = 2 * time.Second
)

// Options 는 Pi 실행 한 번의 입력 설정이다.
type Options struct {
	Command   []string
	Cwd       string
	Prompt    string
	SessionID string
	Sandbox   string
	Model     string
}

// Result 는 Pi 실행 한 번의 결과다.
type Result struct {
	SessionID  string
	Response   string
	StopReason string
	Model      string
	CostUSD    float64
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type piMessage struct {
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	StopReason string         `json:"stopReason"`
	Content    []contentBlock `json:"content"`
	Usage      struct {
		Cost struct {
			Total float64 `json:"total"`
		} `json:"cost"`
	} `json:"usage"`
}

type piEvent struct {
	Type     string      `json:"type"`
	ID       string      `json:"id"`
	Message  *piMessage  `json:"message"`
	Messages []piMessage `json:"messages"`
}

// EffectiveSandbox 는 비어 있는 샌드박스 값을 기본값(read-only)으로 채운다.
func EffectiveSandbox(sandbox string) string {
	if sandbox == "" {
		return DefaultSandbox
	}
	return sandbox
}

// Run 은 Pi CLI 를 비대화형 JSON 모드로 실행한다. SessionID 가 있으면 해당 세션을 이어간다.
func Run(ctx context.Context, opts Options) (Result, error) {
	sandbox := EffectiveSandbox(opts.Sandbox)
	if !slices.Contains(Sandboxes, sandbox) {
		return Result{}, fmt.Errorf("sandbox 는 %v 중 하나여야 합니다: %s", Sandboxes, sandbox)
	}
	cmdName, baseArgs := resolveCommand(opts.Command)
	args := buildArgs(baseArgs, opts, sandbox)
	cmd := exec.CommandContext(ctx, cmdName, args...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if err := ctx.Err(); err != nil {
		return Result{}, withStderr("pi -p", err, stderr.String())
	}
	if runErr != nil {
		return Result{}, withStderr("pi -p", runErr, stderr.String())
	}
	res, err := ParseOutput(stdout.Bytes())
	if err != nil {
		return Result{}, withStderr("pi -p", err, stderr.String())
	}
	return res, nil
}

func resolveCommand(command []string) (string, []string) {
	if len(command) > 0 {
		return command[0], command[1:]
	}
	if parts := strings.Fields(os.Getenv("A2A_PI_COMMAND")); len(parts) > 0 {
		return parts[0], parts[1:]
	}
	return "pi", nil
}

func buildArgs(base []string, opts Options, sandbox string) []string {
	args := make([]string, 0, len(base)+12)
	args = append(args, base...)
	args = append(args, "-p", "--mode", "json")
	if opts.SessionID != "" {
		args = append(args, "--session-id", opts.SessionID)
	}
	if sandbox == DefaultSandbox {
		args = append(args, "--tools", readOnlyTools)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	args = append(args, "--", opts.Prompt)
	return args
}

// ParseOutput 은 `pi -p --mode json` 의 stdout JSONL 에서 session_id 와 최종 응답을 추출한다.
func ParseOutput(stdout []byte) (Result, error) {
	var res Result
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 64*1024), maxJSONLLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if !bytes.HasPrefix(line, []byte("{")) {
			continue
		}
		var ev piEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		applyEvent(&res, &ev)
	}
	if err := sc.Err(); err != nil {
		return Result{}, fmt.Errorf("pi JSONL 스캔 실패: %w", err)
	}
	if res.SessionID == "" && res.Response == "" {
		return Result{}, errors.New("pi 응답에 session id 와 assistant 텍스트가 없음")
	}
	return res, nil
}

func applyEvent(res *Result, ev *piEvent) {
	switch ev.Type {
	case "session":
		if ev.ID != "" {
			res.SessionID = ev.ID
		}
	case "message_end", "turn_end":
		if ev.Message != nil && ev.Message.Role == "assistant" {
			applyAssistantMessage(res, ev.Message)
		}
	case "agent_end":
		if res.Response == "" {
			for i := range ev.Messages {
				if ev.Messages[i].Role == "assistant" {
					applyAssistantMessage(res, &ev.Messages[i])
				}
			}
		}
	}
}

func applyAssistantMessage(res *Result, msg *piMessage) {
	if msg.Model != "" {
		res.Model = msg.Model
	}
	if msg.StopReason != "" {
		res.StopReason = msg.StopReason
	}
	if msg.Usage.Cost.Total > 0 {
		res.CostUSD = msg.Usage.Cost.Total
	}
	if text := extractText(msg.Content); text != "" {
		res.Response = text
	}
}

func extractText(blocks []contentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func withStderr(operation string, err error, stderr string) error {
	if diagnostic := strings.TrimSpace(stderr); diagnostic != "" {
		return fmt.Errorf("%s: %w (stderr: %s)", operation, err, diagnostic)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
