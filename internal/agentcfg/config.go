// Package agentcfg holds the agent's small configuration and its token.
//
// The token never lives in a plain file on macOS (Keychain) or Windows
// (encrypted with DPAPI, bound to the user account). Elsewhere it is a
// 0600 file, the usual Unix practice for a service secret.
package agentcfg

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Config is stored as JSON in the user's config folder.
type Config struct {
	// Server is the display URL. Empty: found with mDNS at each start.
	Server string `json:"server,omitempty"`
	// Board is the paired display's identifier: reports never go to
	// another Pixregie board on the same network.
	Board string `json:"board,omitempty"`
	// AutostartSet records that launch-at-login was turned on once, so a
	// user who later turns it off is not overridden.
	AutostartSet bool `json:"autostart_set,omitempty"`
}

// Dir is ~/Library/Application Support/Pixregie, %AppData%\Pixregie or
// ~/.config/pixregie.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	name := "Pixregie"
	if !strings.Contains(base, "Library") && !strings.Contains(base, "AppData") {
		name = "pixregie"
	}
	return filepath.Join(base, name), nil
}

func path(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}

func Load() (Config, error) {
	var c Config
	p, err := path("config.json")
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func Save(c Config) error {
	p, err := path("config.json")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, append(b, '\n'), 0o600)
}

// StatePath is where the hook tracker keeps open-session state.
func StatePath() (string, error) { return path("sessions.json") }

// ValidToken checks the shape of a token from pixregie-display add-agent.
func ValidToken(t string) bool {
	if !strings.HasPrefix(t, "pxr_") || len(t) < 20 || len(t) > 100 {
		return false
	}
	for _, r := range t[4:] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
