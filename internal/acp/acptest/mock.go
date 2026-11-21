// Package acptest 는 테스트용 모의 ACP 에이전트다. 현재 테스트 바이너리를 자식 프로세스로 다시 띄워
// stdin/stdout 으로 ACP JSON-RPC 를 흉내 낸다.
//
// 사용법: 테스트 패키지의 TestMain 에서 acptest.Main(m) 을 부르고, 세션 풀 명령으로 acptest.Command(t) 를 쓴다.
package acptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
)

const (
	envServe    = "A2A_ACPTEST_SERVE"
	envListOnly = "A2A_ACPTEST_LIST_ONLY"
	envLog      = "A2A_ACPTEST_LOG"
)

// Main 은 TestMain 에서 호출한다. 모의 서버로 다시 실행된 경우 서버를 돌리고 종료한다.
func Main(m *testing.M) {
	if os.Getenv(envServe) == "1" {
		Serve(os.Stdin, os.Stdout)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Command 는 현재 테스트 바이너리를 모의 ACP 서버로 다시 띄우는 명령을 반환한다
func Command(t testing.TB) []string {
	t.Setenv(envServe, "1")
	return []string{os.Args[0]}
}

// ListOnly 는 모의 서버가 session/new 를 거부하게 한다. session/list 가 세션을 열지 않는지 확인할 때 쓴다.
func ListOnly(t testing.TB) {
	t.Setenv(envListOnly, "1")
}

// LogMethods 는 모의 서버가 받은 메서드 이름을 path 에 한 줄씩 남기게 한다
func LogMethods(t testing.TB, path string) {
	t.Setenv(envLog, path)
}

type request struct {
	ID     *int            `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Serve 는 in 에서 ACP 요청을 읽어 out 에 모의 응답을 쓴다
func Serve(in io.Reader, out io.Writer) {
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		var req request
		if json.Unmarshal([]byte(sc.Text()), &req) != nil || req.Method == "" {
			continue
		}
		logMethod(req.Method)
		handle(sc, out, req)
	}
}

func logMethod(method string) {
	path := os.Getenv(envLog)
	if path == "" {
		return
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		_, _ = fmt.Fprintln(f, method)
		_ = f.Close()
	}
}

func handle(sc *bufio.Scanner, out io.Writer, req request) {
	switch req.Method {
	case "initialize":
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"sessionCapabilities":{"close":{}}}}}`+"\n", *req.ID)
	case "session/new":
		if os.Getenv(envListOnly) == "1" {
			fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32600,"message":"unexpected session creation"}}`+"\n", *req.ID)
			return
		}
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"result":{"sessionId":"sess-mock-1"}}`+"\n", *req.ID)
	case "session/list":
		writeSessionList(out, req)
	case "session/load":
		fmt.Fprintln(out, `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"REPLAY_OLD_NOISE"}}}}`)
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"result":{"sessionId":"sess-loaded"}}`+"\n", *req.ID)
	case "session/set_mode", "session/set_config_option", "session/set_model", "session/close":
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"result":{}}`+"\n", *req.ID)
	case "session/prompt":
		fmt.Fprintln(out, `{"jsonrpc":"2.0","id":900,"method":"session/request_permission","params":{"options":[{"optionId":"allow-once","kind":"allow_once"}]}}`)
		sc.Scan() // 권한 응답을 읽고 버린다
		fmt.Fprintln(out, `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","name":"edit_file"}}}`)
		fmt.Fprintln(out, `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"모의 ACP 응답"}}}}`)
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"result":{"stopReason":"end_turn"}}`+"\n", *req.ID)
	}
}

func writeSessionList(out io.Writer, req request) {
	var params struct {
		Cwd    string `json:"cwd"`
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(req.Params, &params)
	sessions := []map[string]string{}
	if params.Cursor == "previous-page" {
		sessions = append(sessions, map[string]string{"sessionId": "existing", "cwd": params.Cwd, "title": "기존 대화"})
	}
	body, _ := json.Marshal(map[string]any{"sessions": sessions, "nextCursor": "next-page"})
	fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"result":%s}`+"\n", *req.ID, body)
}
