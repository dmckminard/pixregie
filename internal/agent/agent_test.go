package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dmckminard/pixregie/internal/hooks"
	"github.com/dmckminard/pixregie/internal/session"
	"github.com/dmckminard/pixregie/internal/tlspin"
)

func TestReportsAndStatus(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "-p"), 0o700)
	os.WriteFile(filepath.Join(root, "-p", "s1.jsonl"),
		[]byte(`{"type":"user","cwd":"/x/demo","message":{"content":"hi"}}`+"\n"), 0o600)

	var got atomic.Value
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pxr_good" {
			w.WriteHeader(401)
			return
		}
		var rep struct{ Sessions []session.Session }
		json.NewDecoder(r.Body).Decode(&rep)
		got.Store(rep.Sessions)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	a, err := New(Config{Server: srv.URL, Token: "pxr_good", Root: root, Every: 20 * time.Millisecond, Tracker: hooks.NewTracker("")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go a.Run(ctx)
	select {
	case <-a.Changed():
	case <-ctx.Done():
		t.Fatal("no status")
	}
	st := a.Status()
	if !st.Connected || st.Counts[session.Working] != 1 || st.Worst() != session.Working {
		t.Fatalf("status %+v", st)
	}
	if s := got.Load().([]session.Session); len(s) != 1 || s[0].Project != "demo" {
		t.Fatalf("sent %+v", s)
	}

	bad, _ := New(Config{Server: srv.URL, Token: "pxr_bad", Root: root})
	var last time.Time
	bad.tick(context.Background(), &last)
	if st := bad.Status(); st.Connected || st.Err != ErrUnauthorized.Error() {
		t.Fatalf("status %+v", st)
	}
}

func TestFilterDropsOldIdle(t *testing.T) {
	now := time.Now()
	in := []session.Session{
		{ID: "old", Status: session.Idle, Modified: now.Add(-2 * time.Hour)},
		{ID: "old-open", Status: session.Idle, Open: true, Modified: now.Add(-2 * time.Hour)},
		{ID: "recent", Status: session.Idle, Modified: now.Add(-time.Minute)},
	}
	if out := filter(in, now); len(out) != 2 || out[0].ID != "old-open" {
		t.Fatalf("%+v", out)
	}
}

func TestServeHandler(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "-p"), 0o700)
	os.WriteFile(filepath.Join(root, "-p", "s1.jsonl"),
		[]byte(`{"type":"user","cwd":"/srv/data-sync","message":{"content":"SECRET"}}`+"\n"), 0o600)
	a, _ := New(Config{Root: root, Token: "unused"})
	h := ServeHandler(a, "pxs_right")
	for tok, want := range map[string]int{"": 401, "pxs_wrong": 401, "pxs_right": 200} {
		r := httptest.NewRequest("GET", "/v1/snapshot", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("token %q: %d", tok, w.Code)
		}
		if want == 200 && (!strings.Contains(w.Body.String(), `"project":"data-sync"`) || strings.Contains(w.Body.String(), "SECRET")) {
			t.Fatalf("body %s", w.Body)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/snapshot", nil))
	if w.Code != 405 {
		t.Fatalf("POST: %d", w.Code)
	}
}

func TestPickOnlyOwnBoard(t *testing.T) {
	board := func(id string) *httptest.Server {
		return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"board":"` + id + `"}`))
		}))
	}
	other, mine := board("pxb_other"), board("pxb_mine")
	defer other.Close()
	defer mine.Close()
	a, _ := New(Config{Root: t.TempDir(), Token: "x", Board: "pxb_mine"})
	if got := a.pick(context.Background(), []string{other.URL, mine.URL}); got != mine.URL {
		t.Fatalf("picked %q", got)
	}
	if got := a.pick(context.Background(), []string{other.URL}); got != "" {
		t.Fatalf("must ignore another board, got %q", got)
	}
}

func TestPinLearntThenEnforced(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mine := httptest.NewTLSServer(ok)
	defer mine.Close()
	// httptest servers all share one built-in certificate: give the
	// impostor its own.
	dir := t.TempDir()
	cert, _, err := tlspin.LoadOrCreate(filepath.Join(dir, "c"), filepath.Join(dir, "k"), nil)
	if err != nil {
		t.Fatal(err)
	}
	impostor := httptest.NewUnstartedServer(ok)
	impostor.TLS = tlspin.ServerConfig(cert)
	impostor.StartTLS()
	defer impostor.Close()

	learnt := ""
	a, err := New(Config{Server: mine.URL, Token: "pxr_x", Root: t.TempDir(), Board: "pxb_x",
		OnPin: func(fp string) { learnt = fp }})
	if err != nil {
		t.Fatal(err)
	}
	var last time.Time
	a.tick(context.Background(), &last)
	if want := tlspin.Fingerprint(mine.Certificate().Raw); learnt != want {
		t.Fatalf("pin %q, want %q", learnt, want)
	}

	b, _ := New(Config{Server: impostor.URL, Token: "pxr_x", Root: t.TempDir(), Board: "pxb_x", Pin: learnt})
	b.tick(context.Background(), &last)
	if st := b.Status(); st.Connected || st.Err != tlspin.ErrPin.Error() {
		t.Fatalf("impostor accepted: %+v", st)
	}
}

func TestOldURLUpgraded(t *testing.T) {
	a, err := New(Config{Server: "http://192.168.1.50:7421", Root: t.TempDir()})
	if err != nil || a.cfg.Server != "https://192.168.1.50:7421" {
		t.Fatalf("%v %q", err, a.cfg.Server)
	}
}

func TestFilterDropsLongStale(t *testing.T) {
	now := time.Now()
	in := []session.Session{
		{ID: "dead", Status: session.Stale, Modified: now.Add(-time.Hour)},
		{ID: "dead-open", Status: session.Stale, Open: true, Modified: now.Add(-time.Hour)},
		{ID: "stuck", Status: session.Stale, Modified: now.Add(-15 * time.Minute)},
	}
	if out := filter(in, now); len(out) != 1 || out[0].ID != "stuck" {
		t.Fatalf("%+v", out)
	}
}
