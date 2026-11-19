package collector

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Pairing by code: an agent without a token asks to pair, the Pi shows a
// six-digit code on its screen, the user types it in the agent, and the Pi
// returns a fresh token. Seeing the screen is the proof of authority.
//
// Limits: one pairing at a time, 2 minutes to answer, 3 wrong codes cancel
// it, at most 6 pairing requests a minute.
const (
	pairTTL      = 2 * time.Minute
	pairAttempts = 3
	pairPerMin   = 6
)

// PairRequest is what the screen shows while a pairing is pending.
type PairRequest struct {
	Label string
	Code  string // "482913"
	Left  time.Duration
}

type pending struct {
	id       string
	code     string
	label    string
	expires  time.Time
	attempts int
}

type Pairer struct {
	mu     sync.Mutex
	cur    *pending
	starts []time.Time
	now    func() time.Time
}

func NewPairer() *Pairer { return &Pairer{now: time.Now} }

// Pending returns the pairing to display, if any.
func (p *Pairer) Pending() (PairRequest, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur == nil || p.now().After(p.cur.expires) {
		p.cur = nil
		return PairRequest{}, false
	}
	return PairRequest{Label: p.cur.label, Code: p.cur.code, Left: p.cur.expires.Sub(p.now())}, true
}

var labelClean = regexp.MustCompile(`[^A-Za-z0-9 ._-]+`)

// CleanLabel turns a host name into a short display label.
func CleanLabel(s string) string {
	s = strings.TrimSuffix(strings.TrimSpace(s), ".local")
	s = strings.Trim(labelClean.ReplaceAllString(s, "-"), " .-_")
	if r := []rune(s); len(r) > 24 {
		s = strings.TrimRight(string(r[:24]), " .-_")
	}
	if s == "" {
		s = "machine"
	}
	return s
}

func randDigits(n int) (string, error) {
	max := big.NewInt(1)
	for i := 0; i < n; i++ {
		max.Mul(max, big.NewInt(10))
	}
	v, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", n, v), nil
}

func (p *Pairer) start(label string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	kept := p.starts[:0]
	for _, t := range p.starts {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	p.starts = kept
	if len(p.starts) >= pairPerMin {
		return "", errTooMany
	}
	p.starts = append(p.starts, now)
	code, err := randDigits(6)
	if err != nil {
		return "", err
	}
	idb := make([]byte, 16)
	if _, err := rand.Read(idb); err != nil {
		return "", err
	}
	id := hex.EncodeToString(idb)
	p.cur = &pending{id: id, code: code, label: CleanLabel(label), expires: now.Add(pairTTL)}
	return id, nil
}

var (
	errTooMany = fmt.Errorf("too many pairing requests")
	errNoPair  = fmt.Errorf("no such pairing")
	errBadCode = fmt.Errorf("wrong code")
	errGaveUp  = fmt.Errorf("too many wrong codes")
)

// check validates a code; on success it consumes the pairing and returns
// its label.
func (p *Pairer) check(id, code string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.cur
	if c == nil || subtle.ConstantTimeCompare([]byte(id), []byte(c.id)) != 1 || p.now().After(c.expires) {
		return "", errNoPair
	}
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if subtle.ConstantTimeCompare([]byte(code), []byte(c.code)) != 1 {
		c.attempts++
		if c.attempts >= pairAttempts {
			p.cur = nil
			return "", errGaveUp
		}
		return "", errBadCode
	}
	p.cur = nil
	return c.label, nil
}

func (s *Server) pairStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	id, err := s.Pairer.start(req.Label)
	if err == errTooMany {
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	}
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"id": id, "expires_in": int(pairTTL.Seconds())})
}

func (s *Server) pairFinish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string `json:"id"`
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	label, err := s.Pairer.check(req.ID, req.Code)
	switch err {
	case nil:
	case errBadCode:
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	default: // unknown, expired or cancelled: start over
		http.Error(w, err.Error(), http.StatusGone)
		return
	}
	tok, err := NewToken()
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	hash := HashSecret(tok)
	s.mu.Lock()
	label = uniqueLabel(s.Auth.AgentTokens, label)
	if s.SaveAgent != nil {
		if err := s.SaveAgent(hash, label); err != nil {
			s.mu.Unlock()
			if s.Log != nil {
				s.Log.Printf("pairing: cannot save agent: %v", err)
			}
			http.Error(w, "cannot save", http.StatusInternalServerError)
			return
		}
	}
	if s.Auth.AgentTokens == nil {
		s.Auth.AgentTokens = map[string]string{}
	}
	s.Auth.AgentTokens[hash] = label
	s.mu.Unlock()
	if s.Log != nil {
		s.Log.Printf("paired new agent %q", label)
	}
	writeJSON(w, map[string]any{"token": tok, "label": label, "board": s.BoardID})
}

// uniqueLabel avoids two machines sharing a room on the screen.
func uniqueLabel(existing map[string]string, label string) string {
	taken := map[string]bool{}
	for _, l := range existing {
		taken[l] = true
	}
	if !taken[label] {
		return label
	}
	for i := 2; ; i++ {
		if l := fmt.Sprintf("%s %d", label, i); !taken[l] {
			return l
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
