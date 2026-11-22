package a2a

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
)

// poolActor maps the operator's configured bearer credential to the user role.
func poolActor(r *http.Request) string {
	key := os.Getenv("A2A_POOL_WRITE_TOKEN")
	value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if ok && key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(value)) == 1 {
		return "user"
	}
	return ""
}
