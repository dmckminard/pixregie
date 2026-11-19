package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const updateAsset = "Pixregie-macos.zip"

// bundlePath is the .app this binary runs from.
func bundlePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	b := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // …/X.app/Contents/MacOS/bin
	if !strings.HasSuffix(b, ".app") {
		return "", errors.New("not running from an .app bundle")
	}
	return b, nil
}

// applyUpdate swaps the running bundle for the one in the zip and starts
// it. The zip was verified (signed checksum) before reaching here.
func applyUpdate(data []byte) error {
	bundle, err := bundlePath()
	if err != nil {
		return err
	}
	parent := filepath.Dir(bundle)
	stage := filepath.Join(parent, fmt.Sprintf(".pixregie-update-%d", time.Now().Unix()))
	if err := os.Mkdir(stage, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := unzip(data, stage); err != nil {
		return err
	}
	fresh := filepath.Join(stage, "Pixregie.app")
	if _, err := os.Stat(filepath.Join(fresh, "Contents", "MacOS", "pixregie-agent")); err != nil {
		return errors.New("unexpected update content")
	}
	old := filepath.Join(stage, "old.app")
	if err := os.Rename(bundle, old); err != nil {
		return err
	}
	if err := os.Rename(fresh, bundle); err != nil {
		os.Rename(old, bundle) // put the running app back
		return err
	}
	return exec.Command("/usr/bin/open", "-n", bundle).Start()
}

func cleanupOldUpdate() {}
