// Package release verifies release signatures and finds updates.
//
// Releases are signed with an SSH ed25519 key (`ssh-keygen -Y sign`): the
// same signature is checked by the install scripts with `ssh-keygen -Y
// verify` and here with the standard library only.
package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
)

// Key is the public key releases are signed with. Its private half lives
// only in the repository's GitHub secrets.
const Key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPrsmtv1WZ/gg6Fg2dz99VsjIpErQ0acaMFV1hBaFQLo pixregie-release"

// Namespace binds signatures to this use (ssh-keygen -n).
const Namespace = "pixregie-release"

var errBadSig = errors.New("invalid release signature")

// sshString reads one SSH wire-format string.
func sshString(b []byte) ([]byte, []byte, error) {
	if len(b) < 4 {
		return nil, nil, errBadSig
	}
	n := binary.BigEndian.Uint32(b)
	if uint64(len(b)-4) < uint64(n) {
		return nil, nil, errBadSig
	}
	return b[4 : 4+n], b[4+n:], nil
}

func appendString(b, s []byte) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

// keyBlob parses "ssh-ed25519 AAAA… comment" into its wire blob and key.
func keyBlob(authorized string) ([]byte, ed25519.PublicKey, error) {
	f := strings.Fields(authorized)
	if len(f) < 2 || f[0] != "ssh-ed25519" {
		return nil, nil, errors.New("not an ssh-ed25519 key")
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return nil, nil, err
	}
	typ, rest, err := sshString(blob)
	if err != nil || string(typ) != "ssh-ed25519" {
		return nil, nil, errors.New("bad key blob")
	}
	pk, rest, err := sshString(rest)
	if err != nil || len(pk) != ed25519.PublicKeySize || len(rest) != 0 {
		return nil, nil, errors.New("bad key blob")
	}
	return blob, ed25519.PublicKey(pk), nil
}

// Verify checks an armored SSH signature (PROTOCOL.sshsig) of msg made by
// the key `authorized` in the given namespace.
func Verify(authorized, namespace string, msg, armored []byte) error {
	wantBlob, pub, err := keyBlob(authorized)
	if err != nil {
		return err
	}
	s := string(armored)
	const begin, end = "-----BEGIN SSH SIGNATURE-----", "-----END SSH SIGNATURE-----"
	i, j := strings.Index(s, begin), strings.Index(s, end)
	if i < 0 || j < i {
		return errBadSig
	}
	raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s[i+len(begin):j]), ""))
	if err != nil || len(raw) < 10 || string(raw[:6]) != "SSHSIG" {
		return errBadSig
	}
	if binary.BigEndian.Uint32(raw[6:]) != 1 {
		return errBadSig
	}
	rest := raw[10:]
	var keyB, ns, reserved, hashAlg, sigB []byte
	for _, dst := range []*[]byte{&keyB, &ns, &reserved, &hashAlg, &sigB} {
		if *dst, rest, err = sshString(rest); err != nil {
			return errBadSig
		}
	}
	if !bytes.Equal(keyB, wantBlob) {
		return errors.New("release signed by an unknown key")
	}
	if string(ns) != namespace {
		return errors.New("release signature for another purpose")
	}
	var h []byte
	switch string(hashAlg) {
	case "sha512":
		d := sha512.Sum512(msg)
		h = d[:]
	case "sha256":
		d := sha256.Sum256(msg)
		h = d[:]
	default:
		return errBadSig
	}
	sigType, sigRest, err := sshString(sigB)
	if err != nil || string(sigType) != "ssh-ed25519" {
		return errBadSig
	}
	sig, _, err := sshString(sigRest)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errBadSig
	}
	signed := []byte("SSHSIG")
	signed = appendString(signed, ns)
	signed = appendString(signed, reserved)
	signed = appendString(signed, hashAlg)
	signed = appendString(signed, h)
	if !ed25519.Verify(pub, signed, sig) {
		return errBadSig
	}
	return nil
}
