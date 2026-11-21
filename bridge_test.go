package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelperMockACPServer(t *testing.T) {
	if os.Getenv("GO_WANT_MOCK_ACP") != "1" {
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var env jsonRPCEnvelope
		if json.Unmarshal([]byte(sc.Text()), &env) != nil || env.Method == "" {
			continue
		}
		handleMockACPLine(sc, env)
	}
	os.Exit(0)
}

func handleMockACPLine(sc *bufio.Scanner, env jsonRPCEnvelope) {
	if logPath := os.Getenv("GO_WANT_MOCK_ACP_LOG"); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			_, _ = fmt.Fprintln(f, env.Method)
			_ = f.Close()
		}
	}
	switch env.Method {
	case "initialize":
		fmt.Printf(`{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"sessionCapabilities":{"close":{}}}}}`+"\n", *env.ID)
	case "session/new":
		if os.Getenv("GO_WANT_MOCK_ACP_LIST_ONLY") == "1" {
			fmt.Printf(`{"jsonrpc":"2.0","id":%d,"error":{"code":-32600,"message":"unexpected session creation"}}`+"\n", *env.ID)
			return
		}
		fmt.Printf(`{"jsonrpc":"2.0","id":%d,"result":{"sessionId":"sess-mock-1"}}`+"\n", *env.ID)
	case "session/list":
		var params struct {
			Cwd    string `json:"cwd"`
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(env.Params, &params)
		page := agySessionPage{Sessions: []agySessionInfo{}, NextCursor: "next-page"}
		if params.Cursor == "previous-page" {
			page.Sessions = append(page.Sessions, agySessionInfo{SessionID: "existing", Cwd: params.Cwd, Title: "기존 대화"})
		}
		body, _ := json.Marshal(page)
		fmt.Printf(`{"jsonrpc":"2.0","id":%d,"result":%s}`+"\n", *env.ID, body)
	case "session/load":
		fmt.Println(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"REPLAY_OLD_NOISE"}}}}`)
		fmt.Printf(`{"jsonrpc":"2.0","id":%d,"result":{"sessionId":"sess-loaded"}}`+"\n", *env.ID)
	case "session/set_mode", "session/set_config_option", "session/set_model", "session/close":
		fmt.Printf(`{"jsonrpc":"2.0","id":%d,"result":{}}`+"\n", *env.ID)
	case "session/prompt":
		fmt.Println(`{"jsonrpc":"2.0","id":900,"method":"session/request_permission","params":{"options":[{"optionId":"allow-once","kind":"allow_once"}]}}`)
		if sc.Scan() {
			var permReply jsonRPCEnvelope
			_ = json.Unmarshal([]byte(sc.Text()), &permReply)
		}
		fmt.Println(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","name":"edit_file"}}}`)
		fmt.Println(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"모의 ACP 응답"}}}}`)
		fmt.Printf(`{"jsonrpc":"2.0","id":%d,"result":{"stopReason":"end_turn"}}`+"\n", *env.ID)
	}
}

func TestBoardAndMessageLifecycle(t *testing.T) {
	store := filepath.Join(t.TempDir(), "board.json")
	d := &Dispatcher{Board: NewBoard(store), DefaultCwd: t.TempDir()}
	ctx := context.Background()

	rawCards, err := d.CallTool(ctx, "a2a_discover_agents", map[string]any{})
	if err != nil {
		t.Fatalf("a2a_discover_agents 실패: %v", err)
	}
	cards := rawCards.(DiscoverResponse)
	if got, want := len(cards.Agents), 2; got != want {
		t.Fatalf("에이전트 수 = %d, 원하는 값 %d", got, want)
	}

	rawSent, err := d.CallTool(ctx, "a2a_send_message", map[string]any{
		"from_agent": "agy",
		"to_agent":   "codex",
		"intent":     "review",
		"content":    "Go 단일 바이너리 전환 리뷰 요청",
	})
	if err != nil {
		t.Fatalf("a2a_send_message 실패: %v", err)
	}
	sent := rawSent.(Message)

	rawInbox, err := d.CallTool(ctx, "a2a_receive_messages", map[string]any{
		"agent_id":    "codex",
		"unread_only": true,
	})
	if err != nil {
		t.Fatalf("a2a_receive_messages 실패: %v", err)
	}
	if got, want := rawInbox.(ReceiveResponse).Count, 1; got != want {
		t.Fatalf("미열람 메시지 수 = %d, 원하는 값 %d", got, want)
	}

	rawTask, err := d.CallTool(ctx, "a2a_task_status", map[string]any{
		"task_id": sent.TaskID,
		"state":   "completed",
	})
	if err != nil {
		t.Fatalf("a2a_task_status 갱신 실패: %v", err)
	}
	if got, want := rawTask.(Task).State, "completed"; got != want {
		t.Fatalf("태스크 상태 = %s, 원하는 값 %s", got, want)
	}

	_, err = d.CallTool(ctx, "a2a_task_status", map[string]any{"task_id": "task-missing"})
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("없는 태스크 에러 = %v, 원하는 값 %v", err, ErrTaskNotFound)
	}
}

func TestListAgySessionsWithoutOpeningSession(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_ACP", "1")
	t.Setenv("GO_WANT_MOCK_ACP_LIST_ONLY", "1")
	pool := NewSessionPool([]string{os.Args[0], "-test.run=TestHelperMockACPServer"})
	cwd := t.TempDir()
	page, err := pool.listAgySessions(context.Background(), cwd, "previous-page")
	if err != nil {
		t.Fatal(err)
	}
	want := agySessionInfo{SessionID: "existing", Cwd: cwd, Title: "기존 대화"}
	if len(page.Sessions) != 1 || page.Sessions[0] != want || page.NextCursor != "next-page" {
		t.Fatalf("세션 목록 = %+v, 원하는 세션 = %+v와 다음 커서", page, want)
	}
	if len(pool.Snapshots()) != 0 {
		t.Fatal("목록 조회가 세션 풀에 워커를 남겼습니다")
	}
}

func TestACPSessionPoolAndRuntimeControl(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_ACP", "1")
	pool := NewSessionPool([]string{os.Args[0], "-test.run=TestHelperMockACPServer"})
	t.Cleanup(pool.CloseAll)

	store := filepath.Join(t.TempDir(), "board.json")
	d := &Dispatcher{Board: NewBoard(store), Pool: pool, DefaultCwd: t.TempDir()}
	ctx := context.Background()

	r1, err := d.CallTool(ctx, "acp_delegate_to_agy", map[string]any{
		"prompt":       "첫 번째 턴",
		"session_name": "backend",
		"mode":         "auto",
	})
	if err != nil {
		t.Fatalf("첫 번째 위임 실패: %v", err)
	}
	res1 := r1.(AgyDelegateResult)
	if res1.ReusedProcess {
		t.Fatalf("첫 턴 ReusedProcess = true, 원하는 값 false")
	}
	if got, want := res1.Response, "모의 ACP 응답"; got != want {
		t.Fatalf("응답 = %q, 원하는 값 %q", got, want)
	}

	r2, err := d.CallTool(ctx, "acp_delegate_to_agy", map[string]any{
		"prompt":       "두 번째 턴 (풀 재사용)",
		"session_name": "backend",
	})
	if err != nil {
		t.Fatalf("두 번째 위임 실패: %v", err)
	}
	res2 := r2.(AgyDelegateResult)
	if !res2.ReusedProcess {
		t.Fatalf("두 번째 턴 ReusedProcess = false, 원하는 값 true")
	}

	rLoad, err := d.CallTool(ctx, "acp_delegate_to_agy", map[string]any{
		"prompt":     "로드된 세션 프롬프트",
		"session_id": "sess-loaded",
	})
	if err != nil {
		t.Fatalf("session/load 위임 실패: %v", err)
	}
	if strings.Contains(rLoad.(AgyDelegateResult).Response, "REPLAY_OLD_NOISE") {
		t.Fatalf("session/load 리플레이 노이즈가 응답에 섞임: %q", rLoad.(AgyDelegateResult).Response)
	}

	rAsync, err := d.CallTool(ctx, "acp_delegate_to_agy", map[string]any{
		"prompt":       "비동기 큐 프롬프트",
		"session_name": "backend",
		"async":        true,
	})
	if err != nil {
		t.Fatalf("비동기 위임 실패: %v", err)
	}
	if !rAsync.(AgyDelegateResult).Async {
		t.Fatalf("Async = false, 원하는 값 true")
	}
	d.Wait()

	ctrlStatus, err := d.CallTool(ctx, "acp_session_control", map[string]any{"action": "status"})
	if err != nil {
		t.Fatalf("status 실패: %v", err)
	}
	if len(ctrlStatus.(SessionControlResult).Workers) == 0 {
		t.Fatalf("활성 워커 수가 0임")
	}

	if _, err := d.CallTool(ctx, "acp_session_control", map[string]any{
		"action":  "set_model",
		"session": "backend",
		"model":   "gemini-4-argon",
	}); err != nil {
		t.Fatalf("set_model 실패: %v", err)
	}

	if _, err := d.CallTool(ctx, "acp_session_control", map[string]any{
		"action":  "close",
		"session": "backend",
	}); err != nil {
		t.Fatalf("close 실패: %v", err)
	}
}

func TestStreamParsersAndServers(t *testing.T) {
	var chunks, tools []string
	CollectACPUpdate(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"안녕하세요 ACP"}}}}`, &chunks, &tools)
	CollectACPUpdate(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","name":"read_file"}}}`, &chunks, &tools)
	if got, want := strings.Join(chunks, ""), "안녕하세요 ACP"; got != want {
		t.Errorf("청크 = %q, 원하는 값 %q", got, want)
	}
	if got, want := strings.Join(tools, ""), "read_file"; got != want {
		t.Errorf("도구 = %q, 원하는 값 %q", got, want)
	}

	store := filepath.Join(t.TempDir(), "board.json")
	d := &Dispatcher{Board: NewBoard(store), Pool: NewSessionPool(nil), DefaultCwd: t.TempDir()}

	in := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n") + "\n")
	var out bytes.Buffer
	if err := RunStdioMCPServer(context.Background(), d, in, &out); err != nil {
		t.Fatalf("RunStdioMCPServer 실패: %v", err)
	}
	if got, want := len(strings.Split(strings.TrimSpace(out.String()), "\n")), 2; got != want {
		t.Fatalf("MCP 출력 줄 수 = %d, 원하는 값 %d", got, want)
	}

	srv := httptest.NewServer(NewA2AMux(d))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/agent.json")
	if err != nil {
		t.Fatalf("GET /.well-known/agent.json 실패: %v", err)
	}
	defer resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("GET 상태코드 = %d, 원하는 값 %d", got, want)
	}

	postBody := bytes.NewBufferString(`{"jsonrpc":"2.0","id":10,"method":"acp/control","params":{"action":"status"}}`)
	postResp, err := http.Post(srv.URL+"/a2a", "application/json", postBody)
	if err != nil {
		t.Fatalf("POST /a2a 실패: %v", err)
	}
	defer postResp.Body.Close()
	if got, want := postResp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("POST 상태코드 = %d, 원하는 값 %d", got, want)
	}
}
