package collector

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func pairServer() (*Server, *[]string) {
	saved := &[]string{}
	s := &Server{
		Store:  NewStore(time.Minute, time.Hour),
		Auth:   Auth{AgentTokens: map[string]string{HashSecret("pxr_old"): "Mac"}},
		Pairer: NewPairer(),
		SaveAgent: func(hash, label string) error {
			*saved = append(*saved, label)
			return nil
		},
	}
	return s, saved
}

func post(s *Server, path, body string) (int, map[string]any) {
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
	var m map[string]any
	json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func TestPairingFlow(t *testing.T) {
	s, saved := pairServer()
	code, m := post(s, "/v1/pair/start", `{"label":"Mac.local"}`)
	if code != 200 {
		t.Fatalf("start %d", code)
	}
	id := m["id"].(string)
	req, ok := s.Pairer.Pending()
	if !ok || len(req.Code) != 6 || req.Label != "Mac" {
		t.Fatalf("pending %+v", req)
	}

	if c, _ := post(s, "/v1/pair/finish", `{"id":"`+id+`","code":"000000x"}`); c != 403 {
		t.Fatalf("wrong code: %d", c)
	}
	c, m := post(s, "/v1/pair/finish", `{"id":"`+id+`","code":"`+req.Code[:3]+" "+req.Code[3:]+`"}`)
	if c != 200 {
		t.Fatalf("finish %d", c)
	}
	tok := m["token"].(string)
	if m["label"] != "Mac 2" || len(*saved) != 1 {
		t.Fatalf("label %v saved %v", m["label"], *saved)
	}
	// The new token works for reports right away.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/report", strings.NewReader(`{"sessions":[]}`))
	r.Header.Set("Authorization", "Bearer "+tok)
	s.Handler().ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("report %d", w.Code)
	}
	// Pairing is consumed.
	if _, ok := s.Pairer.Pending(); ok {
		t.Fatal("still pending")
	}
	if c, _ := post(s, "/v1/pair/finish", `{"id":"`+id+`","code":"`+req.Code+`"}`); c != 410 {
		t.Fatalf("replay: %d", c)
	}
}

func TestPairingGivesUpAfterThreeWrongCodes(t *testing.T) {
	s, _ := pairServer()
	_, m := post(s, "/v1/pair/start", `{"label":"x"}`)
	id := m["id"].(string)
	req, _ := s.Pairer.Pending()
	wrong := "999999"
	if req.Code == wrong {
		wrong = "999998"
	}
	post(s, "/v1/pair/finish", `{"id":"`+id+`","code":"`+wrong+`"}`)
	post(s, "/v1/pair/finish", `{"id":"`+id+`","code":"`+wrong+`"}`)
	post(s, "/v1/pair/finish", `{"id":"`+id+`","code":"`+wrong+`"}`)
	if c, _ := post(s, "/v1/pair/finish", `{"id":"`+id+`","code":"`+req.Code+`"}`); c != 410 {
		t.Fatalf("must be cancelled, got %d", c)
	}
}

func TestPairingExpiresAndRateLimits(t *testing.T) {
	s, _ := pairServer()
	now := time.Now()
	s.Pairer.now = func() time.Time { return now }
	_, m := post(s, "/v1/pair/start", `{"label":"x"}`)
	req, _ := s.Pairer.Pending()
	now = now.Add(3 * time.Minute)
	if c, _ := post(s, "/v1/pair/finish", `{"id":"`+m["id"].(string)+`","code":"`+req.Code+`"}`); c != 410 {
		t.Fatalf("expired: %d", c)
	}
	for i := 0; i < pairPerMin; i++ {
		post(s, "/v1/pair/start", `{"label":"x"}`)
	}
	if c, _ := post(s, "/v1/pair/start", `{"label":"x"}`); c != 429 {
		t.Fatalf("rate limit: %d", c)
	}
}

func TestCleanLabel(t *testing.T) {
	for in, want := range map[string]string{
		"MacBook-Pro-de-David.local":             "MacBook-Pro-de-David",
		"DESKTOP-7QK2\x1b[31m":                   "DESKTOP-7QK2-31m",
		"":                                       "machine",
		"un nom vraiment beaucoup trop long ici": "un nom vraiment beaucoup",
	} {
		if got := CleanLabel(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
