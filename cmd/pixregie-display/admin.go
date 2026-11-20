package main

import (
	"encoding/json"
	"errors"
	"net/http"
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

func (a *admin) settings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShowTitles *bool   `json:"show_titles"`
		SleepAfter *string `json:"sleep_after"`
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
