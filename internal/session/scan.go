package session

import (
	"os"
	"path/filepath"
	"sort"
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
// Layout: <root>/<encoded-project-dir>/<session-id>.jsonl. Subagent
// transcripts live in deeper folders and are intentionally ignored.
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
		if within > 0 && opt.Now.Sub(fi.ModTime()) > within {
			continue
		}
		s, err := ParseFile(f, opt)
		if err != nil {
			continue // unreadable file: skip, never fail the whole scan
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}
