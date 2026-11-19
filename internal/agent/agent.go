// Package agent is the reporting loop shared by the command-line agent and
// the menu-bar app: scan transcripts, merge hook knowledge, push the
// snapshot to the display, and expose a status for the UI.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/dmckminard/pixregie/internal/discover"
	"github.com/dmckminard/pixregie/internal/hooks"
	"github.com/dmckminard/pixregie/internal/session"
)

// Config for one agent.
type Config struct {
	Server  string        // display URL; empty = mDNS discovery
	Token   string        // from `pixregie-display add-agent`
	Root    string        // transcripts dir; empty = ~/.claude/projects
	Every   time.Duration // report interval (default 3 s)
	Tracker *hooks.Tracker
}

// Status is what the tray shows.
type Status struct {
	Connected bool
	Server    string // display URL in use, if known
	Err       string // last problem, human readable
	Counts    map[session.Status]int
}

// Worst returns the most urgent state present, for the tray icon.
func (s Status) Worst() session.Status {
	for _, st := range []session.Status{session.Waiting, session.Stale, session.Working} {
		if s.Counts[st] > 0 {
			return st
		}
	}
	return session.Idle
}

type Agent struct {
	cfg    Config
	client *http.Client
	host   string

	mu      sync.Mutex
	status  Status
	server  string
	kick    chan struct{}
	changed chan struct{}
}

func New(cfg Config) (*Agent, error) {
	if cfg.Root == "" {
		r, err := session.DefaultRoot()
		if err != nil {
			return nil, err
		}
		cfg.Root = r
	}
	if cfg.Every == 0 {
		cfg.Every = 3 * time.Second
	}
	if cfg.Tracker == nil {
		cfg.Tracker = hooks.NewTracker("")
	}
	if cfg.Server != "" {
		if err := checkURL(cfg.Server); err != nil {
			return nil, err
		}
	}
	host, _ := os.Hostname()
	return &Agent{
		cfg:     cfg,
		client:  &http.Client{Timeout: 5 * time.Second},
		host:    host,
		server:  cfg.Server,
		kick:    make(chan struct{}, 1),
		changed: make(chan struct{}, 1),
	}, nil
}

func checkURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("invalid display URL %q", s)
	}
	return nil
}

// Status returns the latest status.
func (a *Agent) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// Changed signals (coalesced) whenever Status changes.
func (a *Agent) Changed() <-chan struct{} { return a.changed }

// Refresh triggers an immediate scan, and a new discovery if not connected.
func (a *Agent) Refresh() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

// Run reports until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) {
	lastDiscovery := time.Time{}
	for {
		a.tick(ctx, &lastDiscovery)
		select {
		case <-ctx.Done():
			return
		case <-a.kick:
			lastDiscovery = time.Time{}
		case <-time.After(a.cfg.Every):
		}
	}
}

func (a *Agent) tick(ctx context.Context, lastDiscovery *time.Time) {
	now := time.Now()
	list, err := session.Scan(a.cfg.Root, 24*time.Hour, session.Options{
		Machine: a.host, StaleAfter: 10 * time.Minute, Now: now,
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		a.set(false, "", "lecture des sessions : "+err.Error(), nil)
		return
	}
	list = filter(a.cfg.Tracker.Apply(list), now)

	counts := map[session.Status]int{}
	for _, s := range list {
		counts[s.Status]++
	}

	a.mu.Lock()
	server := a.server
	a.mu.Unlock()
	if server == "" {
		if !lastDiscovery.IsZero() && now.Sub(*lastDiscovery) < 30*time.Second {
			return
		}
		*lastDiscovery = now
		dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		found, err := discover.Find(dctx, 3*time.Second)
		cancel()
		if err != nil {
			a.set(false, "", "Pi introuvable sur le réseau", counts)
			return
		}
		server = found
		a.mu.Lock()
		a.server = found
		a.mu.Unlock()
	}

	if err := a.push(server, list); err != nil {
		a.set(false, server, err.Error(), counts)
		if a.cfg.Server == "" {
			// Discovered address may be stale (DHCP): look again later.
			a.mu.Lock()
			a.server = ""
			a.mu.Unlock()
		}
		return
	}
	a.set(true, server, "", counts)
}

// filter keeps what is worth showing: open sessions, anything not idle,
// and sessions finished less than an hour ago.
func filter(in []session.Session, now time.Time) []session.Session {
	out := in[:0:0]
	for _, s := range in {
		if s.Status == session.Idle && !s.Open && now.Sub(s.Modified) > time.Hour {
			continue
		}
		out = append(out, s)
	}
	return out
}

// ErrUnauthorized means the display rejected the token.
var ErrUnauthorized = errors.New("token refusé par le Pi")

func (a *Agent) push(server string, list []session.Session) error {
	if list == nil {
		list = []session.Session{}
	}
	body, err := json.Marshal(map[string]any{"sessions": list})
	if err != nil {
		return err
	}
	u, _ := url.Parse(server)
	req, err := http.NewRequest(http.MethodPost, u.JoinPath("/v1/report").String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return errors.New("Pi injoignable")
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusUnauthorized:
		return ErrUnauthorized
	default:
		return fmt.Errorf("le Pi a répondu %s", resp.Status)
	}
}

func (a *Agent) set(ok bool, server, msg string, counts map[session.Status]int) {
	a.mu.Lock()
	a.status = Status{Connected: ok, Server: server, Err: msg, Counts: counts}
	a.mu.Unlock()
	select {
	case a.changed <- struct{}{}:
	default:
	}
}
