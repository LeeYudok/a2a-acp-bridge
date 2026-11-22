package a2a

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeeYudok/a2a-acp-bridge/internal/acp"
	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
	"github.com/LeeYudok/a2a-acp-bridge/internal/dispatch"
)

func TestAgentCardAndControlEndpoints(t *testing.T) {
	d := &dispatch.Dispatcher{Board: board.New(filepath.Join(t.TempDir(), "board.json")), Pool: acp.NewSessionPool(nil), DefaultCwd: t.TempDir()}
	srv := httptest.NewServer(NewMux(d))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/agent.json")
	if err != nil {
		t.Fatalf("GET /.well-known/agent.json 실패: %v", err)
	}
	resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("GET 상태코드 = %d, 원하는 값 %d", got, want)
	}

	cases := []struct {
		body string
		want int
	}{
		{`{"jsonrpc":"2.0","id":10,"method":"acp/control","params":{"action":"status"}}`, http.StatusOK},
		{`{"jsonrpc":"2.0","id":11,"method":"no/such"}`, http.StatusBadRequest},
		{`not json`, http.StatusBadRequest},
	}
	for _, c := range cases {
		postResp, err := http.Post(srv.URL+"/a2a", "application/json", bytes.NewBufferString(c.body))
		if err != nil {
			t.Fatalf("POST /a2a 실패: %v", err)
		}
		postResp.Body.Close()
		if postResp.StatusCode != c.want {
			t.Fatalf("POST %s 상태코드 = %d, 원하는 값 %d", c.body, postResp.StatusCode, c.want)
		}
	}
}

func TestMonitorSnapshotEndpointIsReadOnly(t *testing.T) {
	b := board.New(filepath.Join(t.TempDir(), "board.json"))
	if _, err := b.SendMessage("codex", "agy", "private prompt body", "delegate", ""); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}
	d := &dispatch.Dispatcher{Board: b, Pool: acp.NewSessionPool(nil), DefaultCwd: t.TempDir()}
	srv := httptest.NewServer(NewMux(d))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/monitor/snapshot")
	if err != nil {
		t.Fatalf("GET /monitor/snapshot failed: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", resp.StatusCode, http.StatusOK, raw)
	}
	if strings.Contains(string(raw), "private prompt body") {
		t.Fatalf("snapshot leaked message body: %s", raw)
	}
	var got struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
		Tasks []struct {
			TaskID string `json:"task_id"`
		} `json:"tasks"`
		Sessions []json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(got.Agents) != 3 || len(got.Tasks) != 1 || got.Tasks[0].TaskID == "" || got.Sessions == nil {
		t.Fatalf("snapshot = %s", raw)
	}
}

func TestMonitorSnapshotDoesNotLeakBoardErrors(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "broken-board.json")
	if err := os.WriteFile(boardPath, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write broken board: %v", err)
	}
	b := board.New(boardPath)
	d := &dispatch.Dispatcher{Board: b, Pool: acp.NewSessionPool(nil), DefaultCwd: t.TempDir()}
	srv := httptest.NewServer(NewMux(d))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/monitor/snapshot")
	if err != nil {
		t.Fatalf("GET /monitor/snapshot failed: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d: %s", resp.StatusCode, http.StatusInternalServerError, raw)
	}
	if !strings.Contains(string(raw), `"error":"snapshot unavailable"`) || strings.Contains(string(raw), boardPath) {
		t.Fatalf("error response leaked board path: %s", raw)
	}
}

func TestMonitorSnapshotCacheCoalescesPolling(t *testing.T) {
	b := board.New(filepath.Join(t.TempDir(), "board.json"))
	d := &dispatch.Dispatcher{Board: b, Pool: acp.NewSessionPool(nil), DefaultCwd: t.TempDir()}
	srv := httptest.NewServer(NewMux(d))
	defer srv.Close()

	first, err := http.Get(srv.URL + "/monitor/snapshot")
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	firstRaw, _ := io.ReadAll(first.Body)
	first.Body.Close()
	if _, err := b.SendMessage("codex", "agy", "new work", "delegate", ""); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}
	second, err := http.Get(srv.URL + "/monitor/snapshot")
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	secondRaw, _ := io.ReadAll(second.Body)
	second.Body.Close()
	if !bytes.Equal(firstRaw, secondRaw) {
		t.Fatalf("cache returned different snapshots within TTL:\nfirst=%s\nsecond=%s", firstRaw, secondRaw)
	}
}
