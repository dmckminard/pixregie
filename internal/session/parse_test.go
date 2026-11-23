package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Synthetic transcript lines, shaped like Claude Code's but with no real data.
const (
	lPrompt     = `{"type":"user","cwd":"/home/dev/demo-app","gitBranch":"main","entrypoint":"cli","timestamp":"2026-01-01T10:00:00Z","message":{"role":"user","content":"SECRET-PROMPT do the thing"}}`
	lPromptBlk  = `{"type":"user","cwd":"/home/dev/demo-app","timestamp":"2026-01-01T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"SECRET-PROMPT"}]}}`
	lThinking   = `{"type":"assistant","timestamp":"2026-01-01T10:00:01Z","message":{"stop_reason":"tool_use","content":[{"type":"thinking","thinking":"SECRET-THOUGHT"}]}}`
	lToolUse    = `{"type":"assistant","timestamp":"2026-01-01T10:00:02Z","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"SECRET-CMD"}}]}}`
	lToolResult = `{"type":"user","timestamp":"2026-01-01T10:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"SECRET-OUTPUT"}]}}`
	lEndTurn    = `{"type":"assistant","timestamp":"2026-01-01T10:00:04Z","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"SECRET-ANSWER"}]}}`
	lInterrupt  = `{"type":"user","timestamp":"2026-01-01T10:00:05Z","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`
	lSidechain  = `{"type":"assistant","isSidechain":true,"timestamp":"2026-01-01T10:00:06Z","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"s1","name":"Grep"}]}}`
	lMeta       = `{"type":"user","isMeta":true,"timestamp":"2026-01-01T10:00:07Z","message":{"role":"user","content":"meta"}}`
	lOther      = `{"type":"last-prompt","sessionId":"x"}`
	lGarbage    = `{"type":"assistant","message":{"stop_re`
)

// Just after the fixtures' entries (10:00:00–10:00:07), as in real life.
var now = time.Date(2026, 1, 1, 10, 0, 10, 0, time.UTC)

func write(t *testing.T, mtime time.Time, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "abc-123.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return p
}

