package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

func platformIcon(png []byte) []byte { return png }

func darkMenuBar() bool {
	out, err := exec.Command("/usr/bin/defaults", "read", "-g", "AppleInterfaceStyle").Output()
	return err == nil && strings.Contains(string(out), "Dark")
}

// osaString quotes s for AppleScript.
func osaString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func prompt(msg string, hidden bool) (string, bool) {
	h := ""
	if hidden {
		h = "with hidden answer "
	}
	script := fmt.Sprintf(`text returned of (display dialog %s default answer "" %s`+
		`with title "Pixregie" buttons {"Annuler", "OK"} default button "OK" cancel button "Annuler")`,
		osaString(msg), h)
	out, err := exec.Command("/usr/bin/osascript", "-e", script).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func alert(msg string) {
	exec.Command("/usr/bin/osascript", "-e",
		fmt.Sprintf(`display alert "Pixregie" message %s`, osaString(msg))).Run()
}

func openURL(u string) { exec.Command("/usr/bin/open", u).Start() }

// Launch at login: a per-user LaunchAgent pointing at this binary.
const launchLabel = "fr.pixregie.agent"

var plist = template.Must(template.New("p").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>{{.Label}}</string>
  <key>ProgramArguments</key><array><string>{{.Exe}}</string></array>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
`))

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchLabel+".plist")
}

func autostartEnabled() bool {
	_, err := os.Stat(plistPath())
	return err == nil
}

func setAutostart(on bool) error {
	p := plistPath()
	if !on {
		err := os.Remove(p)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	var b bytes.Buffer
	if err := plist.Execute(&b, struct{ Label, Exe string }{launchLabel, exe}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b.Bytes(), 0o644)
}

// computerName is the name shown in System Settings ("MacBook Pro de Camille").
func computerName() string {
	if out, err := exec.Command("/usr/sbin/scutil", "--get", "ComputerName").Output(); err == nil {
		if n := strings.TrimSpace(string(out)); n != "" {
			return n
		}
	}
	h, _ := os.Hostname()
	return h
}
