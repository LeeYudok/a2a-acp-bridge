package main

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// NewA2AMux 는 Go 1.22+ ServeMux 로 A2A HTTP 라우터를 생성한다
func NewA2AMux(d *Dispatcher) *http.ServeMux {
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
	mux.HandleFunc("POST /a2a", func(w http.ResponseWriter, r *http.Request) {
		handleA2APost(d, w, r)
	})
	return mux
}

func handleA2APost(d *Dispatcher, w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req MCPRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	mapping := map[string]string{
		"tasks/send":  "a2a_send_message",
		"tasks/get":   "a2a_task_status",
		"acp/agy":     "acp_delegate_to_agy",
		"acp/control": "acp_session_control",
		"a2a/codex":   "a2a_delegate_to_codex",
	}
	toolName, ok := mapping[req.Method]
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

// RunA2AHTTPServer 는 ReadHeaderTimeout 이 설정된 A2A HTTP 서버를 기동한다
func RunA2AHTTPServer(d *Dispatcher, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           NewA2AMux(d),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return srv.ListenAndServe()
}
