package agentcfg

import "testing"

func TestValidToken(t *testing.T) {
	for tok, want := range map[string]bool{
		"pxr_abcdefghijklmnopqrstuvwxyz0123456789-_AB": true,
		"pxr_short":                        false,
		"abc_abcdefghijklmnopqrstuvwxyz":   false,
		"pxr_abcdefghijklmnop qrstuvwxyz":  false,
		"pxr_abcdefghijklmnopqrstu;rm -rf": false,
	} {
		if ValidToken(tok) != want {
			t.Errorf("%q: want %v", tok, want)
		}
	}
}

func TestTokenRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if _, err := GetToken(); err != ErrNoToken {
		t.Fatalf("got %v", err)
	}
	if err := SetToken("pxr_abcdefghijklmnopqrstuvwxyz"); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetToken(); got != "pxr_abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("got %q", got)
	}
}
