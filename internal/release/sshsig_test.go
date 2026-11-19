package release

import (
	"os"
	"strings"
	"testing"
)

// Made with a throwaway key: ssh-keygen -Y sign -f k -n pixregie-release msg
const (
	testKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJdCAOy3a4hhiy+B9YNwY+g8JwadYObBdLjIBX4q4vLk test"
	testSig = `-----BEGIN SSH SIGNATURE-----
U1NIU0lHAAAAAQAAADMAAAALc3NoLWVkMjU1MTkAAAAgl0IA7LdriGGLL4H1g3Bj6DwnBp
1g5sF0uMgFfiri8uQAAAAQcGl4cmVnaWUtcmVsZWFzZQAAAAAAAAAGc2hhNTEyAAAAUwAA
AAtzc2gtZWQyNTUxOQAAAED6E7mLEyCQysXxh6S2jALjXOm/w02sk5Y3WBIXkW/hBlu2yT
JWmJHunjCns/f8pBUVqH4CjRqfO6HJiouNwAcP
-----END SSH SIGNATURE-----
`
)

var testMsg = []byte("hello pixregie\n")

func TestVerify(t *testing.T) {
	if err := Verify(testKey, Namespace, testMsg, []byte(testSig)); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if Verify(testKey, Namespace, []byte("hello pixregie!\n"), []byte(testSig)) == nil {
		t.Fatal("tampered message accepted")
	}
	if Verify(Key, Namespace, testMsg, []byte(testSig)) == nil {
		t.Fatal("signature from another key accepted")
	}
	if Verify(testKey, "other", testMsg, []byte(testSig)) == nil {
		t.Fatal("wrong namespace accepted")
	}
	for _, bad := range []string{"", "garbage", "-----BEGIN SSH SIGNATURE-----\nAAAA\n-----END SSH SIGNATURE-----"} {
		if Verify(testKey, Namespace, testMsg, []byte(bad)) == nil {
			t.Fatalf("garbage accepted: %q", bad)
		}
	}
}

func TestReleaseKeyParses(t *testing.T) {
	if _, _, err := keyBlob(Key); err != nil {
		t.Fatal(err)
	}
}

// Every copy of the release key (scripts, release.pub) must be this one,
// or a rotated key would leave some installer trusting the old one.
func TestKeyCopiesMatch(t *testing.T) {
	pub, err := os.ReadFile("../../release.pub")
	if err != nil || strings.TrimSpace(string(pub)) != Key {
		t.Fatalf("release.pub differs from Key: %v", err)
	}
	for _, f := range []string{
		"../../install/pi/install.sh", "../../install/server/install.sh",
		"../../install/mac/install.sh", "../../install/common/pixregie-update.sh",
	} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `RELEASE_KEY="`+Key+`"`) {
			t.Errorf("%s does not carry the release key", f)
		}
	}
}
