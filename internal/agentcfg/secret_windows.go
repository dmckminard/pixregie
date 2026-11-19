package agentcfg

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// On Windows the token is encrypted with DPAPI (CryptProtectData): only
// this Windows user on this machine can decrypt the file.

var (
	crypt32           = syscall.NewLazyDLL("crypt32.dll")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procProtectData   = crypt32.NewProc("CryptProtectData")
	procUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree     = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	n uint32
	p *byte
}

func blobOf(b []byte) *dataBlob {
	if len(b) == 0 {
		return &dataBlob{}
	}
	return &dataBlob{n: uint32(len(b)), p: &b[0]}
}

func (d *dataBlob) bytes() []byte {
	out := make([]byte, d.n)
	copy(out, unsafe.Slice(d.p, d.n))
	return out
}

const cryptprotectUIForbidden = 0x1

func dpapi(proc *syscall.LazyProc, in []byte) ([]byte, error) {
	var out dataBlob
	r, _, err := proc.Call(uintptr(unsafe.Pointer(blobOf(in))), 0, 0, 0, 0, cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.p)))
	return out.bytes(), nil
}

func tokenFile() (string, error) { return path("token.dpapi") }

func GetToken() (string, error) {
	p, err := tokenFile()
	if err != nil {
		return "", err
	}
	enc, err := os.ReadFile(p)
	if err != nil {
		return "", ErrNoToken
	}
	dec, err := dpapi(procUnprotectData, enc)
	if err != nil {
		return "", ErrNoToken
	}
	return string(dec), nil
}

func SetToken(t string) error {
	p, err := tokenFile()
	if err != nil {
		return err
	}
	enc, err := dpapi(procProtectData, []byte(t))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, enc, 0o600)
}

func DeleteToken() error {
	p, err := tokenFile()
	if err != nil {
		return err
	}
	return os.Remove(p)
}
