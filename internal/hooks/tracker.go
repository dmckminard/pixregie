// Package hooks receives Claude Code hook events on a local port and turns
// them into facts the transcripts cannot give, the moment they happen:
// which sessions are actually open, when a turn starts and ends, and which
// one is waiting for a permission answer. Without hooks (sessions started
// before installing, old Claude Code) the transcripts alone still work.
//
// Only the session id and the event kind are kept. Hook payloads also carry
// paths and messages; those are read to classify the event and dropped.
package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dmckminard/pixregie/internal/session"
)

// Event is the part of a hook payload we use.
type Event struct {
	SessionID        string `json:"session_id"`
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"`
	Message          string `json:"message"` // classified, never stored
}

var sessionIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type state struct {
	Open    bool      `json:"open"`
	Closed  bool      `json:"closed"`
	Waiting time.Time `json:"waiting,omitempty"` // permission prompt shown at
	Busy    time.Time `json:"busy,omitempty"`    // last prompt submitted at
	Done    time.Time `json:"done,omitempty"`    // last turn finished at
	Seen    time.Time `json:"seen"`
}

// Tracker is safe for concurrent use.
type Tracker struct {
	mu   sync.Mutex
	m    map[string]*state
	path string // persistence file; empty = memory only
	now  func() time.Time
}

// NewTracker loads previous state from path (if any) so an agent restart
// does not forget which sessions are open.
func NewTracker(path string) *Tracker {
	t := &Tracker{m: map[string]*state{}, path: path, now: time.Now}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			json.Unmarshal(b, &t.m)
		}
	}
	return t
}

// isPermission tells a permission prompt from the "waiting for your input"
// reminder, which only means the turn is over.
func isPermission(e Event) bool {
	if e.NotificationType != "" {
		return e.NotificationType == "permission_prompt"
	}
	return strings.Contains(strings.ToLower(e.Message), "permission")
}

// Handle records one hook event. Unknown events and ids are ignored.
func (t *Tracker) Handle(e Event) {
	if !sessionIDRE.MatchString(e.SessionID) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	st := t.m[e.SessionID]
	if st == nil {
		st = &state{}
		t.m[e.SessionID] = st
	}
	st.Seen = now
	switch e.HookEventName {
	case "SessionStart":
		st.Open, st.Closed, st.Waiting = true, false, time.Time{}
	case "SessionEnd":
		st.Open, st.Closed, st.Waiting = false, true, time.Time{}
	case "UserPromptSubmit":
		st.Open, st.Closed, st.Waiting, st.Busy = true, false, time.Time{}, now
	case "Stop", "StopFailure":
		st.Open, st.Closed, st.Waiting, st.Done = true, false, time.Time{}, now
	case "PermissionRequest":
		st.Open, st.Closed, st.Waiting = true, false, now
	case "Notification": // older Claude Code: no PermissionRequest event
		st.Open, st.Closed = true, false
		if isPermission(e) {
			st.Waiting = now
		}
	default:
		return
	}
	t.saveLocked()
}

// Apply merges hook knowledge into scanned sessions:
//   - sessions Claude Code reported closed are dropped;
//   - sessions reported open are flagged Open;
//   - a permission prompt makes the session Waiting until the transcript
//     moves again (the tool ran or was refused);
//   - a turn in progress (prompt submitted, not finished) is Working even
//     while the transcript is quiet (thinking), and Stale after StaleAfter
//     of silence; a finished turn is Idle at once.
func (t *Tracker) Apply(in []session.Session) []session.Session {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	out := in[:0:0]
	for _, s := range in {
		st := t.m[s.ID]
		if st == nil {
			out = append(out, s)
			continue
		}
		if st.Closed {
			continue
		}
		s.Open = st.Open
		if !st.Waiting.IsZero() {
			// Small grace: the transcript is often written a moment
			// before the notification fires.
			if s.Modified.Before(st.Waiting.Add(2 * time.Second)) {
				s.Status, s.Tool = session.Waiting, ""
			} else {
				st.Waiting = time.Time{}
			}
		}
		if s.Status != session.Waiting {
			s = applyTurn(s, st, now)
		}
		out = append(out, s)
	}
	return out
}

// StaleAfter is how long a turn in progress may stay silent before the
// session is shown as stuck (same as the transcript scan).
const StaleAfter = 10 * time.Minute

func applyTurn(s session.Session, st *state, now time.Time) session.Session {
	switch {
	case !st.Busy.IsZero() && st.Busy.After(st.Done):
		last := s.Modified
		if st.Busy.After(last) {
			last = st.Busy
		}
		if now.Sub(last) > StaleAfter {
			s.Status = session.Stale
		} else if s.Status == session.Idle || s.Status == session.Stale {
			s.Status = session.Working
		}
	case !st.Done.IsZero() && !s.Modified.After(st.Done.Add(2*time.Second)):
		s.Status, s.Tool = session.Idle, ""
	}
	return s
}

// Prune forgets sessions not heard of for a week.
func (t *Tracker) Prune() {
	t.mu.Lock()
	defer t.mu.Unlock()
	cut := t.now().Add(-7 * 24 * time.Hour)
	for id, st := range t.m {
		if st.Seen.Before(cut) {
			delete(t.m, id)
		}
	}
	t.saveLocked()
}

func (t *Tracker) saveLocked() {
	if t.path == "" {
		return
	}
	b, err := json.Marshal(t.m)
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(t.path), 0o700)
	tmp := t.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, t.path)
	}
}
