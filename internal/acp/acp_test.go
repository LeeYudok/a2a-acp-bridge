package acp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeeYudok/a2a-acp-bridge/internal/acp"
	"github.com/LeeYudok/a2a-acp-bridge/internal/acp/acptest"
)

func TestMain(m *testing.M) { acptest.Main(m) }

func TestCollectUpdate(t *testing.T) {
	var chunks, tools []string
	acp.CollectUpdate(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"안녕하세요 ACP"}}}}`, &chunks, &tools)
	acp.CollectUpdate(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","name":"read_file"}}}`, &chunks, &tools)
	if got, want := strings.Join(chunks, ""), "안녕하세요 ACP"; got != want {
		t.Errorf("청크 = %q, 원하는 값 %q", got, want)
	}
	if got, want := strings.Join(tools, ""), "read_file"; got != want {
		t.Errorf("도구 = %q, 원하는 값 %q", got, want)
	}
}

func TestListSessionsWithoutOpeningSession(t *testing.T) {
	pool := acp.NewSessionPool(acptest.Command(t))
	acptest.ListOnly(t)
	cwd := t.TempDir()
	page, err := pool.ListSessions(context.Background(), cwd, "previous-page")
	if err != nil {
		t.Fatal(err)
	}
	want := acp.SessionInfo{SessionID: "existing", Cwd: cwd, Title: "기존 대화"}
	if len(page.Sessions) != 1 || page.Sessions[0] != want || page.NextCursor != "next-page" {
		t.Fatalf("세션 목록 = %+v, 원하는 세션 = %+v와 다음 커서", page, want)
	}
	if len(pool.Snapshots()) != 0 {
		t.Fatal("목록 조회가 세션 풀에 워커를 남겼습니다")
	}
}

func TestPromptReusesWorkerAndSuppressesReplay(t *testing.T) {
	pool := acp.NewSessionPool(acptest.Command(t))
	t.Cleanup(pool.CloseAll)
	ctx := context.Background()
	cwd := t.TempDir()

	r1, err := pool.Prompt(ctx, cwd, "첫 번째 턴", "")
	if err != nil {
		t.Fatalf("첫 턴 실패: %v", err)
	}
	if r1.ReusedProcess || r1.Response != "모의 ACP 응답" || r1.StopReason != "end_turn" {
		t.Fatalf("첫 턴 결과 = %+v", r1)
	}
	r2, err := pool.Prompt(ctx, cwd, "두 번째 턴", r1.SessionID)
	if err != nil {
		t.Fatalf("두 번째 턴 실패: %v", err)
	}
	if !r2.ReusedProcess {
		t.Fatal("같은 세션 두 번째 턴이 워커를 재사용하지 않았습니다")
	}
	r3, err := pool.Prompt(ctx, cwd, "로드된 세션", "sess-loaded")
	if err != nil {
		t.Fatalf("session/load 턴 실패: %v", err)
	}
	if strings.Contains(r3.Response, "REPLAY_OLD_NOISE") {
		t.Fatalf("session/load 리플레이 노이즈가 응답에 섞임: %q", r3.Response)
	}
}
