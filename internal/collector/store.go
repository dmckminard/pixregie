// Package collector keeps the latest session snapshot reported by each
// machine and serves it to the display and the debug page.
package collector

import (
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dmckminard/pixregie/internal/session"
)

// Limits protect the Pi from a buggy or hostile agent on the LAN.
const (
	MaxSessionsPerMachine = 200
	maxField              = 64 // characters kept in any string field
)

// Report is what an agent sends: its full current view, replacing the
// previous one. Snapshots (rather than events) make the protocol
// idempotent: a lost request is repaired by the next one.
type Report struct {
	Sessions []session.Session `json:"sessions"`
}

// Machine is one reporting machine as seen by the display.
type Machine struct {
	Name     string            `json:"name"`
	Online   bool              `json:"online"`
	LastSeen time.Time         `json:"last_seen"`
	Sessions []session.Session `json:"sessions"`
}

// State is the full picture handed to renderers.
type State struct {
	Generated time.Time `json:"generated"`
	Machines  []Machine `json:"machines"`
}

type entry struct {
	lastSeen time.Time
	sessions []session.Session
}

// Store is safe for concurrent use.
type Store struct {
	mu           sync.Mutex
	machines     map[string]*entry
	offlineAfter time.Duration
	forgetAfter  time.Duration
	now          func() time.Time
	changed      chan struct{}
}

// NewStore: a machine is shown offline after offlineAfter without a report
// and removed after forgetAfter.
func NewStore(offlineAfter, forgetAfter time.Duration) *Store {
	return &Store{
		machines:     map[string]*entry{},
		offlineAfter: offlineAfter,
		forgetAfter:  forgetAfter,
		now:          time.Now,
		changed:      make(chan struct{}),
	}
}

// Put replaces the snapshot of one machine. The machine name comes from the
// authenticated token, never from the report body, so an agent cannot
// overwrite another machine's sessions.
func (s *Store) Put(machine string, r Report) {
	list := r.Sessions
	if len(list) > MaxSessionsPerMachine {
		list = list[:MaxSessionsPerMachine]
	}
	clean := make([]session.Session, 0, len(list))
	for _, x := range list {
		x.Machine = machine
		x.ID = trim(x.ID)
		x.Project = trim(x.Project)
		x.Branch = trim(x.Branch)
		x.Tool = trim(x.Tool)
		x.Entrypoint = trim(x.Entrypoint)
		switch x.Status {
		case session.Working, session.Waiting, session.Idle, session.Stale:
		default:
			x.Status = session.Idle
		}
		clean = append(clean, x)
	}
	s.mu.Lock()
	s.machines[machine] = &entry{lastSeen: s.now(), sessions: clean}
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

// Changed returns a channel closed at the next Put (for live renderers).
func (s *Store) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

// Snapshot returns a sorted copy of the current state.
func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	st := State{Generated: now}
	for name, e := range s.machines {
		age := now.Sub(e.lastSeen)
		if s.forgetAfter > 0 && age > s.forgetAfter {
			delete(s.machines, name)
			continue
		}
		m := Machine{
			Name:     name,
			Online:   age <= s.offlineAfter,
			LastSeen: e.lastSeen,
			Sessions: append([]session.Session(nil), e.sessions...),
		}
		sort.Slice(m.Sessions, func(i, j int) bool {
			a, b := m.Sessions[i], m.Sessions[j]
			if a.Project != b.Project {
				return a.Project < b.Project
			}
			return a.ID < b.ID
		})
		st.Machines = append(st.Machines, m)
	}
	sort.Slice(st.Machines, func(i, j int) bool { return st.Machines[i].Name < st.Machines[j].Name })
	return st
}

// trim keeps a short, printable version of an untrusted string.
func trim(v string) string {
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	if utf8.RuneCountInString(v) > maxField {
		v = string([]rune(v)[:maxField])
	}
	return v
}
