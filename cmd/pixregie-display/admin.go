package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
)

// admin backs the settings and machines panel of the debug page. Every
// change goes through the config file, so the CLI and the page always
// agree, and is applied at once.
type admin struct {
	path    string
	store   *collector.Store
	apply   func(Config)
	upgrade *upgrader
}

type machineView struct {
	Name     string    `json:"name"`
	Kind     string    `json:"kind"` // machine | server
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"last_seen,omitzero"`
}

func (a *admin) get(w http.ResponseWriter, r *http.Request) {
	cfg, err := load(a.path)
	if err != nil {
		http.Error(w, "config unreadable", http.StatusInternalServerError)
		return
	}
	seen := map[string]collector.Machine{}
	for _, m := range a.store.Snapshot().Machines {
		seen[m.Name] = m
	}
	kinds := map[string]string{}
	for _, l := range cfg.Agents {
		kinds[l] = "machine"
	}
	for _, s := range cfg.Servers {
		kinds[s.Label] = "server"
	}
	var list []machineView
	for name, kind := range kinds {
		m := seen[name]
		list = append(list, machineView{Name: name, Kind: kind, Online: m.Online, LastSeen: m.LastSeen})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	sleep := cfg.SleepAfter
	if sleep == "" {
		sleep = "30m"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{
		"show_titles": cfg.showTitles(),
		"sleep_after": sleep,
		"idle_hide":   orStr(cfg.IdleHide, "1h"),
		"stale_hide":  orStr(cfg.StaleHide, "1h"),
		"machines":    list,
		"upgrade":     a.upgrade.view(),
	})
}

// change loads the config, lets f modify it, saves and applies it.
func (a *admin) change(w http.ResponseWriter, r *http.Request, body any, f func(*Config) error) {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cfg, err := load(a.path)
	if err == nil {
		err = f(&cfg)
	}
	if err == nil {
		err = save(a.path, cfg)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.apply(cfg)
	w.WriteHeader(http.StatusNoContent)
}

var sleepChoices = map[string]bool{"0": true, "10m": true, "30m": true, "1h": true, "2h": true, "4h": true}

// Delays offered on the page for finished and stuck sessions; machines
// send a week of sessions, so 168h is "always, within a week".
var hideChoices = map[string]bool{"30m": true, "1h": true, "4h": true, "12h": true, "24h": true, "168h": true}

func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// maxPins keeps the config small.
const maxPins = 50

var sessionIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// pin pins or unpins one session.
func (a *admin) pin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Pinned bool   `json:"pinned"`
	}
	a.change(w, r, &req, func(c *Config) error {
		if !sessionIDRE.MatchString(req.ID) {
			return errors.New("bad session id")
		}
		kept := c.Pinned[:0:0]
		for _, id := range c.Pinned {
			if id != req.ID {
				kept = append(kept, id)
			}
		}
		if req.Pinned {
			if len(kept) >= maxPins {
				return errors.New("50 sessions épinglées au plus")
			}
			kept = append(kept, req.ID)
		}
		c.Pinned = kept
		return nil
	})
}

func (a *admin) settings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShowTitles *bool   `json:"show_titles"`
		SleepAfter *string `json:"sleep_after"`
		IdleHide   *string `json:"idle_hide"`
		StaleHide  *string `json:"stale_hide"`
	}
	a.change(w, r, &req, func(c *Config) error {
		if req.ShowTitles != nil {
			v := *req.ShowTitles
			c.ShowTitles = &v
		}
		if req.SleepAfter != nil {
			if !sleepChoices[*req.SleepAfter] {
				return errors.New("unsupported sleep delay")
			}
			c.SleepAfter = *req.SleepAfter
		}
		for _, h := range []struct {
			v   *string
			dst *string
		}{{req.IdleHide, &c.IdleHide}, {req.StaleHide, &c.StaleHide}} {
			if h.v != nil {
				if !hideChoices[*h.v] {
					return errors.New("unsupported delay")
				}
				*h.dst = *h.v
			}
		}
		return nil
	})
}

func (a *admin) rename(w http.ResponseWriter, r *http.Request) {
	var req struct{ Old, New string }
	a.change(w, r, &req, func(c *Config) error {
		nu := strings.TrimSpace(req.New)
		if !labelRE.MatchString(nu) {
			return errors.New("name: 1-32 letters, digits, space, . _ -")
		}
		n := 0
		for h, l := range c.Agents {
			if l == req.Old {
				c.Agents[h] = nu
				n++
			}
		}
		for i := range c.Servers {
			if c.Servers[i].Label == req.Old {
				c.Servers[i].Label = nu
				n++
			}
		}
		if n == 0 {
			return errors.New("no such machine")
		}
		return nil
	})
}

// revoke removes a paired machine's tokens. Servers are removed from the
// Pi's shell, which also stops their tunnel.
func (a *admin) revoke(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	a.change(w, r, &req, func(c *Config) error {
		n := 0
		for h, l := range c.Agents {
			if l == req.Name {
				delete(c.Agents, h)
				n++
			}
		}
		if n == 0 {
			return errors.New("no such machine (servers: sudo bash install-pi.sh --remove-server NAME)")
		}
		return nil
	})
}
