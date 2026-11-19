package agent

import (
	"context"
	"log"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
)

func TestPairAgainstRealCollector(t *testing.T) {
	srv := &collector.Server{
		Store:  collector.NewStore(time.Minute, time.Hour),
		Auth:   collector.Auth{AgentTokens: map[string]string{}},
		Pairer: collector.NewPairer(),
		Log:    log.New(testWriter{t}, "", 0),
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	asked := 0
	tok, err := Pair(context.Background(), ts.URL, "Mac", func(retry bool) (string, bool) {
		asked++
		if !retry {
			return "000000-wrong", true
		}
		req, _ := srv.Pairer.Pending() // what the screen shows
		return req.Code, true
	})
	if err != nil || asked != 2 || len(tok) < 20 {
		t.Fatalf("tok=%q err=%v asked=%d", tok, err, asked)
	}

	_, err = Pair(context.Background(), ts.URL, "PC", func(bool) (string, bool) { return "", false })
	if err != ErrPairCancelled {
		t.Fatalf("got %v", err)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Log(string(p)); return len(p), nil }
