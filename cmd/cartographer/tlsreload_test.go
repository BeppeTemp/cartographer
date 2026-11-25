package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeTLSPair writes a self-signed pair for cn, valid in [notBefore, notAfter],
// into dir as tls.crt and tls.key, and returns the PEM of the key. Nothing of
// it is committed: each test makes its own.
func makeTLSPair(t *testing.T, dir, cn string, notBefore, notAfter time.Time) (keyPEM string) {
	t.Helper()
	crt, key := tlsPairPEM(t, cn, notBefore, notAfter)
	if err := os.WriteFile(filepath.Join(dir, "tls.crt"), crt, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	return string(key)
}

func tlsPairPEM(t *testing.T, cn string, notBefore, notAfter time.Time) (crt, key []byte) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{"localhost"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb})
}

func validPair(t *testing.T, dir, cn string) string {
	return makeTLSPair(t, dir, cn, time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
}

// presentedCN does a TLS 1.3 handshake against cfg and returns the common name
// of the certificate the server presented.
func presentedCN(t *testing.T, cfg *tls.Config, maxVersion uint16) (string, error) {
	t.Helper()
	c, s := net.Pipe()
	defer c.Close()
	defer s.Close()
	go func() {
		srv := tls.Server(s, cfg)
		_ = srv.Handshake()
	}()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	cl := tls.Client(c, &tls.Config{InsecureSkipVerify: true, MaxVersion: maxVersion})
	if err := cl.Handshake(); err != nil {
		return "", err
	}
	return cl.ConnectionState().PeerCertificates[0].Subject.CommonName, nil
}

// AC-tls-native-5
func TestTLSReloadSwapsCertificate(t *testing.T) {
	dir := t.TempDir()
	validPair(t, dir, "first")
	r, err := newTLSReloader(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := newServerTLSConfig(r)
	if cn, err := presentedCN(t, cfg, tls.VersionTLS13); err != nil || cn != "first" {
		t.Fatalf("first handshake: %q, %v", cn, err)
	}

	// On the poll.
	validPair(t, dir, "second")
	r.poll()
	if cn, err := presentedCN(t, cfg, tls.VersionTLS13); err != nil || cn != "second" {
		t.Fatalf("after poll: %q, %v", cn, err)
	}

	// On a reload request, and through the running loop's poll.
	validPair(t, dir, "third")
	r.Reload("test")
	if cn, _ := presentedCN(t, cfg, tls.VersionTLS13); cn != "third" {
		t.Fatalf("after reload request: %q", cn)
	}
	stop := r.start(10 * time.Millisecond)
	defer stop()
	validPair(t, dir, "fourth")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if cn, _ := presentedCN(t, cfg, tls.VersionTLS13); cn == "fourth" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the running poll never applied the new pair")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// AC-tls-native-6
func TestTLSReloadRefusedKeepsCertificate(t *testing.T) {
	dir := t.TempDir()
	key := validPair(t, dir, "kept")
	r, err := newTLSReloader(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := newServerTLSConfig(r)
	buf := captureLog(t)

	// A certificate that does not match the key on disk.
	otherDir := t.TempDir()
	validPair(t, otherDir, "other")
	otherCrt, _ := os.ReadFile(filepath.Join(otherDir, "tls.crt"))
	if err := os.WriteFile(filepath.Join(dir, "tls.crt"), otherCrt, 0o600); err != nil {
		t.Fatal(err)
	}
	r.poll()
	r.poll()
	if cn, _ := presentedCN(t, cfg, tls.VersionTLS13); cn != "kept" {
		t.Fatalf("mismatched pair replaced the certificate: %q", cn)
	}
	if n := strings.Count(buf.String(), "refused"); n != 1 {
		t.Fatalf("refusal logged %d times for one content, want 1:\n%s", n, buf.String())
	}

	// Garbage, then a missing file.
	if err := os.WriteFile(filepath.Join(dir, "tls.key"), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.poll()
	if err := os.Remove(filepath.Join(dir, "tls.key")); err != nil {
		t.Fatal(err)
	}
	r.poll()
	r.poll()
	if cn, _ := presentedCN(t, cfg, tls.VersionTLS13); cn != "kept" {
		t.Fatalf("a refused content replaced the certificate: %q", cn)
	}
	// SIGHUP always logs.
	before := strings.Count(buf.String(), "refused")
	r.Reload("SIGHUP")
	if strings.Count(buf.String(), "refused") != before+1 {
		t.Fatal("a reload request did not log its refusal")
	}
	// No key bytes in any line.
	body := strings.Split(strings.TrimSpace(key), "\n")
	for _, l := range body[1 : len(body)-1] {
		if strings.Contains(buf.String(), l) {
			t.Fatalf("a log line carries key material:\n%s", buf.String())
		}
	}
}

func TestTLSReloadFollowsSymlinkSwap(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, d := range []string{a, b} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	validPair(t, a, "one")
	validPair(t, b, "two")
	creds := filepath.Join(root, "creds")
	if err := os.Mkdir(creds, 0o700); err != nil {
		t.Fatal(err)
	}
	link := func(target string) {
		for _, n := range []string{"tls.crt", "tls.key"} {
			p := filepath.Join(creds, n)
			_ = os.Remove(p)
			if err := os.Symlink(filepath.Join(target, n), p); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}
	}
	link(a)
	r, err := newTLSReloader(creds)
	if err != nil {
		t.Fatal(err)
	}
	link(b)
	r.poll()
	if cn, _ := presentedCN(t, newServerTLSConfig(r), tls.VersionTLS13); cn != "two" {
		t.Fatalf("symlink swap not seen: %q", cn)
	}
}

// AC-tls-native-7
func TestTLSServerConfigTLS13Only(t *testing.T) {
	dir := t.TempDir()
	validPair(t, dir, "v13")
	r, err := newTLSReloader(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := newServerTLSConfig(r)
	if _, err := presentedCN(t, cfg, tls.VersionTLS12); err == nil {
		t.Fatal("a TLS 1.2 client completed a handshake")
	}
	if cn, err := presentedCN(t, cfg, tls.VersionTLS13); err != nil || cn != "v13" {
		t.Fatalf("TLS 1.3 client: %q, %v", cn, err)
	}
	if len(cfg.NextProtos) != 1 || cfg.NextProtos[0] != "http/1.1" {
		t.Fatalf("NextProtos = %v", cfg.NextProtos)
	}
}

// AC-tls-native-8
func TestTLSStartRefusesMissingOrMismatchedPair(t *testing.T) {
	dir := t.TempDir()
	_, err := newTLSReloader(dir)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "tls.crt")) {
		t.Fatalf("missing pair: %v", err)
	}
	if _, err := newTLSReloader(""); err == nil {
		t.Fatal("no credentials directory accepted")
	}

	validPair(t, dir, "x")
	other := t.TempDir()
	validPair(t, other, "y")
	key, _ := os.ReadFile(filepath.Join(other, "tls.key"))
	if err := os.WriteFile(filepath.Join(dir, "tls.key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newTLSReloader(dir); err == nil || !strings.Contains(err.Error(), "tls.crt") || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("mismatched pair: %v", err)
	}

	// An expired certificate starts, with a warning.
	exp := t.TempDir()
	makeTLSPair(t, exp, "old", time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour))
	buf := captureLog(t)
	r, err := newTLSReloader(exp)
	if err != nil {
		t.Fatalf("expired certificate refused: %v", err)
	}
	if !strings.Contains(buf.String(), "warning") || !strings.Contains(buf.String(), "expired") {
		t.Fatalf("no expiry warning: %q", buf.String())
	}
	if cn, _ := presentedCN(t, newServerTLSConfig(r), tls.VersionTLS13); cn != "old" {
		t.Fatalf("expired certificate not served: %q", cn)
	}

	// Expiring soon is warned about too.
	soon := t.TempDir()
	makeTLSPair(t, soon, "soon", time.Now().Add(-time.Hour), time.Now().Add(48*time.Hour))
	buf = captureLog(t)
	if _, err := newTLSReloader(soon); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "expires on") {
		t.Fatalf("no expiring-soon warning: %q", buf.String())
	}
}
