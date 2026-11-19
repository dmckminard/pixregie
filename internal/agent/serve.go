package agent

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dmckminard/pixregie/internal/session"
)

// ServeHandler is the pull endpoint for servers: the Pi, through its SSH
// tunnel, asks GET /v1/snapshot with the server token. Read-only: nothing
// on it can change anything on the server.
func ServeHandler(a *Agent, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		list, err := a.Snapshot(time.Now())
		if err != nil {
			http.Error(w, "scan failed", http.StatusInternalServerError)
			return
		}
		if list == nil {
			list = []session.Session{}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(map[string]any{"sessions": list})
	})
	return mux
}
