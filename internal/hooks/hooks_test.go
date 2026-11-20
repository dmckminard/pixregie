package hooks

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dmckminard/pixregie/internal/session"
)

func TestTrackerLifecycle(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "state.json")
	tr := NewTracker(path)
	tr.now = func() time.Time { return now }

	base := []session.Session{
		{ID: "open1", Status: session.Idle, Modified: now.Add(-3 * time.Hour)},
		{ID: "closed1", Status: session.Idle, Modified: now.Add(-time.Minute)},
		{ID: "unknown", Status: session.Working, Modified: now},
		{ID: "asks", Status: session.Working, Tool: "Bash", Modified: now.Add(-time.Second)},
	}
	tr.Handle(Event{SessionID: "open1", HookEventName: "SessionStart"})
	tr.Handle(Event{SessionID: "closed1", HookEventName: "SessionStart"})
	tr.Handle(Event{SessionID: "closed1", HookEventName: "SessionEnd"})
	tr.Handle(Event{SessionID: "asks", HookEventName: "Notification", NotificationType: "permission_prompt"})
	tr.Handle(Event{SessionID: "../evil", HookEventName: "SessionEnd"}) // ignored

	got := map[string]session.Session{}
	for _, s := range tr.Apply(base) {
		got[s.ID] = s
	}
	if _, ok := got["closed1"]; ok {
		t.Fatal("closed session must be dropped")
	}
	if !got["open1"].Open {
		t.Fatal("open1 should be flagged open")
	}
	if got["asks"].Status != session.Waiting || got["asks"].Tool != "" {
		t.Fatalf("asks: %+v", got["asks"])
	}
	if _, ok := got["unknown"]; !ok {
		t.Fatal("sessions without hook info are kept")
	}

	// The tool ran: the transcript moved after the prompt, waiting clears.
	base[3].Modified = now.Add(10 * time.Second)
	for _, s := range tr.Apply(base) {
		if s.ID == "asks" && s.Status != session.Working {
			t.Fatalf("waiting should clear, got %s", s.Status)
		}
	}

	// State survives a restart.
	tr2 := NewTracker(path)
	if st := tr2.m["closed1"]; st == nil || !st.Closed {
		t.Fatal("state not persisted")
	}
}

func TestIdleReminderIsNotWaiting(t *testing.T) {
	tr := NewTracker("")
	tr.Handle(Event{SessionID: "s", HookEventName: "Notification", Message: "Claude is waiting for your input"})
	out := tr.Apply([]session.Session{{ID: "s", Status: session.Idle}})
	if out[0].Status != session.Idle {
		t.Fatalf("got %s", out[0].Status)
	}
}

func TestHandlerRejectsBrowserOrigin(t *testing.T) {
	h := Handler(NewTracker(""))
	r := httptest.NewRequest("POST", "/hook", strings.NewReader(`{"session_id":"s","hook_event_name":"SessionEnd"}`))
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestInstallKeepsUserSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	orig := `{
  "model": "opus",
  "cleanupPeriodDays": 30,
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "echo mine"}]}],
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "check.sh"}]}]
  }
}`
	os.WriteFile(path, []byte(orig), 0o644)

	changed, err := Install(path)
	if err != nil || !changed || !Installed(path) {
		t.Fatalf("install: %v %v", changed, err)
	}
	if changed, _ := Install(path); changed {
		t.Fatal("second install must be a no-op")
	}
	var root map[string]any
	b, _ := os.ReadFile(path)
	json.Unmarshal(b, &root)
	if root["model"] != "opus" || root["cleanupPeriodDays"] != float64(30) {
		t.Fatalf("user keys lost: %v", root)
	}
	hooks := root["hooks"].(map[string]any)
	if n := len(hooks["SessionStart"].([]any)); n != 2 {
		t.Fatalf("SessionStart groups: %d", n)
	}
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Fatal("user hook lost")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode changed: %v", fi.Mode())
	}
	if _, err := os.Stat(path + ".pixregie-backup"); err != nil {
		t.Fatal("no backup")
	}

	if changed, err := Uninstall(path); err != nil || !changed {
		t.Fatalf("uninstall: %v %v", changed, err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "pixregie") || !strings.Contains(string(b), "echo mine") {
		t.Fatalf("after uninstall: %s", b)
	}
}

func TestInstallRefusesBrokenJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(`{"model": `), 0o600)
	if _, err := Install(path); err == nil {
		t.Fatal("must refuse")
	}
	if b, _ := os.ReadFile(path); string(b) != `{"model": ` {
		t.Fatal("file was modified")
	}
}

func TestInstallCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	if _, err := Install(path); err != nil || !Installed(path) {
		t.Fatal(err)
	}
}

func TestBindIsExclusive(t *testing.T) {
	ln, err := Bind()
	if err != nil {
		t.Skipf("port busy on this machine: %v", err)
	}
	defer ln.Close()
	if ln2, err := Bind(); err == nil {
		ln2.Close()
		t.Fatal("a second instance could bind the hook port")
	}
}

func TestTurnHooks(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	tr := NewTracker("")
	tr.now = func() time.Time { return now }
	s := []session.Session{{ID: "s", Status: session.Idle, Modified: now.Add(-time.Hour)}}
	status := func() session.Status { return tr.Apply(s)[0].Status }

	// Prompt submitted, transcript not written yet (thinking): working.
	tr.Handle(Event{SessionID: "s", HookEventName: "UserPromptSubmit"})
	if status() != session.Working {
		t.Fatal("prompt should mean working")
	}
	// Permission asked: waiting.
	tr.Handle(Event{SessionID: "s", HookEventName: "PermissionRequest"})
	if status() != session.Waiting {
		t.Fatal("permission request should mean waiting")
	}
	// Turn over, even if the scan still sees a tool running: idle.
	s[0].Status, s[0].Modified = session.Working, now
	now = now.Add(time.Second)
	tr.Handle(Event{SessionID: "s", HookEventName: "Stop"})
	if status() != session.Idle {
		t.Fatal("stop should mean idle")
	}
	// A turn silent for too long: stale (Stop never came, e.g. crash).
	now = now.Add(time.Minute)
	tr.Handle(Event{SessionID: "s", HookEventName: "UserPromptSubmit"})
	now = now.Add(StaleAfter + time.Minute)
	if status() != session.Stale {
		t.Fatalf("silent turn should be stale, got %s", status())
	}
}
