package hooks

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Port is where the agent listens for hook events, on 127.0.0.1 only.
const Port = 7422

// Bind takes the hook port on 127.0.0.1. Holding it for the life of the
// process also makes it a single-instance lock.
func Bind() (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(Port)))
}

// Listen binds and serves POST /hook until the listener fails.
// The endpoint only ever changes what the board shows; it cannot reach
// anything else.
func Listen(t *Tracker) error {
	ln, err := Bind()
	if err != nil {
		return err
	}
	return Serve(ln, t)
}

// Serve serves the hook endpoint on an already bound listener.
func Serve(ln net.Listener, t *Tracker) error {
	srv := &http.Server{
		Handler:           Handler(t),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      2 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Handler is the hook endpoint, exported for tests.
func Handler(t *Tracker) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hook", func(w http.ResponseWriter, r *http.Request) {
		// Browsers cannot send this without a CORS preflight we never
		// answer, so a web page cannot forge events.
		if r.Header.Get("Origin") != "" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var e Event
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&e); err != nil {
			http.Error(w, "bad event", http.StatusBadRequest)
			return
		}
		t.Handle(e)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}
