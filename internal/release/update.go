package release

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Repo is where releases are published.
const Repo = "dmckminard/pixregie"

var (
	apiBase      = "https://api.github.com"
	downloadBase = "https://github.com"
)

// Latest returns the tag of the latest published release ("v0.5.0").
func Latest(ctx context.Context, c *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var r struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return "", err
	}
	if r.Tag == "" {
		return "", errors.New("no release")
	}
	return r.Tag, nil
}

// Newer reports whether tag a ("v1.2.3") is newer than b. Unparseable
// versions (development builds) are never updated.
func Newer(a, b string) bool {
	pa, okA := parse(a)
	pb, okB := parse(b)
	if !okA || !okB {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func get(ctx context.Context, c *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: too large", url)
	}
	return b, nil
}

// Download fetches one asset of release tag and returns it only if
// SHA256SUMS is signed by Key and lists the asset with a matching hash.
func Download(ctx context.Context, c *http.Client, tag, asset string) ([]byte, error) {
	base := fmt.Sprintf("%s/%s/releases/download/%s/", downloadBase, Repo, tag)
	sums, err := get(ctx, c, base+"SHA256SUMS", 64<<10)
	if err != nil {
		return nil, err
	}
	sig, err := get(ctx, c, base+"SHA256SUMS.sig", 16<<10)
	if err != nil {
		return nil, fmt.Errorf("release %s is not signed: %w", tag, err)
	}
	if err := Verify(Key, Namespace, sums, sig); err != nil {
		return nil, err
	}
	want, err := lookup(sums, asset)
	if err != nil {
		return nil, err
	}
	data, err := get(ctx, c, base+asset, 200<<20)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("%s: checksum mismatch", asset)
	}
	return data, nil
}

// lookup finds "<hex>  <asset>" in a sha256sum listing.
func lookup(sums []byte, asset string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%s not listed in SHA256SUMS", asset)
}
