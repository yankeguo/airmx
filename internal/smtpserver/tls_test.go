package smtpserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestCert generates a self-signed ECDSA certificate for cn and writes
// <dir>/test.crt and <dir>/test.key.
func writeTestCert(t *testing.T, dir, cn string, notAfter time.Time) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(filepath.Join(dir, "test.crt"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test.key"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCertReloader(t *testing.T) {
	dir := t.TempDir()
	farFuture := time.Now().Add(365 * 24 * time.Hour)

	writeTestCert(t, dir, "old.example.com", farFuture)
	r, err := NewCertReloader(filepath.Join(dir, "test.crt"), filepath.Join(dir, "test.key"))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}

	cert, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got := cert.Leaf.DNSNames[0]; got != "old.example.com" {
		t.Fatalf("got cert for %q, want old.example.com", got)
	}

	// Simulate a renewal replacing the files, with a distinct mtime.
	writeTestCert(t, dir, "new.example.com", farFuture)
	mtime := time.Now().Add(time.Hour)
	for _, name := range []string{"test.crt", "test.key"} {
		if err := os.Chtimes(filepath.Join(dir, name), mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	cert, err = r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after renewal: %v", err)
	}
	if got := cert.Leaf.DNSNames[0]; got != "new.example.com" {
		t.Fatalf("got cert for %q after renewal, want new.example.com", got)
	}
}

func TestCertReloaderKeepsOldCertOnReloadFailure(t *testing.T) {
	dir := t.TempDir()
	writeTestCert(t, dir, "keep.example.com", time.Now().Add(365*24*time.Hour))
	r, err := NewCertReloader(filepath.Join(dir, "test.crt"), filepath.Join(dir, "test.key"))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}

	// Corrupt the cert file; the reloader must keep serving the old cert.
	if err := os.WriteFile(filepath.Join(dir, "test.crt"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate with broken files: %v", err)
	}
	if got := cert.Leaf.DNSNames[0]; got != "keep.example.com" {
		t.Fatalf("got cert for %q, want keep.example.com", got)
	}
}

func TestNewCertReloaderMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewCertReloader(filepath.Join(dir, "test.crt"), filepath.Join(dir, "test.key")); err == nil {
		t.Fatal("expected error for missing files, got nil")
	}
}
