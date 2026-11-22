package a2a

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
	"github.com/LeeYudok/a2a-acp-bridge/internal/dispatch"
)

func TestHTTPPoolAuthorization(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("A2A_POOL_FILE", filepath.Join(dir, "pool.json"))
	t.Setenv("A2A_POOL_WRITE_TOKEN", "test-credential")
	d := &dispatch.Dispatcher{Board: board.New(filepath.Join(dir, "board.json")), PoolActor: "user"}
	handler := NewMux(d)
	for _, tc := range []struct {
		name, params, bearer string
		code                 int
	}{
		{"public status", `{"action":"status"}`, "", http.StatusOK},
		{"anonymous set", `{"action":"set","max_total":2,"from_agent":"user"}`, "", http.StatusForbidden},
		{"invalid credential", `{"action":"set","max_total":2}`, "wrong", http.StatusForbidden},
		{"authorized set", `{"action":"set","max_total":2}`, "test-credential", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/a2a", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"a2a/pool","params":`+tc.params+`}`))
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			if resp.Code != tc.code {
				t.Errorf("status = %d, want %d: %s", resp.Code, tc.code, resp.Body)
			}
		})
	}
	status, err := d.Board.PoolStatus(nil)
	if err != nil || status.MaxTotal == nil || *status.MaxTotal != 2 {
		t.Fatalf("pool = %+v, %v", status, err)
	}
}
