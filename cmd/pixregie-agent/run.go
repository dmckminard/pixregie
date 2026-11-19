package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
	"github.com/dmckminard/pixregie/internal/session"
)

// run scans and pushes a snapshot to the display at a fixed interval.
// The token is read from the environment, never from a flag, so it does not
// show up in the process list.
func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	server := fs.String("server", "", "display URL, e.g. http://pixregie.local:7421")
	every := fs.Duration("every", 3*time.Second, "report interval")
	within := fs.Duration("within", 24*time.Hour, "only sessions active within this duration")
	stale := fs.Duration("stale", 10*time.Minute, "working sessions silent this long are shown as stale")
	root := fs.String("root", "", "transcripts directory (default ~/.claude/projects)")
	fs.Parse(args)

	token := strings.TrimSpace(os.Getenv("PIXREGIE_TOKEN"))
	if token == "" {
		fatal(errors.New("set PIXREGIE_TOKEN (from `pixregie-display add-agent`)"))
	}
	u, err := url.Parse(*server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		fatal(errors.New("-server must be an http(s) URL"))
	}
	endpoint := u.JoinPath("/v1/report").String()
	if *root == "" {
		if *root, err = session.DefaultRoot(); err != nil {
			fatal(err)
		}
	}

	client := &http.Client{Timeout: 5 * time.Second}
	host, _ := os.Hostname()
	lastErr := "init"
	for {
		sessions, err := session.Scan(*root, *within, session.Options{
			Machine: host, StaleAfter: *stale, Now: time.Now(),
		})
		if err == nil {
			err = push(client, endpoint, token, sessions)
		}
		// Log transitions only, not every failed attempt.
		if msg := errString(err); msg != lastErr {
			if err != nil {
				log.Printf("report failed: %v", err)
			} else {
				log.Printf("reporting to %s", u.Host)
			}
			lastErr = msg
		}
		time.Sleep(*every)
	}
}

func push(c *http.Client, endpoint, token string, sessions []session.Session) error {
	if sessions == nil {
		sessions = []session.Session{}
	}
	body, err := json.Marshal(collector.Report{Sessions: sessions})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("display answered %s", resp.Status)
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
