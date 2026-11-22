package dispatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/LeeYudok/a2a-acp-bridge/internal/acp"
	"github.com/LeeYudok/a2a-acp-bridge/internal/acp/acptest"
	"github.com/LeeYudok/a2a-acp-bridge/internal/agents"
	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

func TestMain(m *testing.M) { acptest.Main(m) }

func newTestDispatcher(t *testing.T, pool *acp.SessionPool) *Dispatcher {
	t.Helper()
	return &Dispatcher{Board: board.New(filepath.Join(t.TempDir(), "board.json")), Pool: pool, DefaultCwd: t.TempDir()}
}

func newMockPool(t *testing.T) *acp.SessionPool {
	t.Helper()
	pool := acp.NewSessionPool(acptest.Command(t))
	t.Cleanup(pool.CloseAll)
	return pool
}

func TestBoardAndMessageLifecycle(t *testing.T) {
	t.Setenv("A2A_AGENTS_FILE", filepath.Join(t.TempDir(), "agents.json"))
	t.Setenv("A2A_POOL_FILE", filepath.Join(t.TempDir(), "pool.json"))
	d := newTestDispatcher(t, nil)
	ctx := context.Background()

	rawCards, err := d.CallTool(ctx, "a2a_discover_agents", map[string]any{})
	if err != nil {
		t.Fatalf("a2a_discover_agents 실패: %v", err)
	}
	if got, want := len(rawCards.(agents.Discovery).Agents), 3; got != want {
		t.Fatalf("에이전트 수 = %d, 원하는 값 %d", got, want)
	}

	rawSent, err := d.CallTool(ctx, "a2a_send_message", map[string]any{
		"from_agent": "agy", "to_agent": "codex", "intent": "review", "content": "Go 단일 바이너리 전환 리뷰 요청",
	})
	if err != nil {
		t.Fatalf("a2a_send_message 실패: %v", err)
	}
	sent := rawSent.(board.Message)

	rawInbox, err := d.CallTool(ctx, "a2a_receive_messages", map[string]any{"agent_id": "codex", "unread_only": true})
	if err != nil {
		t.Fatalf("a2a_receive_messages 실패: %v", err)
	}
	if got, want := rawInbox.(ReceiveResponse).Count, 1; got != want {
		t.Fatalf("미열람 메시지 수 = %d, 원하는 값 %d", got, want)
	}

	rawTask, err := d.CallTool(ctx, "a2a_task_status", map[string]any{"task_id": sent.TaskID, "state": "completed"})
	if err != nil {
		t.Fatalf("a2a_task_status 갱신 실패: %v", err)
	}
	if got, want := rawTask.(board.Task).State, "completed"; got != want {
		t.Fatalf("태스크 상태 = %s, 원하는 값 %s", got, want)
	}

	_, err = d.CallTool(ctx, "a2a_task_status", map[string]any{"task_id": "task-missing"})
	if !errors.Is(err, board.ErrTaskNotFound) {
		t.Fatalf("없는 태스크 에러 = %v, 원하는 값 %v", err, board.ErrTaskNotFound)
	}
	if _, err := d.CallTool(ctx, "acp_delegate_to_agy", map[string]any{"prompt": "x"}); !errors.Is(err, ErrNoPool) {
		t.Fatalf("풀 없는 AGY 위임 에러 = %v, 원하는 값 %v", err, ErrNoPool)
	}
}

func TestACPSessionPoolAndRuntimeControl(t *testing.T) {
	d := newTestDispatcher(t, newMockPool(t))
	ctx := context.Background()

	r1, err := d.CallTool(ctx, "acp_delegate_to_agy", map[string]any{"prompt": "첫 번째 턴", "session_name": "backend", "mode": "auto"})
	if err != nil {
		t.Fatalf("첫 번째 위임 실패: %v", err)
	}
	res1 := r1.(AgyDelegateResult)
	if res1.ReusedProcess || res1.Response != "모의 ACP 응답" {
		t.Fatalf("첫 턴 결과 = %+v", res1)
	}

	r2, err := d.CallTool(ctx, "acp_delegate_to_agy", map[string]any{"prompt": "두 번째 턴 (풀 재사용)", "session_name": "backend"})
	if err != nil {
		t.Fatalf("두 번째 위임 실패: %v", err)
	}
	if !r2.(AgyDelegateResult).ReusedProcess {
		t.Fatal("두 번째 턴 ReusedProcess = false, 원하는 값 true")
	}

	rLoad, err := d.CallTool(ctx, "acp_delegate_to_agy", map[string]any{"prompt": "로드된 세션 프롬프트", "session_id": "sess-loaded"})
	if err != nil {
		t.Fatalf("session/load 위임 실패: %v", err)
	}
	if strings.Contains(rLoad.(AgyDelegateResult).Response, "REPLAY_OLD_NOISE") {
		t.Fatalf("session/load 리플레이 노이즈가 응답에 섞임: %q", rLoad.(AgyDelegateResult).Response)
	}

	rAsync, err := d.CallTool(ctx, "acp_delegate_to_agy", map[string]any{"prompt": "비동기 큐 프롬프트", "session_name": "backend", "async": true})
	if err != nil {
		t.Fatalf("비동기 위임 실패: %v", err)
	}
	if !rAsync.(AgyDelegateResult).Async {
		t.Fatal("Async = false, 원하는 값 true")
	}
	d.Wait()

	ctrlStatus, err := d.CallTool(ctx, "acp_session_control", map[string]any{"action": "status"})
	if err != nil {
		t.Fatalf("status 실패: %v", err)
	}
	if len(ctrlStatus.(SessionControlResult).Workers) == 0 {
		t.Fatal("활성 워커 수가 0임")
	}
	if _, err := d.CallTool(ctx, "acp_session_control", map[string]any{"action": "set_model", "session": "backend", "model": "gemini-4-argon"}); err != nil {
		t.Fatalf("set_model 실패: %v", err)
	}
	if _, err := d.CallTool(ctx, "acp_session_control", map[string]any{"action": "close", "session": "backend"}); err != nil {
		t.Fatalf("close 실패: %v", err)
	}
}

