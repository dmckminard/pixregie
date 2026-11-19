// Package tlspin gives the display a self-signed certificate and the
// agents a client that trusts exactly that certificate (pinned by its
// SHA-256), learnt when the machine is paired.
//
// A board on a home network cannot get a publicly trusted certificate;
// pinning is stronger anyway: an agent trusts this one Pi, not any
// certificate authority.
package tlspin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Fingerprint is the hex SHA-256 of a DER certificate.
func Fingerprint(der []byte) string {
	h := sha256.Sum256(der)
	return hex.EncodeToString(h[:])
}

// LoadOrCreate returns the display's certificate, creating a self-signed
// one (ECDSA P-256, 20 years) on first use. Files are written 0600.
func LoadOrCreate(certPath, keyPath string, hosts []string) (tls.Certificate, string, error) {
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return c, Fingerprint(c.Certificate[0]), nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Pixregie"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(20, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if h != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	c, err := tls.LoadX509KeyPair(certPath, keyPath)
	return c, Fingerprint(der), err
}

// ServerConfig is the display's TLS configuration.
func ServerConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
}

// ErrPin means the display presented another certificate than the pinned
// one: another device, or someone in the middle.
var ErrPin = errors.New("certificat du Pi inconnu (autre appareil ou interception)")

// Pinner verifies that every connection presents the pinned certificate.
// With no pin yet (first contact), it accepts the certificate and records
// its fingerprint, which the caller then stores.
type Pinner struct {
	mu   sync.Mutex
	pin  string
	seen string // fingerprint of the last accepted connection
}

func NewPinner(pin string) *Pinner { return &Pinner{pin: strings.ToLower(pin)} }

// Seen returns the fingerprint of the last accepted connection.
func (p *Pinner) Seen() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen
}

// Pin fixes the trusted fingerprint (after a successful pairing).
func (p *Pinner) Pin(fp string) {
	p.mu.Lock()
	p.pin = strings.ToLower(fp)
	p.mu.Unlock()
}

// Pinned returns the trusted fingerprint, empty before the first pairing.
func (p *Pinner) Pinned() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pin
}

func (p *Pinner) verify(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return ErrPin
	}
	fp := Fingerprint(cs.PeerCertificates[0].Raw)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pin != "" && subtle.ConstantTimeCompare([]byte(fp), []byte(p.pin)) != 1 {
		return ErrPin
	}
	p.seen = fp
	return nil
}

// Client returns an HTTP client whose TLS connections are checked by p
// instead of by certificate authorities. Make a new one after Pin.
func (p *Pinner) Client(timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // the board is on the local network
	if p.Pinned() == "" {
		// Before pinning, Seen must describe the connection of the request
		// just made, not a pooled one to another board.
		tr.DisableKeepAlives = true
	}
	tr.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Chain and name checks are replaced by the pin check below.
		InsecureSkipVerify: true,
		VerifyConnection:   p.verify,
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}
