package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// marker identifies the entries Pixregie owns in settings.json, so they can
// be updated or removed without touching the user's own hooks.
const marker = "# pixregie-hook"

// Events the agent needs. Each fires once per turn at most, never per tool
// call, and runs a local curl: nothing reaches the model.
var Events = []string{
	"SessionStart", "SessionEnd", "Notification",
	"UserPromptSubmit", "Stop", "StopFailure", "PermissionRequest",
}

// background events run asynchronously: Claude Code does not even wait
// for curl. SessionStart/End stay synchronous so they are not lost when a
// session exits right away (they are bounded by curl's 1 s anyway).
var background = map[string]bool{
	"UserPromptSubmit": true, "Stop": true, "StopFailure": true, "PermissionRequest": true,
}

// Command is the hook command: forward the payload, never block or fail
// Claude Code (1 s timeout, errors ignored). Runs in bash on macOS, Linux
// and Windows (Claude Code uses Git Bash there).
func Command() string {
	return fmt.Sprintf("curl -s -m 1 -o /dev/null --data-binary @- http://127.0.0.1:%d/hook || true %s", Port, marker)
}

// SettingsPath returns Claude Code's user settings file.
func SettingsPath() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// Install adds (or refreshes) the Pixregie hooks. Returns false if nothing
// changed. A malformed settings file is left untouched.
func Install(path string) (bool, error) { return edit(path, true) }

// Uninstall removes the Pixregie hooks and nothing else.
func Uninstall(path string) (bool, error) { return edit(path, false) }

// Installed reports whether every Pixregie hook is present.
func Installed(path string) bool {
	root, _, err := load(path)
	if err != nil {
		return false
	}
	hooks, _ := root["hooks"].(map[string]any)
	for _, ev := range Events {
		if !hasOurs(hooks[ev]) {
			return false
		}
	}
	return true
}

func load(path string) (map[string]any, os.FileMode, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, 0o600, nil
	}
	if err != nil {
		return nil, 0, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	root := map[string]any{}
	if len(bytes.TrimSpace(b)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber() // keep numbers exactly as written
		if err := dec.Decode(&root); err != nil {
			return nil, 0, fmt.Errorf("%s is not valid JSON, not touching it: %w", path, err)
		}
	}
	return root, fi.Mode().Perm(), nil
}

func isOurs(group any) bool {
	g, ok := group.(map[string]any)
	if !ok {
		return false
	}
	list, _ := g["hooks"].([]any)
	for _, h := range list {
		if m, ok := h.(map[string]any); ok {
			if c, _ := m["command"].(string); strings.Contains(c, marker) {
				return true
			}
		}
	}
	return false
}

func hasOurs(v any) bool {
	list, _ := v.([]any)
	for _, g := range list {
		if isOurs(g) {
			return true
		}
	}
	return false
}

func edit(path string, add bool) (bool, error) {
	root, mode, err := load(path)
	if err != nil {
		return false, err
	}
	before, _ := json.Marshal(root)

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, ev := range Events {
		var kept []any
		list, _ := hooks[ev].([]any)
		for _, g := range list {
			if !isOurs(g) {
				kept = append(kept, g)
			}
		}
		if add {
			h := map[string]any{"type": "command", "command": Command(), "timeout": 5}
			if background[ev] {
				h["async"] = true
			}
			kept = append(kept, map[string]any{"hooks": []any{h}})
		}
		if len(kept) == 0 {
			delete(hooks, ev)
		} else {
			hooks[ev] = kept
		}
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}

	after, _ := json.Marshal(root)
	if bytes.Equal(before, after) {
		return false, nil
	}
	// One-time backup of the user's original file.
	if orig, err := os.ReadFile(path); err == nil {
		bak := path + ".pixregie-backup"
		if _, err := os.Stat(bak); errors.Is(err, os.ErrNotExist) {
			os.WriteFile(bak, orig, mode)
		}
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	tmp := path + ".pixregie-tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), mode); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}
