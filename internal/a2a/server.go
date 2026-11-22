// Package a2a 는 Dispatcher 의 도구를 A2A HTTP 엔드포인트(/.well-known/agent.json, /a2a)로 노출한다.
package a2a

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/LeeYudok/a2a-acp-bridge/internal/dispatch"
	"github.com/LeeYudok/a2a-acp-bridge/internal/jsonrpc"
	"github.com/LeeYudok/a2a-acp-bridge/internal/monitor"
)

// methodTools 는 A2A JSON-RPC 메서드를 Dispatcher 도구 이름으로 잇는다
var methodTools = map[string]string{
	"tasks/send":  "a2a_send_message",
	"tasks/get":   "a2a_task_status",
	"acp/agy":     "acp_delegate_to_agy",
	"acp/control": "acp_session_control",
	"a2a/codex":   "a2a_delegate_to_codex",
	"a2a/claude":  "a2a_delegate_to_claude",
}

// NewMux 는 Go 1.22+ ServeMux 로 A2A HTTP 라우터를 만든다
func NewMux(d *dispatch.Dispatcher) *http.ServeMux {
	mux := http.NewServeMux()
	handleCards := func(w http.ResponseWriter, r *http.Request) {
		res, err := d.CallTool(r.Context(), "a2a_discover_agents", map[string]any{})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
	mux.HandleFunc("GET /.well-known/agent.json", handleCards)
	mux.HandleFunc("GET /agents", handleCards)
	mux.HandleFunc("GET /monitor/snapshot", func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := monitor.Capture(d.Board, time.Now(), monitor.DefaultStaleAfter)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	})
	mux.HandleFunc("POST /a2a", func(w http.ResponseWriter, r *http.Request) {
		handlePost(d, w, r)
	})
	return mux
}

func handlePost(d *dispatch.Dispatcher, w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req jsonrpc.Request
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	toolName, ok := methodTools[req.Method]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": "unknown method: " + req.Method})
		return
	}
	res, err := d.CallTool(r.Context(), toolName, req.Params)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	b, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// ListenAndServe 는 ReadHeaderTimeout 이 걸린 A2A HTTP 서버를 띄운다
func ListenAndServe(d *dispatch.Dispatcher, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           NewMux(d),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return srv.ListenAndServe()
}
