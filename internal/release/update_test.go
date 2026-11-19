package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.5.0", "v0.4.3", true},
		{"v0.4.10", "v0.4.9", true},
		{"v0.4.3", "v0.4.3", false},
		{"v0.4.2", "v0.4.3", false},
		{"v1.0.0", "dev", false},
		{"garbage", "v0.1.0", false},
	} {
		if Newer(c.a, c.b) != c.want {
			t.Errorf("Newer(%s, %s) != %v", c.a, c.b, c.want)
		}
	}
}

func TestDownloadRequiresSignature(t *testing.T) {
	asset := []byte("binary")
	h := sha256.Sum256(asset)
	sums := hex.EncodeToString(h[:]) + "  pixregie-display-linux-arm64\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			w.Write([]byte(sums))
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS.sig"):
			w.Write([]byte(testSig)) // signed by the test key, not Key
		default:
			w.Write(asset)
		}
	}))
	defer srv.Close()
	downloadBase = srv.URL
	defer func() { downloadBase = "https://github.com" }()
	if _, err := Download(context.Background(), srv.Client(), "v9.9.9", "pixregie-display-linux-arm64"); err == nil {
		t.Fatal("download accepted without a valid release signature")
	}
}

func TestLookup(t *testing.T) {
	sums := []byte(strings.Repeat("a", 64) + "  Pixregie.exe\n" + strings.Repeat("b", 64) + " *install-pi.sh\n")
	if h, err := lookup(sums, "install-pi.sh"); err != nil || h != strings.Repeat("b", 64) {
		t.Fatal(h, err)
	}
	if _, err := lookup(sums, "missing"); err == nil {
		t.Fatal("missing asset found")
	}
}
