package collector

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dmckminard/pixregie/internal/session"
)

func TestStoreOfflineAndForget(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := NewStore(30*time.Second, time.Hour)
	s.now = func() time.Time { return now }
	s.Put("mac", Report{Sessions: []session.Session{{ID: "b", Project: "z", Status: session.Working}, {ID: "a", Project: "a", Status: "bogus"}}})

	st := s.Snapshot()
	if len(st.Machines) != 1 || !st.Machines[0].Online {
		t.Fatalf("%+v", st)
	}
	ss := st.Machines[0].Sessions
	if ss[0].Project != "a" || ss[0].Status != session.Idle || ss[0].Machine != "mac" {
		t.Fatalf("sorting/sanitising failed: %+v", ss)
	}

	now = now.Add(time.Minute)
	if s.Snapshot().Machines[0].Online {
		t.Fatal("should be offline")
	}
	now = now.Add(2 * time.Hour)
	if len(s.Snapshot().Machines) != 0 {
		t.Fatal("should be forgotten")
	}
}

func TestTrim(t *testing.T) {
	if got := trim("a\x1b[31mb\n" + strings.Repeat("x", 100)); strings.ContainsAny(got, "\x1b\n") || len([]rune(got)) != maxField {
		t.Fatalf("%q", got)
	}
}

func newTestServer() (*Server, http.Handler) {
	s := &Server{
		Store: NewStore(time.Minute, time.Hour),
		Auth: Auth{
			AgentTokens:   map[string]string{HashSecret("tok-mac"): "mac"},
			DebugPassword: mustHash("pw"),
		},
	}
	return s, s.Handler()
}

func do(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestReportAuth(t *testing.T) {
	s, h := newTestServer()
	body := `{"sessions":[{"id":"x","machine":"spoofed","project":"p","status":"working"}]}`

	if w := do(h, "POST", "/v1/report", body, nil); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
	if w := do(h, "POST", "/v1/report", body, map[string]string{"Authorization": "Bearer nope"}); w.Code != 401 {
		t.Fatalf("bad token: %d", w.Code)
	}
	if w := do(h, "POST", "/v1/report", body, map[string]string{"Authorization": "Bearer tok-mac"}); w.Code != 204 {
		t.Fatalf("good token: %d", w.Code)
	}
	m := s.Store.Snapshot().Machines
	if len(m) != 1 || m[0].Name != "mac" || m[0].Sessions[0].Machine != "mac" {
		t.Fatalf("machine must come from the token: %+v", m)
	}
}

func TestReportTooLarge(t *testing.T) {
	_, h := newTestServer()
	big := `{"sessions":[{"id":"` + strings.Repeat("x", maxBody) + `"}]}`
	if w := do(h, "POST", "/v1/report", big, map[string]string{"Authorization": "Bearer tok-mac"}); w.Code != 400 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestDebugPage(t *testing.T) {
	_, h := newTestServer()
	if w := do(h, "GET", "/", "", nil); w.Code != 401 {
		t.Fatalf("no auth: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("x", "pw")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "PIXREGIE") {
		t.Fatalf("page: %d", w.Code)
	}
	if w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("missing CSP")
	}

	// No password configured: page disabled entirely.
	s2 := &Server{Store: NewStore(time.Minute, time.Hour)}
	if w := do(s2.Handler(), "GET", "/v1/state", "", nil); w.Code != 404 {
		t.Fatalf("disabled: %d", w.Code)
	}
}

func mustHash(pw string) string {
	h, err := HashPassword(pw)
	if err != nil {
		panic(err)
	}
	return h
}

func TestPassword(t *testing.T) {
	h := mustHash("secret")
	if !CheckPassword(h, "secret") || CheckPassword(h, "Secret") || CheckPassword("garbage", "secret") {
		t.Fatal("password check")
	}
}

func TestRetainAndSetAgents(t *testing.T) {
	s, h := newTestServer()
	s.Store.Put("mac", Report{})
	s.Store.Put("old", Report{})
	s.Store.Retain(map[string]bool{"mac": true})
	if m := s.Store.Snapshot().Machines; len(m) != 1 || m[0].Name != "mac" {
		t.Fatalf("%+v", m)
	}
	s.SetAgents(map[string]string{})
	body := `{"sessions":[]}`
	if w := do(h, "POST", "/v1/report", body, map[string]string{"Authorization": "Bearer tok-mac"}); w.Code != 401 {
		t.Fatalf("revoked token still accepted: %d", w.Code)
	}
}

func TestAdminPostNeedsSameOrigin(t *testing.T) {
	called := 0
	s := &Server{
		Store: NewStore(time.Minute, time.Hour),
		Auth:  Auth{DebugPassword: mustHash("pw")},
		AdminPost: map[string]http.Handler{"/v1/admin/x": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called++
		})},
	}
	h := s.Handler()
	try := func(hdr map[string]string) int {
		r := httptest.NewRequest("POST", "http://pi:7421/v1/admin/x", strings.NewReader("{}"))
		r.SetBasicAuth("x", "pw")
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if try(nil) != 403 || try(map[string]string{"X-Pixregie": "1", "Origin": "https://evil.example"}) != 403 {
		t.Fatal("cross-site request accepted")
	}
	if try(map[string]string{"X-Pixregie": "1", "Origin": "http://pi:7421"}) != 200 || called != 1 {
		t.Fatal("same-origin request refused")
	}
}

func TestPasswordLockout(t *testing.T) {
	_, h := newTestServer()
	code := func(pw string) int {
		r := httptest.NewRequest("GET", "/v1/state", nil)
		r.SetBasicAuth("x", pw)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < maxFailures; i++ {
		if code("wrong") != 401 {
			t.Fatal("wrong password not refused")
		}
	}
	if code("pw") != 429 {
		t.Fatal("no lockout after repeated failures")
	}
}