func TestACPDelegateAppliesModeAndModelBeforePrompt(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "acp_methods.log")
	acptest.LogMethods(t, logPath)
	d := newTestDispatcher(t, newMockPool(t))
	ctx := context.Background()

	delegate := func(args map[string]any) {
		t.Helper()
		if _, err := d.CallTool(ctx, "acp_delegate_to_agy", args); err != nil {
			t.Fatalf("위임 실패(%v): %v", args["prompt"], err)
		}
	}

	delegate(map[string]any{"prompt": "t1", "session_name": "sync-check", "mode": "plan", "model": "gemini-4-neon"})
	methods1 := readMethodLog(t, logPath)
	modeIdx := slices.Index(methods1, "session/set_mode")
	modelIdx := slices.Index(methods1, "session/set_config_option")
	promptIdx := slices.Index(methods1, "session/prompt")
	if modeIdx < 0 || modelIdx < 0 || promptIdx < 0 || modeIdx > promptIdx || modelIdx > promptIdx {
		t.Fatalf("1턴 호출 순서 잘못됨: %v", methods1)
	}

	delegate(map[string]any{"prompt": "t2", "session_name": "sync-check", "mode": "plan", "model": "gemini-4-neon"})
	methods2 := readMethodLog(t, logPath)
	if diff := methods2[len(methods1):]; !slices.Equal(diff, []string{"session/prompt"}) {
		t.Fatalf("2턴 동일 모드·모델 재호출 발생: %v", diff)
	}

	delegate(map[string]any{"prompt": "t3", "session_name": "sync-check", "mode": "auto"})
	if methods3 := readMethodLog(t, logPath)[len(methods2):]; !slices.Equal(methods3, []string{"session/set_mode", "session/prompt"}) {
		t.Fatalf("3턴 모드 변경 순서 잘못됨: %v", methods3)
	}
}

func toolProps(t *testing.T, name string) (map[string]any, []string) {
	t.Helper()
	for _, tool := range Tools() {
		if tool.Name == name {
			props, _ := tool.InputSchema["properties"].(map[string]any)
			required, _ := tool.InputSchema["required"].([]string)
			return props, required
		}
	}
	t.Fatalf("%s 도구가 없습니다", name)
	return nil, nil
}

// 발신자를 생략하면 user 로 기록되고 응답도 user 수신함에 쌓이므로, MCP 스키마로도 그 수신함을 읽고
// from_agent 없이 보낼 수 있어야 한다(PR #10 Codex 리뷰 P2·P3).
func TestToolsAllowDefaultSender(t *testing.T) {
	props, _ := toolProps(t, "a2a_receive_messages")
	agentID, _ := props["agent_id"].(map[string]any)
	if enums, _ := agentID["enum"].([]string); !slices.Contains(enums, "user") || !slices.Contains(enums, "claude") {
		t.Fatalf("a2a_receive_messages agent_id enum = %v, user·claude 가 있어야 함", enums)
	}
	_, required := toolProps(t, "a2a_send_message")
	if slices.Contains(required, "from_agent") {
		t.Fatalf("a2a_send_message required = %v, from_agent 는 선택이어야 함", required)
	}
}

func TestToolsIncludeSyncIntent(t *testing.T) {
	for _, tool := range Tools() {
		if tool.Name != "a2a_send_message" {
			continue
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		intent, _ := props["intent"].(map[string]any)
		enums, _ := intent["enum"].([]string)
		if !slices.Contains(enums, "sync") {
			t.Fatal("a2a_send_message intent enum에 sync가 없습니다")
		}
		return
	}
	t.Fatal("a2a_send_message 도구가 없습니다")
}

func readMethodLog(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ACP 로그 읽기 실패: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}
