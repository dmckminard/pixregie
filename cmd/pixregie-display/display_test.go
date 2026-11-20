package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
	"github.com/dmckminard/pixregie/internal/session"
)

func TestPullerUsesConfigLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/snapshot" || r.Header.Get("Authorization") != "Bearer pxs_secret_token_value" {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `{"sessions":[{"id":"a","machine":"spoof","project":"data-sync","status":"working"}]}`)
	}))
	defer srv.Close()
	store := collector.NewStore(time.Minute, time.Hour)
	p := newPuller(store, log.New(io.Discard, "", 0))
	p.set([]ServerConfig{{Label: "VPS"}})
	if err := p.pull(context.Background(), ServerConfig{Label: "VPS", URL: srv.URL, Token: "pxs_secret_token_value"}); err != nil {
		t.Fatal(err)
	}
	m := store.Snapshot().Machines
	if len(m) != 1 || m[0].Name != "VPS" || m[0].Sessions[0].Machine != "VPS" {
		t.Fatalf("%+v", m)
	}
	if err := p.pull(context.Background(), ServerConfig{Label: "VPS", URL: srv.URL, Token: "pxs_wrong"}); err == nil {
		t.Fatal("bad token accepted")
	}
}

func TestWatchConfigReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "display.json")
	if err := save(path, Config{Listen: ":0", Agents: map[string]string{"h1": "Mac"}}); err != nil {
		t.Fatal(err)
	}
	got := make(chan Config, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchConfig(ctx, path, log.New(io.Discard, "", 0), func(c Config) { got <- c })
	time.Sleep(50 * time.Millisecond)
	c, _ := load(path)
	delete(c.Agents, "h1")
	save(path, c)
	os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second))
	select {
	case c := <-got:
		if len(c.Agents) != 0 {
			t.Fatalf("%+v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reload")
	}
}

func TestSleepAfter(t *testing.T) {
	if (Config{}).sleepAfter() != 30*time.Minute || (Config{SleepAfter: "0"}).sleepAfter() != 0 || (Config{SleepAfter: "2h"}).sleepAfter() != 2*time.Hour {
		t.Fatal("sleep_after parsing")
	}
}

func TestLatestActivity(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	st := collector.State{Machines: []collector.Machine{
		{Online: true, Sessions: []session.Session{{Modified: old}, {Modified: old.Add(time.Minute)}}},
		{Online: false, Sessions: []session.Session{{Modified: old.Add(time.Hour)}}},
	}}
	if got := latestActivity(st); !got.Equal(old.Add(time.Minute)) {
		t.Fatalf("got %v", got)
	}
}
