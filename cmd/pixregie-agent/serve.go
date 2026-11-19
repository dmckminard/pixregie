package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dmckminard/pixregie/internal/agent"
	"github.com/dmckminard/pixregie/internal/hooks"
)

// serve is the server mode (VPS): instead of pushing to the Pi, which it
// cannot reach, the agent answers the Pi's requests coming through an SSH
// tunnel. It only listens on loopback.
//
// Token: $CREDENTIALS_DIRECTORY/server-token (systemd LoadCredential) or
// -token-file. State: $STATE_DIRECTORY/sessions.json (systemd
// StateDirectory) or the user config folder.
func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:7423", "loopback address the Pi's tunnel connects to")
	tokenFile := fs.String("token-file", "", "file holding the server token (default: systemd credential)")
	root := fs.String("root", "", "transcripts directory (default ~/.claude/projects)")
	fs.Parse(args)

	host, _, err := net.SplitHostPort(*listen)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		fatal(errors.New("-listen must be a loopback address: the Pi reaches it through SSH"))
	}
	if *tokenFile == "" {
		if d := os.Getenv("CREDENTIALS_DIRECTORY"); d != "" {
			*tokenFile = filepath.Join(d, "server-token")
		}
	}
	if *tokenFile == "" {
		fatal(errors.New("no token: use -token-file or the systemd credential server-token"))
	}
	b, err := os.ReadFile(*tokenFile)
	if err != nil {
		fatal(err)
	}
	token := strings.TrimSpace(string(b))
	if !strings.HasPrefix(token, "pxs_") || len(token) < 20 {
		fatal(errors.New("invalid server token"))
	}

	statePath := ""
	if d := os.Getenv("STATE_DIRECTORY"); d != "" {
		statePath = filepath.Join(d, "sessions.json")
	}
	tracker := hooks.NewTracker(statePath)
	go func() {
		if err := hooks.Listen(tracker); err != nil {
			log.Printf("hook listener disabled (%v)", err)
		}
	}()
	go func() {
		for range time.Tick(24 * time.Hour) {
			tracker.Prune()
		}
	}()

	a, err := agent.New(agent.Config{Root: *root, Tracker: tracker, Token: "unused"})
	if err != nil {
		fatal(err)
	}
	srv := &http.Server{
		Addr:              *listen,
		Handler:           agent.ServeHandler(a, token),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	log.Printf("pixregie-agent %s serving snapshots on %s", version, *listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal(err)
	}
}
