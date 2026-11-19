package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dmckminard/pixregie/internal/hooks"
	"github.com/dmckminard/pixregie/internal/session"
)

func TestReportsAndStatus(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "-p"), 0o700)
	os.WriteFile(filepath.Join(root, "-p", "s1.jsonl"),
		[]byte(`{"type":"user","cwd":"/x/demo","message":{"content":"hi"}}`+"\n"), 0o600)

	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
