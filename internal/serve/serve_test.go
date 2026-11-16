package serve

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var (
	names = []string{"pve", "localhost"}
	ips   = []net.IP{net.IPv4(127, 0, 0, 1), net.IPv4(192, 168, 1, 10)}
)

func TestSelfSignedCertificate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	now := time.Now()
	clock := func() time.Time { return now }

	c, err := selfSigned(dir, names, ips, clock)
	if err != nil {
		t.Fatal(err)
	}
	leaf := c.cert.Leaf
	for _, host := range []string{"pve", "localhost", "127.0.0.1", "192.168.1.10"} {
		if err := leaf.VerifyHostname(host); err != nil {
			t.Errorf("certificate does not cover %s: %v", host, err)
		}
	}
	if leaf.VerifyHostname("example.com") == nil {
		t.Error("certificate covers a name it was not made for")
	}
	if life := leaf.NotAfter.Sub(now); life > 825*24*time.Hour || life < 800*24*time.Hour {
		t.Errorf("certificate lasts %v", life)
	}
	if leaf.IsCA {
		t.Error("the certificate could be used to sign others")
	}
	// The private key must be readable only by its owner.
	if info, err := os.Stat(filepath.Join(dir, "key.pem")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("key file: %v, mode %v", err, info.Mode().Perm())
	}
	first := c.Fingerprint()
	if len(first) != 95 { // 32 bytes as "AB:CD:..."
		t.Errorf("fingerprint = %q", first)
	}

	// Starting again keeps the same certificate, so browsers that accepted
	// it are not asked again.
	again, err := selfSigned(dir, names, ips, clock)
	if err != nil || again.Fingerprint() != first {
		t.Errorf("certificate changed on restart (err %v)", err)
	}

	// A new address on the host needs a new certificate.
	moved, err := selfSigned(dir, names, append(ips, net.IPv4(10, 20, 30, 5)), clock)
	if err != nil || moved.Fingerprint() == first || moved.cert.Leaf.VerifyHostname("10.20.30.5") != nil {
		t.Errorf("certificate was not replaced for a new address (err %v)", err)
	}

	// Close to expiry, it is replaced while serving, without a restart.
	renewing := moved.Fingerprint()
	now = now.Add(selfSignedLife - renewBefore + time.Hour)
	if _, err := moved.GetCertificate(nil); err != nil {
		t.Fatal(err)
	}
	if moved.Fingerprint() == renewing {
		t.Error("certificate was not renewed before expiry")
	}
	if moved.cert.Leaf.NotAfter.Before(now.Add(800 * 24 * time.Hour)) {
		t.Error("renewed certificate is not fresh")
	}
}

func TestProvidedCertificateIsReloaded(t *testing.T) {
	// Make two certificates to stand in for the owner's, before and after renewal.
	a, err := SelfSigned(filepath.Join(t.TempDir(), "a"), names, ips)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SelfSigned(filepath.Join(t.TempDir(), "b"), names, ips)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	install := func(from *Certs, modTime time.Time) {
		for src, dst := range map[string]string{from.certFile: certFile, from.keyFile: keyFile} {
			data, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dst, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(certFile, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	install(a, now)
	c, err := Load(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return now }
	if c.Generated() || c.Fingerprint() != a.Fingerprint() {
		t.Fatal("did not load the provided certificate")
	}

	install(b, now.Add(time.Hour))
	now = now.Add(2 * recheckEvery)
	c.GetCertificate(nil)
	if c.Fingerprint() != b.Fingerprint() {
		t.Error("a replaced certificate was not picked up")
	}

	// A broken replacement leaves the working certificate in use.
	if err := os.WriteFile(certFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(certFile, now.Add(3*time.Hour), now.Add(3*time.Hour))
	now = now.Add(2 * recheckEvery)
	c.GetCertificate(nil)
	if c.Fingerprint() != b.Fingerprint() {
		t.Error("a broken certificate file replaced a working one")
	}

	if _, err := Load(filepath.Join(dir, "missing.pem"), keyFile); err == nil {
		t.Error("loading a missing certificate succeeded")
	}
}

func TestHTTPSAndRedirectOnOnePort(t *testing.T) {
	certs, err := SelfSigned(filepath.Join(t.TempDir(), "tls"), names, ips)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- HTTPS(ctx, ln, certs, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "secure=%v proto=%s path=%s", r.TLS != nil, r.Proto, r.URL.Path)
		}))
	}()

	// Trust exactly the generated certificate, as a browser does once the
	// owner has accepted it.
	pool := x509.NewCertPool()
	pool.AddCert(certs.cert.Leaf)
	client := &http.Client{
		Timeout:       5 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}, ForceAttemptHTTP2: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	// A client that connects and sends nothing must not block the others.
	idle, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()

	resp, err := client.Get("https://" + addr + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "secure=true proto=HTTP/2.0 path=/api/status" {
		t.Errorf("https answer = %q", body)
	}

	resp, err = client.Get("http://" + addr + "/some/page?x=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect || resp.Header.Get("Location") != "https://"+addr+"/some/page?x=1" {
		t.Errorf("plain HTTP: status %d, location %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// An old TLS version is refused.
	old := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MaxVersion: tls.VersionTLS11}}}
	if _, err := old.Get("https://" + addr + "/"); err == nil {
		t.Error("TLS 1.1 was accepted")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("server did not stop")
	}
}
