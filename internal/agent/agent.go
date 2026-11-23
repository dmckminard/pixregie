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
	"strings"
	"sync"
	"time"

	"github.com/dmckminard/pixregie/internal/discover"
	"github.com/dmckminard/pixregie/internal/hooks"
	"github.com/dmckminard/pixregie/internal/session"
	"github.com/dmckminard/pixregie/internal/tlspin"
)

// Config for one agent.
type Config struct {
	Server  string        // display URL; empty = mDNS discovery
	Token   string        // from `pixregie-display add-agent`
	Root    string        // transcripts dir; empty = ~/.claude/projects
	Every   time.Duration // report interval (default 3 s)
	Tracker *hooks.Tracker
	// Board is the paired display's identifier. When set, the agent only
	// reports to a display that presents it, so session data never goes to
	// another Pixregie board on the same network.
	Board string
	// OnBoard is called once the board identifier is learnt (agents paired
	// before identifiers existed learn it at their first good report).
	OnBoard func(board string)
	// Pin is the SHA-256 of the display's TLS certificate, learnt at
	// pairing: the agent talks to that board only. Agents paired before
	// HTTPS learn it at their first accepted report (OnPin).
	Pin   string
	OnPin func(pin string)
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
	pinner *tlspin.Pinner
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
		cfg.Server = HTTPS(cfg.Server)
		if err := checkURL(cfg.Server); err != nil {
			return nil, err
		}
	}
	host, _ := os.Hostname()
	p := tlspin.NewPinner(cfg.Pin)
	return &Agent{
		cfg:     cfg,
		pinner:  p,
		client:  p.Client(5 * time.Second),
		host:    host,
		server:  cfg.Server,
		kick:    make(chan struct{}, 1),
		changed: make(chan struct{}, 1),
	}, nil
}

// HTTPS rewrites a display URL saved before the board served HTTPS.
func HTTPS(s string) string {
	if rest, ok := strings.CutPrefix(s, "http://"); ok {
		return "https://" + rest
	}
	return s
}

func checkURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" {
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
	list, err := a.Snapshot(now)
	if err != nil {
		a.set(false, "", "lecture des sessions : "+err.Error(), nil)
		return
	}

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
		found, err := discover.FindAll(dctx, 3*time.Second)
		cancel()
		if err != nil {
			a.set(false, "", "Pi introuvable sur le réseau", counts)
			return
		}
		server = a.pick(ctx, found)
		if server == "" {
			a.set(false, "", "écran Pixregie introuvable (autre écran détecté, ignoré)", counts)
			return
		}
		a.mu.Lock()
		a.server = server
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
	if a.cfg.Pin == "" {
		// First accepted report since HTTPS: the token proved this is our
		// board, trust its certificate from now on.
		if fp := a.pinner.Seen(); fp != "" {
			a.cfg.Pin = fp
			a.pinner.Pin(fp)
			a.client = a.pinner.Client(5 * time.Second)
			if a.cfg.OnPin != nil {
				a.cfg.OnPin(fp)
			}
		}
	}
	if a.cfg.Board == "" {
		if id, err := a.hello(ctx, server); err == nil && id != "" {
			a.cfg.Board = id
			if a.cfg.OnBoard != nil {
				a.cfg.OnBoard(id)
			}
		}
	}
	a.set(true, server, "", counts)
}

// pick returns the discovered display that is ours. Without a known board
// (old pairing), the first one.
func (a *Agent) pick(ctx context.Context, urls []string) string {
	if a.cfg.Board == "" {
		return urls[0]
	}
	for _, u := range urls {
		if id, err := a.hello(ctx, u); err == nil && id == a.cfg.Board {
			return u
		}
	}
	return ""
}

// hello asks a display for its board identifier.
func (a *Agent) hello(ctx context.Context, server string) (string, error) {
	u, _ := url.Parse(server)
	var out struct {
		Board string `json:"board"`
	}
	code, err := postGet(ctx, a.client, u.JoinPath("/v1/hello").String(), &out)
	if err != nil || code != http.StatusOK {
		return "", fmt.Errorf("hello failed")
	}
	return out.Board, nil
}

// Snapshot scans the transcripts and returns what the board should show.
func (a *Agent) Snapshot(now time.Time) ([]session.Session, error) {
	list, err := session.Scan(a.cfg.Root, 24*time.Hour, session.Options{
		Machine: a.host, StaleAfter: 10 * time.Minute, Now: now,
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return filter(a.cfg.Tracker.Apply(list), now), nil
}

// StaleHideAfter: a session silent this long while it was working is
// almost always one whose window was closed mid-turn (Claude Code does not
// always report the end): it leaves the board.
const StaleHideAfter = 30 * time.Minute

// filter keeps what is worth showing: open sessions, anything not idle,
// and sessions finished less than an hour ago; stuck ones for a while.
func filter(in []session.Session, now time.Time) []session.Session {
	out := in[:0:0]
	for _, s := range in {
		if s.Status == session.Idle && !s.Open && now.Sub(s.Modified) > time.Hour {
			continue
		}
		if s.Status == session.Stale && now.Sub(s.Modified) > StaleHideAfter {
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
	if errors.Is(err, tlspin.ErrPin) {
		return tlspin.ErrPin
	}
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
