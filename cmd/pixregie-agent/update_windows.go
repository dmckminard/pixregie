package main

import (
	"os"
	"os/exec"
)

const updateAsset = "Pixregie.exe"

// applyUpdate replaces the running exe: Windows cannot overwrite it but can
// rename it, so the old one becomes .old (deleted at next start) and the
// new one takes its name. The data was verified before reaching here.
func applyUpdate(data []byte) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	os.Remove(exe + ".old")
	if err := os.Rename(exe, exe+".old"); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(exe+".old", exe)
		return err
	}
	return exec.Command(exe).Start()
}

func cleanupOldUpdate() {
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}
