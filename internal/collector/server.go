package collector

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strings"
)

//go:embed web
var webFS embed.FS

const maxBody = 256 << 10

// Auth holds hashed secrets only: a leaked config file reveals no token.
type Auth struct {
	// AgentTokens maps sha256(token) in hex to the machine label it reports as.
	AgentTokens map[string]string
	// DebugPassword is a HashPassword result. Empty disables the page.
	DebugPassword string
}

// HashSecret returns the hex sha256 of a random token, as stored in the config.
func HashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// Server exposes the agent API and the debug page.
type Server struct {
	Store *Store
	Auth  Auth
	Log   *log.Logger
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/report", s.report)
	mux.Handle("GET /v1/state", s.debugOnly(http.HandlerFunc(s.state)))
	static, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", s.debugOnly(http.FileServerFS(static)))
	return secureHeaders(mux)
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	machine, ok := s.agent(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var rep Report
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&rep); err != nil {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}
	s.Store.Put(machine, rep)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(s.Store.Snapshot())
}

// agent resolves a bearer token to its machine label.
func (s *Server) agent(r *http.Request) (string, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return "", false
	}
	label, ok := s.Auth.AgentTokens[HashSecret(tok)]
	return label, ok
}

// debugOnly guards the page with HTTP basic auth (any user name).
func (s *Server) debugOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Auth.DebugPassword == "" {
			http.NotFound(w, r)
			return
		}
		_, pw, ok := r.BasicAuth()
		if !ok || !CheckPassword(s.Auth.DebugPassword, pw) {
			w.Header().Set("WWW-Authenticate", `Basic realm="pixregie"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
