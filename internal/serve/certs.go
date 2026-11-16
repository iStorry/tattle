// Package serve puts the dashboard on the network over HTTPS.
package serve

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// selfSignedLife is how long a generated certificate lasts. Browsers
	// refuse longer-lived certificates even after the owner accepts them.
	selfSignedLife = 825 * 24 * time.Hour
	// renewBefore is how close to expiry a generated certificate is replaced.
	renewBefore = 30 * 24 * time.Hour
	// recheckEvery limits how often the files are looked at while serving.
	recheckEvery = time.Minute
)

// Certs supplies the certificate for each connection. It is either one the
// owner provides, re-read when the files change so renewals are picked up
// without a restart, or one Tattle generates and renews itself.
type Certs struct {
	certFile, keyFile string
	generated         bool
	names             []string
	ips               []net.IP
	now               func() time.Time

	mu      sync.Mutex
	cert    *tls.Certificate
	modTime time.Time
	checked time.Time
}

// Load uses a certificate and key the owner provides.
func Load(certFile, keyFile string) (*Certs, error) {
	c := &Certs{certFile: certFile, keyFile: keyFile, now: time.Now}
	if err := c.read(); err != nil {
		return nil, err
	}
	return c, nil
}

// SelfSigned uses a certificate kept in dir, creating it if there is none,
// or if the existing one is close to expiry or does not cover the given
// host names and addresses.
func SelfSigned(dir string, names []string, ips []net.IP) (*Certs, error) {
	return selfSigned(dir, names, ips, time.Now)
}

func selfSigned(dir string, names []string, ips []net.IP, now func() time.Time) (*Certs, error) {
	c := &Certs{
		certFile: filepath.Join(dir, "cert.pem"), keyFile: filepath.Join(dir, "key.pem"),
		generated: true, names: names, ips: ips, now: now,
	}
	if err := c.read(); err != nil || !c.stillGood() {
		if err := c.generate(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// read loads the files from disk.
func (c *Certs) read() error {
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return fmt.Errorf("reading the certificate: %w", err)
	}
	if cert.Leaf == nil {
		if cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return fmt.Errorf("reading the certificate: %w", err)
		}
	}
	info, err := os.Stat(c.certFile)
	if err != nil {
		return err
	}
	c.cert, c.modTime, c.checked = &cert, info.ModTime(), c.now()
	return nil
}

// stillGood reports whether a generated certificate can keep being used.
func (c *Certs) stillGood() bool {
	leaf := c.cert.Leaf
	if c.now().After(leaf.NotAfter.Add(-renewBefore)) {
		return false
	}
	for _, n := range c.names {
		if leaf.VerifyHostname(n) != nil {
			return false
		}
	}
	for _, ip := range c.ips {
		if leaf.VerifyHostname(ip.String()) != nil {
			return false
		}
	}
	return true
}

// generate writes a new self-signed certificate and key.
func (c *Certs) generate() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return err
	}
	name := "Tattle"
	if len(c.names) > 0 {
		name = "Tattle on " + c.names[0]
	}
	now := c.now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             now.Add(-time.Hour), // tolerate clocks that are slightly off
		NotAfter:              now.Add(selfSignedLife),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              c.names,
		IPAddresses:           c.ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.certFile), 0o700); err != nil {
		return fmt.Errorf("creating the certificate folder: %w", err)
	}
	// The key is written first and readable only by its owner.
	if err := writeFile(c.keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	if err := writeFile(c.certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	return c.read()
}

// writeFile replaces a file in one step, so a crash cannot leave half of it.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// GetCertificate is called by the TLS server for every new connection.
func (c *Certs) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now := c.now(); now.Sub(c.checked) >= recheckEvery {
		c.checked = now
		c.refresh()
	}
	return c.cert, nil
}

// refresh renews a generated certificate that is close to expiry, or picks
// up a provided one that has been replaced. On failure the current
// certificate stays in use.
func (c *Certs) refresh() {
	if c.generated {
		if !c.stillGood() {
			_ = c.generate()
		}
		return
	}
	info, err := os.Stat(c.certFile)
	if err != nil || info.ModTime().Equal(c.modTime) {
		return
	}
	old := c.cert
	if c.read() != nil {
		c.cert = old
	}
}

// Generated reports whether Tattle made the certificate itself.
func (c *Certs) Generated() bool { return c.generated }

// Expires is when the certificate in use stops being valid.
func (c *Certs) Expires() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cert.Leaf.NotAfter
}

// Fingerprint is the SHA-256 fingerprint browsers show for the certificate,
// so the owner can check that the warning is about the right one.
func (c *Certs) Fingerprint() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	sum := sha256.Sum256(c.cert.Certificate[0])
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}
