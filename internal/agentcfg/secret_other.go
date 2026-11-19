//go:build !darwin && !windows

package agentcfg

import (
	"os"
	"path/filepath"
	"strings"
)

// Elsewhere (Linux servers) the token is a 0600 file, or PIXREGIE_TOKEN.

func tokenFile() (string, error) { return path("token") }

func GetToken() (string, error) {
	if t := strings.TrimSpace(os.Getenv("PIXREGIE_TOKEN")); t != "" {
		return t, nil
	}
	p, err := tokenFile()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", ErrNoToken
	}
	return strings.TrimSpace(string(b)), nil
}

func SetToken(t string) error {
	p, err := tokenFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(t+"\n"), 0o600)
}

func DeleteToken() error {
	p, err := tokenFile()
	if err != nil {
		return err
	}
	return os.Remove(p)
}
