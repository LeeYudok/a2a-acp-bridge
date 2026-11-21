package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeeYudok/a2a-acp-bridge/internal/acp"
	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
	"github.com/LeeYudok/a2a-acp-bridge/internal/dispatch"
)

func TestServeHandlesInitializeAndToolsList(t *testing.T) {
	d := &dispatch.Dispatcher{Board: board.New(filepath.Join(t.TempDir(), "board.json")), Pool: acp.NewSessionPool(nil), DefaultCwd: t.TempDir()}
	in := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"no_such_tool"}}`,
	}, "\n") + "\n")
	var out bytes.Buffer
	if err := Serve(context.Background(), d, in, &out); err != nil {
		t.Fatalf("Serve 실패: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if got, want := len(lines), 3; got != want {
		t.Fatalf("MCP 출력 줄 수 = %d, 원하는 값 %d (알림에는 응답하지 않아야 함)", got, want)
	}
	var last struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &last); err != nil || !last.Result.IsError {
		t.Fatalf("없는 도구 호출은 isError=true 여야 함: %s", lines[2])
	}
}
