package tlspin

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func server(t *testing.T, dir string) (*httptest.Server, string) {
	cert, fp, err := LoadOrCreate(filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem"), []string{"pixregie.local", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	s.TLS = ServerConfig(cert)
	s.StartTLS()
	return s, fp
}

func TestPinning(t *testing.T) {
	a, fpA := server(t, t.TempDir())
	defer a.Close()
	b, _ := server(t, t.TempDir())
	defer b.Close()

	// First contact: accepted, fingerprint learnt.
	p := NewPinner("")
	if _, err := p.Client(2 * time.Second).Get(a.URL); err != nil {
		t.Fatal(err)
	}
	if p.Seen() != fpA {
		t.Fatal("fingerprint not recorded")
	}
	p.Pin(p.Seen())
	if _, err := p.Client(2 * time.Second).Get(a.URL); err != nil {
		t.Fatalf("pinned board refused: %v", err)
	}
	if _, err := p.Client(2 * time.Second).Get(b.URL); err == nil {
		t.Fatal("another certificate accepted")
	}
}

func TestLoadOrCreateIsStable(t *testing.T) {
	dir := t.TempDir()
	_, fp1, err := LoadOrCreate(filepath.Join(dir, "c"), filepath.Join(dir, "k"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, fp2, _ := LoadOrCreate(filepath.Join(dir, "c"), filepath.Join(dir, "k"), nil)
	if fp1 != fp2 {
		t.Fatal("certificate regenerated")
	}
}

func TestSplit(t *testing.T) {
	dir := t.TempDir()
	cert, _, err := LoadOrCreate(filepath.Join(dir, "c"), filepath.Join(dir, "k"), nil)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }), TLSConfig: ServerConfig(cert)}
	go hs.ServeTLS(Split(l), "", "")
	defer hs.Close()
	addr := l.Addr().String()

	p := NewPinner("")
	resp, err := p.Client(2 * time.Second).Get("https://" + addr + "/x")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("tls: %v", err)
	}
	resp.Body.Close()

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noFollow.Get("http://" + addr + "/x?y=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 301 || resp.Header.Get("Location") != "https://"+addr+"/x?y=1" {
		t.Fatalf("plain: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}
