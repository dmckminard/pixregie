package session

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultRoot returns ~/.claude/projects, or $CLAUDE_CONFIG_DIR/projects
// when Claude Code is configured to use another directory.
func DefaultRoot() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// Scan lists sessions under root whose transcript changed within `within`.
// Layout: <root>/<encoded-project-dir>/<session-id>.jsonl. Subagents write
// to <session-id>/subagents/*.jsonl: they are not sessions of their own, but
// their writes count as activity of the parent, which is silent while it
// waits for them (otherwise a long subagent would look stuck).
func Scan(root string, within time.Duration, opt Options) ([]Session, error) {
	files, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		if strings.HasPrefix(id, "agent-") {
			continue // subagent transcript (older layout), not a session
		}
		if within > 0 && opt.Now.Sub(fi.ModTime()) > within {
			continue
		}
		s, err := parseFile(f, opt, subagentActivity(filepath.Join(filepath.Dir(f), id)))
		if err != nil {
			continue // unreadable file: skip, never fail the whole scan
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// subagentActivity is the newest write among a session's subagent
// transcripts (zero if none).
func subagentActivity(dir string) time.Time {
	var t time.Time
	files, _ := filepath.Glob(filepath.Join(dir, "*", "*.jsonl"))
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}
