package session

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
		sub := subagentActivity(filepath.Join(filepath.Dir(f), id))
		s, err := cache.parse(f, fi, sub, opt)
		if err != nil {
			continue // unreadable file: skip, never fail the whole scan
		}
		out = append(out, s)
	}
	cache.keep(files)
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// cache keeps the last parse of each transcript: with a window of days,
// most files have not changed since the previous scan (every 3 s).
var cache = &parseCache{m: map[string]cached{}}

type parseCache struct {
	mu sync.Mutex
	m  map[string]cached
}

type cached struct {
	size     int64
	mod, sub time.Time
	raw      Session // before the time-dependent stale rule
}

func (c *parseCache) parse(path string, fi os.FileInfo, sub time.Time, opt Options) (Session, error) {
	c.mu.Lock()
	e, ok := c.m[path]
	c.mu.Unlock()
	if !ok || e.size != fi.Size() || !e.mod.Equal(fi.ModTime()) || !e.sub.Equal(sub) {
		raw, err := parseFile(path, Options{Machine: opt.Machine}, sub)
		if err != nil {
			return Session{}, err
		}
		e = cached{size: fi.Size(), mod: fi.ModTime(), sub: sub, raw: raw}
		c.mu.Lock()
		c.m[path] = e
		c.mu.Unlock()
	}
	s := e.raw
	s.Machine = opt.Machine
	applyStale(&s, opt)
	return s, nil
}

func (c *parseCache) keep(files []string) {
	live := make(map[string]bool, len(files))
	for _, f := range files {
		live[f] = true
	}
	c.mu.Lock()
	for p := range c.m {
		if !live[p] {
			delete(c.m, p)
		}
	}
	c.mu.Unlock()
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
