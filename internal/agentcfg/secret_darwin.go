package agentcfg

import (
	"os/exec"
	"strings"
)

const (
	kcService = "fr.pixregie.agent"
	kcAccount = "display-token"
)

// The token goes to the login Keychain through the system `security` tool.
// It is briefly visible in this process's arguments while being stored;
// reading it back never exposes it.

func GetToken() (string, error) {
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", kcService, "-a", kcAccount, "-w").Output()
	if err != nil {
		return "", ErrNoToken
	}
	return strings.TrimSpace(string(out)), nil
}

func SetToken(t string) error {
	return exec.Command("/usr/bin/security", "add-generic-password", "-U", "-s", kcService, "-a", kcAccount,
		"-l", "Pixregie", "-w", t).Run()
}

func DeleteToken() error {
	return exec.Command("/usr/bin/security", "delete-generic-password", "-s", kcService, "-a", kcAccount).Run()
}
