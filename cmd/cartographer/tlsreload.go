package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// tlsExpiryWarning is how long before NotAfter a certificate is warned about.
const tlsExpiryWarning = 14 * 24 * time.Hour

// loadServerCertificate reads tls.crt and tls.key of the credentials directory
// and returns the pair with the digest of both files' bytes. Neither file is
// ever quoted in an error: a key stays out of every log line. os.ReadFile
// follows symlinks, so a mounted secret whose link is swapped is seen.
func loadServerCertificate(credDir string) (*tls.Certificate, [sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	if credDir == "" {
		return nil, sum, errors.New("no credentials directory for tls.crt and tls.key; set credentials_dir")
	}
	read := func(name string) ([]byte, error) {
		p, ok := sharedCredentialFile(credDir, name)
		if !ok {
			return nil, fmt.Errorf("%s is missing: tls.crt and tls.key go in credentials_dir", filepath.Join(credDir, name))
		}
		return os.ReadFile(p)
	}
	crt, err := read(config.TLSCertFile)
	if err != nil {
		return nil, sum, err
	}
	key, err := read(config.TLSKeyFile)
	if err != nil {
		return nil, sum, err
	}
	h := sha256.New()
	h.Write(crt)
	h.Write([]byte{0})
	h.Write(key)
	copy(sum[:], h.Sum(nil))
	cert, err := tls.X509KeyPair(crt, key)
	if err != nil {
		// The error of X509KeyPair names no key bytes; it is reworded all the
		// same so that the files and what is wrong with them are named.
		return nil, sum, fmt.Errorf("%s and %s do not form a usable pair: %v", config.TLSCertFile, config.TLSKeyFile, err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, sum, fmt.Errorf("%s: %v", config.TLSCertFile, err)
	}
	cert.Leaf = leaf
	return &cert, sum, nil
}

// tlsReloader holds the certificate the TLS listener presents and swaps it
// when the files change: the poll compares a digest of both files, SIGHUP
// always reloads. Every failure keeps the certificate in force (the rule of
// authReloader). New handshakes use the new certificate; open connections keep
// theirs.
type tlsReloader struct {
	credDir string
	now     func() time.Time
	cert    atomic.Pointer[tls.Certificate]

	mu      sync.Mutex // serialises reloads: a signal racing a tick
	applied [sha256.Size]byte
	refused [sha256.Size]byte
	missing bool
}

// newTLSReloader makes the first load: a missing, unreadable or mismatched
// pair is an error, so a server that asked for TLS never starts without it.
// An expired or not-yet-valid certificate starts with a warning, as D2110 does
// for tokens: refusing it would crash-loop a pod for a dated file.
func newTLSReloader(credDir string) (*tlsReloader, error) {
	r := &tlsReloader{credDir: credDir, now: time.Now}
	cert, sum, err := loadServerCertificate(credDir)
	if err != nil {
		return nil, err
	}
	r.cert.Store(cert)
	r.applied = sum
	r.warnValidity(cert, "start")
	return r, nil
}

// warnValidity logs a certificate outside its validity window, or expiring soon.
func (r *tlsReloader) warnValidity(cert *tls.Certificate, why string) {
	now := r.now()
	switch {
	case now.After(cert.Leaf.NotAfter):
		log.Printf("warning: tls (%s): the certificate expired on %s, serving it all the same", why, cert.Leaf.NotAfter.UTC().Format(time.RFC3339))
	case now.Before(cert.Leaf.NotBefore):
		log.Printf("warning: tls (%s): the certificate is valid from %s, serving it all the same", why, cert.Leaf.NotBefore.UTC().Format(time.RFC3339))
	case cert.Leaf.NotAfter.Sub(now) < tlsExpiryWarning:
		log.Printf("warning: tls (%s): the certificate expires on %s", why, cert.Leaf.NotAfter.UTC().Format(time.RFC3339))
	}
}

// getCertificate is the tls.Config callback: the certificate in force.
func (r *tlsReloader) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return r.cert.Load(), nil
}

// newServerTLSConfig is the one place the server's TLS settings are built: TLS
// 1.3 only, HTTP/1.1 only (no HTTP/2 is introduced), the certificate from the
// reloader. A later client-certificate change sets ClientAuth and ClientCAs here.
func newServerTLSConfig(r *tlsReloader) *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS13,
		NextProtos:     []string{"http/1.1"},
		GetCertificate: r.getCertificate,
	}
}

// start runs the reloader for the life of the server: SIGHUP, and the poll of
// the files every interval (zero: no poll). The returned function stops it.
func (r *tlsReloader) start(interval time.Duration) func() {
	var tick <-chan time.Time
	stopTick := func() {}
	if interval > 0 {
		t := time.NewTicker(interval)
		tick, stopTick = t.C, t.Stop
	}
	sig, stopSig := watchReloadSignal()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-tick:
				r.poll()
			case <-sig:
				r.Reload("SIGHUP")
			}
		}
	}()
	return func() { close(stop); stopTick(); stopSig() }
}

// poll reloads when the bytes of the files changed since the last look. A
// refused content is logged once, not once per tick.
func (r *tlsReloader) poll() { r.reload("files changed", false) }

// Reload applies the files now (the SIGHUP path), a refused content included:
// it is the operator asking again.
func (r *tlsReloader) Reload(why string) { r.reload(why, true) }

func (r *tlsReloader) reload(why string, force bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cert, sum, err := loadServerCertificate(r.credDir)
	if err != nil {
		if sum != ([sha256.Size]byte{}) {
			// Both files read: the content is the refused unit.
			r.missing = false
			r.logRefused(sum, force, why, err)
			return
		}
		// A file missing or unreadable has no digest: once per state, not per tick.
		if force || !r.missing {
			log.Printf("tls reload (%s) refused, keeping the certificate in force: %v", why, err)
		}
		r.missing = true
		return
	}
	r.missing = false
	if sum == r.applied {
		return
	}
	r.cert.Store(cert)
	r.applied, r.refused = sum, [sha256.Size]byte{}
	log.Printf("tls reloaded (%s): certificate for %q valid until %s", why, cert.Leaf.Subject.CommonName, cert.Leaf.NotAfter.UTC().Format(time.RFC3339))
	r.warnValidity(cert, "reload")
}

// logRefused logs a content that read but did not load, once per distinct content.
func (r *tlsReloader) logRefused(sum [sha256.Size]byte, force bool, why string, err error) {
	if force || sum != r.refused {
		log.Printf("tls reload (%s) refused, keeping the certificate in force: %v", why, err)
	}
	r.refused = sum
}
