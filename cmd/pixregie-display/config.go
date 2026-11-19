package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

const defaultConfig = "/etc/pixregie/display.json"

// Config is stored with mode 0600, owned by the service user. Agent tokens
// are stored hashed; server tokens are stored as is because the Pi has to
// present them.
type Config struct {
	Listen        string            `json:"listen"`
	Agents        map[string]string `json:"agents"`         // sha256(token) -> machine label
	DebugPassword string            `json:"debug_password"` // pbkdf2 hash; empty = page off
	Servers       []ServerConfig    `json:"servers,omitempty"`
	// SleepAfter turns the screen off after this long without activity
	// ("30m" by default, "0" never).
	SleepAfter string `json:"sleep_after,omitempty"`
	// ShowTitles shows session titles (derived from conversations) under
	// each desk. Default true; set false for a screen others can see.
	ShowTitles *bool `json:"show_titles,omitempty"`
	// BoardID is this display's public identifier, created at first start.
	BoardID string `json:"board_id,omitempty"`
}

func (c Config) showTitles() bool { return c.ShowTitles == nil || *c.ShowTitles }

// ServerConfig is a remote agent the Pi pulls from, normally through a
// local SSH tunnel (URL on 127.0.0.1).
type ServerConfig struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

func (c Config) sleepAfter() time.Duration {
	if c.SleepAfter == "" {
		return 30 * time.Minute
	}
	d, err := time.ParseDuration(c.SleepAfter)
	if err != nil || d < 0 {
		return 30 * time.Minute
	}
	return d
}

// labels is every machine name the config allows on the screen.
func (c Config) labels() map[string]bool {
	m := map[string]bool{}
	for _, l := range c.Agents {
		m[l] = true
	}
	for _, s := range c.Servers {
		m[s.Label] = true
	}
	return m
}

func load(path string) (Config, error) {
	cfg := Config{Listen: ":7421", Agents: map[string]string{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]string{}
	}
	return cfg, nil
}

// save writes atomically with mode 0600.
func save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".display-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// watchConfig reloads the file when it changes, so CLI edits (new or
// revoked machine, new server) apply without restarting the service.
func watchConfig(ctx context.Context, path string, logger *log.Logger, apply func(Config)) {
	var last time.Time
	if fi, err := os.Stat(path); err == nil {
		last = fi.ModTime()
	}
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		fi, err := os.Stat(path)
		if err != nil || !fi.ModTime().After(last) {
			continue
		}
		last = fi.ModTime()
		cfg, err := load(path)
		if err != nil {
			logger.Printf("config reload: %v", err)
			continue
		}
		apply(cfg)
		logger.Printf("config reloaded (%d agents, %d servers)", len(cfg.Agents), len(cfg.Servers))
	}
}
