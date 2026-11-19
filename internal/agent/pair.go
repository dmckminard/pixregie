package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

var (
	// ErrPairCancelled: the user closed the code dialog.
	ErrPairCancelled = errors.New("association annulée")
	// ErrPairExpired: too slow, or too many wrong codes. Start again.
	ErrPairExpired = errors.New("code expiré ou trop d'essais, recommence")
	// ErrPiTooOld: the display predates pairing by code.
	ErrPiTooOld = errors.New("le Pi doit être mis à jour (sur le Pi : sudo bash install-pi.sh)")
)

// Paired is the result of a pairing.
type Paired struct {
	Token string
	Board string // the display's identifier
}

// Pair asks the display for a pairing code (shown on its screen), asks the
// user for it through askCode, and returns the new token.
// askCode gets retry=true after a wrong code.
func Pair(ctx context.Context, server, label string, askCode func(retry bool) (string, bool)) (Paired, error) {
	c := &http.Client{Timeout: 10 * time.Second}
	base, err := url.Parse(server)
	if err != nil {
		return Paired{}, err
	}
	var start struct {
		ID string `json:"id"`
	}
	code, err := postJSON(ctx, c, base.JoinPath("/v1/pair/start").String(), map[string]string{"label": label}, &start)
	if err != nil {
		return Paired{}, err
	}
	if code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
		return Paired{}, ErrPiTooOld
	}
	if code == http.StatusTooManyRequests {
		return Paired{}, errors.New("trop de demandes d'association, réessaie dans une minute")
	}
	if code != http.StatusOK || start.ID == "" {
		return Paired{}, fmt.Errorf("le Pi a refusé l'association (%d)", code)
	}

	retry := false
	for {
		typed, ok := askCode(retry)
		if !ok {
			return Paired{}, ErrPairCancelled
		}
		var done struct {
			Token string `json:"token"`
			Board string `json:"board"`
		}
		code, err := postJSON(ctx, c, base.JoinPath("/v1/pair/finish").String(),
			map[string]string{"id": start.ID, "code": typed}, &done)
		if err != nil {
			return Paired{}, err
		}
		switch code {
		case http.StatusOK:
			if done.Token == "" {
				return Paired{}, errors.New("réponse invalide du Pi")
			}
			return Paired{Token: done.Token, Board: done.Board}, nil
		case http.StatusForbidden:
			retry = true
		default:
			return Paired{}, ErrPairExpired
		}
	}
}

func postJSON(ctx context.Context, c *http.Client, u string, in, out any) (int, error) {
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return 0, errors.New("Pi injoignable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && out != nil {
		if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4<<10)).Decode(out); err != nil {
			return 0, err
		}
	}
	return resp.StatusCode, nil
}

func postGet(ctx context.Context, c *http.Client, u string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && out != nil {
		if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4<<10)).Decode(out); err != nil {
			return 0, err
		}
	}
	return resp.StatusCode, nil
}
