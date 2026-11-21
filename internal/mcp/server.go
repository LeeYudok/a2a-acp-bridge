// Package mcp 는 Dispatcher 의 도구를 MCP(모델 문맥 프로토콜) stdio JSON-RPC 2.0 서버로 노출한다.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/LeeYudok/a2a-acp-bridge/internal/agents"
	"github.com/LeeYudok/a2a-acp-bridge/internal/dispatch"
	"github.com/LeeYudok/a2a-acp-bridge/internal/jsonrpc"
)

// Handle 은 MCP JSON-RPC 2.0 요청 하나를 처리하고 응답 맵을 반환한다. 알림이면 nil 이다.
func Handle(ctx context.Context, d *dispatch.Dispatcher, req jsonrpc.Request) map[string]any {
	if strings.HasPrefix(req.Method, "notifications/") {
		return nil
	}
	switch req.Method {
	case "initialize":
		rememberClient(d, req.Params)
		return result(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "a2a-acp-bridge", "version": "2.0.0"},
		})
	case "ping":
		return result(req.ID, map[string]any{})
	case "tools/list":
		return result(req.ID, map[string]any{"tools": dispatch.Tools()})
	case "tools/call":
		return callTool(ctx, d, req.ID, req.Params)
	default:
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"error":   map[string]any{"code": -32601, "message": "Method not found: " + req.Method},
		}
	}
}

// rememberClient 는 Caller 가 비어 있으면 initialize 의 clientInfo.name 으로 호출 에이전트를 정한다.
// 그러면 Codex·AGY 가 MCP 로 위임할 때 from_agent 를 빼도 보드에 제 이름으로 기록된다.
func rememberClient(d *dispatch.Dispatcher, params map[string]any) {
	if d.Caller != "" {
		return
	}
	info, _ := params["clientInfo"].(map[string]any)
	name, _ := info["name"].(string)
	d.Caller = agents.FromClientName(name)
}

func result(id any, payload map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": payload}
}

func textContent(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func callTool(ctx context.Context, d *dispatch.Dispatcher, reqID any, params map[string]any) map[string]any {
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	res, err := d.CallTool(ctx, name, args)
	if err != nil {
		return result(reqID, textContent("Error: "+err.Error(), true))
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	return result(reqID, textContent(string(b), false))
}

// Serve 는 in 에서 줄 단위 JSON-RPC 요청을 읽어 out 에 응답을 쓴다. 끝나면 Dispatcher 를 닫는다.
func Serve(ctx context.Context, d *dispatch.Dispatcher, in io.Reader, out io.Writer) error {
	defer d.Close()
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req jsonrpc.Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		resp := Handle(ctx, d, req)
		if resp == nil {
			continue
		}
		b, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		if _, err := out.Write(append(b, '\n')); err != nil {
			return fmt.Errorf("MCP stdout 쓰기 실패: %w", err)
		}
	}
	return sc.Err()
}
