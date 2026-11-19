package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
)

// puller fetches snapshots from servers (agents in `serve` mode), reached
// through SSH tunnels that end on 127.0.0.1 here. Servers cannot reach the
// Pi, so the Pi asks them.
type puller struct {
	store  *collector.Store
	log    *log.Logger
	client *http.Client

	mu      sync.Mutex
	servers []ServerConfig
	failing map[string]string // label -> last error, to log changes only
}

func newPuller(store *collector.Store, logger *log.Logger) *puller {
	return &puller{
		store:   store,
		log:     logger,
		client:  &http.Client{Timeout: 3 * time.Second},
		failing: map[string]string{},
	}
}

func (p *puller) set(servers []ServerConfig) {
	p.mu.Lock()
	p.servers = append([]ServerConfig(nil), servers...)
	p.mu.Unlock()
}

func (p *puller) run(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		p.mu.Lock()
		servers := p.servers
		p.mu.Unlock()
		var wg sync.WaitGroup
		for _, s := range servers {
			wg.Add(1)
			go func(s ServerConfig) {
				defer wg.Done()
				err := p.pull(ctx, s)
				p.note(s.Label, err)
			}(s)
		}
		wg.Wait()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *puller) pull(ctx context.Context, s ServerConfig) error {
	u, err := url.Parse(s.URL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.JoinPath("/v1/snapshot").String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("unreachable (tunnel down?)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("answered %s", resp.Status)
	}
	var rep collector.Report
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 256<<10)).Decode(&rep); err != nil {
		return err
	}
	// The label comes from the Pi's own config, never from the server.
	// Skip if the server was removed while this request was in flight.
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, cur := range p.servers {
		if cur.Label == s.Label {
			p.store.Put(s.Label, rep)
			return nil
		}
	}
	return nil
}

func (p *puller) note(label string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	p.mu.Lock()
	prev, seen := p.failing[label]
	p.failing[label] = msg
	p.mu.Unlock()
	if seen && prev == msg {
		return
	}
	if err != nil {
		p.log.Printf("server %q: %v", label, err)
	} else {
		p.log.Printf("server %q: reporting", label)
	}
}
