// Package jsonrpc 는 MCP stdio 서버와 A2A HTTP 서버가 함께 쓰는 JSON-RPC 2.0 요청 타입을 정의한다.
package jsonrpc

// Request 는 JSON-RPC 2.0 요청이다. id 는 문자열과 숫자를 모두 받으려고 any 로 둔다.
type Request struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}
