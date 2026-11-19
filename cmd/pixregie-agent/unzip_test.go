package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(body))
	}
	w.Close()
	return b.Bytes()
}

func TestUnzip(t *testing.T) {
	dst := t.TempDir()
	if err := unzip(zipOf(t, map[string]string{"Pixregie.app/Contents/MacOS/pixregie-agent": "bin"}), dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "Pixregie.app/Contents/MacOS/pixregie-agent")); string(b) != "bin" {
		t.Fatal("not extracted")
	}
	for _, evil := range []string{"../evil", "/etc/evil", "a/../../evil"} {
		if err := unzip(zipOf(t, map[string]string{evil: "x"}), t.TempDir()); err == nil {
			t.Fatalf("%s accepted", evil)
		}
	}
}
