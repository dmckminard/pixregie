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
	"sync"
	"time"
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
	// Extra GET routes served behind the debug password (e.g. /screen.png).
	Extra map[string]http.Handler
	// AdminPost are POST routes behind the debug password that change the
	// configuration; they also require a same-origin request (see admin).
	AdminPost map[string]http.Handler
	// Pairer enables pairing by code shown on the screen; nil disables it.
	Pairer *Pairer
	// SaveAgent persists a newly paired agent (token hash, label).
	SaveAgent func(hash, label string) error
	// BoardID identifies this display, so a paired agent never reports to
	// another Pixregie board on the same network.
	BoardID string

	mu sync.RWMutex // guards Auth.AgentTokens, which pairing extends

	failMu   sync.Mutex
	failures []time.Time // recent wrong debug passwords
}

// Password guessing is limited: after 10 wrong passwords in 10 minutes the
// page refuses every attempt until older failures expire.
const (
	maxFailures = 10
	failureSpan = 10 * time.Minute
)

func (s *Server) lockedOut(now time.Time) bool {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	kept := s.failures[:0]
	for _, t := range s.failures {
		if now.Sub(t) < failureSpan {
			kept = append(kept, t)
		}
	}
	s.failures = kept
	return len(s.failures) >= maxFailures
}

func (s *Server) recordFailure(now time.Time) {
	s.failMu.Lock()
	s.failures = append(s.failures, now)
	s.failMu.Unlock()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/report", s.report)
	mux.HandleFunc("GET /v1/hello", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"board": s.BoardID})
	})
	if s.Pairer != nil {
		mux.HandleFunc("POST /v1/pair/start", s.pairStart)
		mux.HandleFunc("POST /v1/pair/finish", s.pairFinish)
	}
	mux.Handle("GET /v1/state", s.debugOnly(http.HandlerFunc(s.state)))
	for path, h := range s.Extra {
		mux.Handle("GET "+path, s.debugOnly(h))
	}
	for path, h := range s.AdminPost {
		mux.Handle("POST "+path, s.debugOnly(sameOrigin(h)))
	}
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

// SetAgents replaces the accepted agent tokens (config reloaded on disk).
func (s *Server) SetAgents(agents map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Auth.AgentTokens = agents
}

// agent resolves a bearer token to its machine label.
func (s *Server) agent(r *http.Request) (string, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
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
		now := time.Now()
		if s.lockedOut(now) {
			http.Error(w, "too many wrong passwords, try again later", http.StatusTooManyRequests)
			return
		}
		_, pw, ok := r.BasicAuth()
		if ok && !CheckPassword(s.Auth.DebugPassword, pw) {
			s.recordFailure(now)
			ok = false
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="pixregie"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin blocks cross-site requests: the browser keeps sending the
// debug password, so another site could otherwise post to the Pi. A
// custom header cannot be added cross-site without a CORS preflight, which
// is never answered; the Origin, when sent, must be this host.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Pixregie") != "1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host && o != "https://"+r.Host {
			http.Error(w, "forbidden", http.StatusForbidden)
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
