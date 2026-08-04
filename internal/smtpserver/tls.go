package smtpserver

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// reloadBeforeExpiry is how close to expiry the cached certificate must be
// before GetCertificate attempts a reload from disk.
const reloadBeforeExpiry = 24 * time.Hour

// CertReloader loads a certificate/key pair from disk and reloads it when
// the files change or the certificate approaches expiry. It is meant for
// setups where an external tool (e.g. Caddy) renews the certificate in
// place; mounting its certificate directory is enough.
type CertReloader struct {
	certFile string
	keyFile  string

	mu      sync.RWMutex
	cert    *tls.Certificate
	expiry  time.Time
	certMod time.Time
	keyMod  time.Time
}

// NewCertReloader performs the initial load and fails fast if the pair
// cannot be loaded.
func NewCertReloader(certFile, keyFile string) (*CertReloader, error) {
	r := &CertReloader{certFile: certFile, keyFile: keyFile}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// GetCertificate implements tls.Config.GetCertificate.
func (r *CertReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	cert, expiry, certMod, keyMod := r.cert, r.expiry, r.certMod, r.keyMod
	r.mu.RUnlock()

	if cert == nil ||
		time.Until(expiry) < reloadBeforeExpiry ||
		fileMod(r.certFile) != certMod ||
		fileMod(r.keyFile) != keyMod {
		if err := r.reload(); err != nil {
			log.Printf("tls: certificate reload failed: %v", err)
			if cert == nil {
				return nil, err
			}
			// Keep serving the previous certificate.
		} else {
			r.mu.RLock()
			cert = r.cert
			r.mu.RUnlock()
		}
	}
	return cert, nil
}

func (r *CertReloader) reload() error {
	pair, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse %s: %w", r.certFile, err)
	}
	pair.Leaf = leaf

	r.mu.Lock()
	r.cert = &pair
	r.expiry = leaf.NotAfter
	r.certMod = fileMod(r.certFile)
	r.keyMod = fileMod(r.keyFile)
	r.mu.Unlock()

	log.Printf("tls: loaded certificate for %v (expires %s)", leaf.DNSNames, leaf.NotAfter.Format(time.RFC3339))
	return nil
}

func fileMod(path string) time.Time {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}
