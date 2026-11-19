package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/dmckminard/pixregie/internal/icon"
)

func platformIcon(png []byte) []byte { return icon.ICO(png, 32) }

// hidden runs a console helper (reg, rundll32) without flashing a window.
func hidden(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return c
}

// dialog runs PowerShell for a dialog: no console window, but NOT
// HideWindow, which Windows would apply to the dialog itself and leave the
// app waiting on an invisible box.
func dialog(script string) *exec.Cmd {
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return c
}

func darkMenuBar() bool {
	out, err := hidden("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
		"/v", "SystemUsesLightTheme").Output()
	if err != nil {
		return true // Windows 11 default taskbar is dark
	}
	return strings.Contains(string(out), "0x0")
}

// psString quotes s for a PowerShell single-quoted string.
func psString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// prompt shows an input box. Windows' InputBox cannot hide what is typed.
func prompt(msg string, _ bool) (string, bool) {
	script := "Add-Type -AssemblyName Microsoft.VisualBasic; " +
		"[Microsoft.VisualBasic.Interaction]::InputBox(" + psString(msg) + ", 'Pixregie', '')"
	out, err := dialog(script).Output()
	if err != nil {
		return "", false
	}
	t := strings.TrimSpace(string(out))
	return t, t != ""
}

func alert(msg string) {
	script := "Add-Type -AssemblyName PresentationFramework; [System.Windows.MessageBox]::Show(" +
		psString(msg) + ", 'Pixregie') | Out-Null"
	dialog(script).Run()
}

func openURL(u string) { hidden("rundll32", "url.dll,FileProtocolHandler", u).Start() }

const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

func autostartEnabled() bool {
	return hidden("reg", "query", runKey, "/v", "Pixregie").Run() == nil
}

func setAutostart(on bool) error {
	if !on {
		return hidden("reg", "delete", runKey, "/v", "Pixregie", "/f").Run()
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return hidden("reg", "add", runKey, "/v", "Pixregie", "/t", "REG_SZ", "/d", `"`+exe+`"`, "/f").Run()
}

func computerName() string {
	h, _ := os.Hostname()
	return h
}
