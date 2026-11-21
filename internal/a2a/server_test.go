package a2a

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