func parseT(t *testing.T, mtime time.Time, lines ...string) Session {
	t.Helper()
	s, err := ParseFile(write(t, mtime, lines...), Options{Machine: "m", StaleAfter: 10 * time.Minute, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStatus(t *testing.T) {
	fresh := now.Add(-5 * time.Second)
	cases := []struct {
		name   string
		lines  []string
		status Status
		tool   string
	}{
		{"empty", nil, Idle, ""},
		{"prompt only", []string{lPrompt}, Working, ""},
		{"prompt as blocks", []string{lPromptBlk}, Working, ""},
		{"thinking", []string{lPrompt, lThinking}, Working, ""},
		{"tool running", []string{lPrompt, lThinking, lToolUse}, Working, "Bash"},
		{"after tool result", []string{lPrompt, lToolUse, lToolResult}, Working, ""},
		{"turn finished", []string{lPrompt, lToolUse, lToolResult, lEndTurn}, Idle, ""},
		{"interrupted mid-tool", []string{lPrompt, lToolUse, lInterrupt}, Idle, ""},
		{"sidechain ignored", []string{lPrompt, lEndTurn, lSidechain}, Idle, ""},
		{"meta ignored", []string{lPrompt, lEndTurn, lMeta}, Idle, ""},
		{"unknown and garbage lines", []string{lPrompt, lOther, lEndTurn, lGarbage}, Idle, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := parseT(t, fresh, c.lines...)
			if s.Status != c.status || s.Tool != c.tool {
				t.Fatalf("got %s/%q, want %s/%q", s.Status, s.Tool, c.status, c.tool)
			}
		})
	}
}

func TestMetadata(t *testing.T) {
	s := parseT(t, now, lPrompt, lEndTurn)
	if s.ID != "abc-123" || s.Project != "demo-app" || s.Branch != "main" || s.Entrypoint != "cli" || s.Machine != "m" {
		t.Fatalf("bad metadata: %+v", s)
	}
	if !s.LastEvent.Equal(time.Date(2026, 1, 1, 10, 0, 4, 0, time.UTC)) {
		t.Fatalf("last event %v", s.LastEvent)
	}
}

func TestWindowsPath(t *testing.T) {
	if got := projectName(`C:\Users\me\dev\my-app\`, "x"); got != "my-app" {
		t.Fatal(got)
	}
}

func TestStale(t *testing.T) {
	s := parseT(t, now.Add(-30*time.Minute), lPrompt, lToolUse)
	if s.Status != Stale || s.Tool != "" {
		t.Fatalf("got %s/%q", s.Status, s.Tool)
	}
	// An idle session never becomes stale.
	s = parseT(t, now.Add(-30*time.Minute), lPrompt, lEndTurn)
	if s.Status != Idle {
		t.Fatalf("got %s", s.Status)
	}
}

// The record that leaves the machine must never contain transcript content.
func TestNoContentLeaks(t *testing.T) {
	s := parseT(t, now, lPrompt, lThinking, lToolUse, lToolResult, lEndTurn)
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "SECRET") {
		t.Fatalf("content leaked: %s", b)
	}
}

// Only the tail is read: a huge transcript with a line cut at the boundary
// must still parse to the right state.
func TestLargeTail(t *testing.T) {
	filler := `{"type":"user","message":{"content":"` + strings.Repeat("x", 4000) + `"}}`
	lines := []string{lPrompt}
	for i := 0; i < 300; i++ { // ~1.2 MB, more than tailBytes
		lines = append(lines, lToolUse, filler)
	}
	lines = append(lines, lPrompt, lToolUse)
	s := parseT(t, now, lines...)
	if s.Status != Working || s.Tool != "Bash" {
		t.Fatalf("got %s/%q", s.Status, s.Tool)
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	mk := func(proj, id string, mtime time.Time, lines ...string) {
		d := filepath.Join(root, proj)
		os.MkdirAll(d, 0o700)
		p := filepath.Join(d, id+".jsonl")
		os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o600)
		os.Chtimes(p, mtime, mtime)
	}
	mk("-a", "recent", now.Add(-time.Minute), lPrompt)
	mk("-b", "old", now.Add(-48*time.Hour), lPrompt, lEndTurn)
	mk("-a/sub", "subagent", now, lPrompt) // deeper: ignored
	got, err := Scan(root, 24*time.Hour, Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "recent" {
		t.Fatalf("got %+v", got)
	}
}

func TestTitle(t *testing.T) {
	const (
		summary = `{"type":"summary","summary":"Old summary","leafUuid":"x"}`
		aiTitle = `{"type":"ai-title","aiTitle":"Synchro des comptes","sessionId":"x"}`
		custom  = `{"type":"custom-title","customTitle":"  Mon\nnom  ","sessionId":"x"}`
	)
	if s := parseT(t, now, summary, lPrompt); s.Title != "Old summary" {
		t.Fatalf("summary: %q", s.Title)
	}
	if s := parseT(t, now, summary, aiTitle, lPrompt); s.Title != "Synchro des comptes" {
		t.Fatalf("ai: %q", s.Title)
	}
	if s := parseT(t, now, custom, aiTitle, lPrompt); s.Title != "Mon nom" {
		t.Fatalf("custom wins: %q", s.Title)
	}
	// Summary at the top of a transcript longer than the tail window.
	filler := `{"type":"user","message":{"content":"` + strings.Repeat("x", 4000) + `"}}`
	lines := []string{summary}
	for i := 0; i < 300; i++ {
		lines = append(lines, filler)
	}
	if s := parseT(t, now, append(lines, lPrompt)...); s.Title != "Old summary" {
		t.Fatalf("head title: %q", s.Title)
	}
}

// A session waiting for a subagent is silent; the subagent's writes keep it
// working instead of stale, and subagent files are never sessions.
func TestSubagentActivity(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, mtime time.Time, lines ...string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o600)
		os.Chtimes(p, mtime, mtime)
	}
	lTask := strings.Replace(lToolUse, `"Bash"`, `"Task"`, 1)
	write("-p/main.jsonl", now.Add(-30*time.Minute), lPrompt, lTask)
	write("-p/main/subagents/agent-1.jsonl", now.Add(-10*time.Second), lPrompt)
	write("-p/agent-2.jsonl", now, lPrompt) // older layout
	got, err := Scan(root, 24*time.Hour, Options{Now: now, StaleAfter: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "main" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Status != Working || got[0].Tool != "Task" || !got[0].Modified.Equal(now.Add(-10*time.Second)) {
		t.Fatalf("got %s/%q %v", got[0].Status, got[0].Tool, got[0].Modified)
	}
}

// A title or summary written into an old transcript changes its date, not
// its activity: the session keeps the time of its last prompt or reply.
func TestTitleWriteIsNotActivity(t *testing.T) {
	later := now.Add(72 * time.Hour)
	p := write(t, later, lPrompt, lToolUse, `{"type":"summary","summary":"Old work"}`)
	s, err := ParseFile(p, Options{StaleAfter: 10 * time.Minute, Now: later})
	if err != nil {
		t.Fatal(err)
	}
	if s.Modified.After(now) || s.Status != Stale {
		t.Fatalf("got %s at %v", s.Status, s.Modified)
	}
}
