package main

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dmckminard/pixregie/internal/release"
)

// upgrader backs the "update the Pi" button. The service itself has no
// right to replace its files: it only drops a request file in its config
// directory. A root unit installed by install-pi.sh (pixregie-upgrade.path)
// sees it, downloads the latest install-pi.sh, checks it against the signed
// SHA256SUMS and runs it. The request's content is never read, so the
// button can only ever install the latest signed release.
type upgrader struct {
	request string // written by us, removed by the root unit
	status  string // written by the root unit only

	mu     sync.Mutex
	latest string
}

// Paths shared with install-pi.sh.
const (
	upgradeRequest = "upgrade.request"              // in the config directory
	upgradeStatus  = "/run/pixregie-upgrade/status" // root-owned directory
)

func (u *upgrader) watch(ctx context.Context) {
	c := &http.Client{Timeout: 30 * time.Second}
	for {
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		tag, err := release.Latest(cctx, c)
		cancel()
		if err == nil {
			u.mu.Lock()
			u.latest = tag
			u.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(6 * time.Hour):
		}
	}
}

func (u *upgrader) available() (string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.latest, release.Newer(u.latest, version)
}

type upgradeView struct {
	Version   string `json:"version"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	State     string `json:"state,omitempty"` // requested | running | ok | error
	At        string `json:"at,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (u *upgrader) view() upgradeView {
	latest, avail := u.available()
	v := upgradeView{Version: version, Latest: latest, Available: avail}
	// "state<TAB>time<TAB>message", one line, written by the root unit.
	if b, err := os.ReadFile(u.status); err == nil && len(b) < 1024 {
		p := strings.SplitN(strings.TrimSpace(string(b)), "\t", 3)
		switch p[0] {
		case "running", "ok", "error":
			v.State = p[0]
			if len(p) > 1 {
				v.At = p[1]
			}
			if len(p) > 2 {
				v.Message = p[2]
			}
		}
	}
	if _, err := os.Stat(u.request); err == nil {
		v.State = "requested"
	}
	return v
}

func (u *upgrader) post(w http.ResponseWriter, r *http.Request) {
	if _, avail := u.available(); !avail {
		http.Error(w, "le Pi est déjà à jour", http.StatusConflict)
		return
	}
	f, err := os.OpenFile(u.request, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		http.Error(w, "mise à jour déjà demandée", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "impossible de demander la mise à jour", http.StatusInternalServerError)
		return
	}
	f.Close()
	w.WriteHeader(http.StatusAccepted)
}
