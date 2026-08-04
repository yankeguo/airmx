package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func newTestKeys(t *testing.T) (pub, priv string) {
	t.Helper()
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestSubscribeUnsubscribePersist(t *testing.T) {
	pub, priv := newTestKeys(t)
	file := filepath.Join(t.TempDir(), "subs.json")

	s, err := New(file, pub, priv, "mailto:postmaster@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 0 {
		t.Fatalf("want 0 subscriptions, got %d", s.Count())
	}

	raw := json.RawMessage(`{"endpoint":"https://push.example.com/abc","keys":{"p256dh":"p","auth":"a"}}`)
	if err := s.Subscribe(raw); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if s.Count() != 1 {
		t.Fatalf("want 1 subscription, got %d", s.Count())
	}

	// A fresh Service over the same file must see the subscription.
	s2, err := New(file, pub, priv, "mailto:postmaster@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if s2.Count() != 1 {
		t.Fatalf("want 1 persisted subscription, got %d", s2.Count())
	}

	if err := s2.Unsubscribe("https://push.example.com/abc"); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if s2.Count() != 0 {
		t.Fatalf("want 0 subscriptions after unsubscribe, got %d", s2.Count())
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "[]" {
		t.Fatalf("subs file not updated: %v %q", err, data)
	}
}

func TestSubscribeRejectsBadInput(t *testing.T) {
	pub, priv := newTestKeys(t)
	s, err := New(filepath.Join(t.TempDir(), "subs.json"), pub, priv, "mailto:postmaster@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"endpoint":"http://insecure.example.com/x","keys":{"p256dh":"p","auth":"a"}}`,
		`{"endpoint":"https://push.example.com/x","keys":{"p256dh":"","auth":""}}`,
		`{"endpoint":"","keys":{"p256dh":"p","auth":"a"}}`,
		`not json`,
	} {
		if err := s.Subscribe(json.RawMessage(raw)); err == nil {
			t.Fatalf("expected error for %s", raw)
		}
	}
}

// testSubKeys returns a real subscriber key pair: p256dh is the uncompressed
// P-256 public key, auth is the 16-byte auth secret (both base64url).
func testSubKeys(t *testing.T) map[string]string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := elliptic.Marshal(elliptic.P256(), key.PublicKey.X, key.PublicKey.Y)
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(pub),
		"auth":   base64.RawURLEncoding.EncodeToString(auth),
	}
}

// TestNotifyDeliversAndPrunes runs a real SendNotification against an
// httptest push service: the encrypted payload must arrive, and a 410
// response must prune the subscription.
func TestNotifyDeliversAndPrunes(t *testing.T) {
	var got int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got++
		if r.Header.Get("Authorization") == "" {
			t.Error("missing VAPID Authorization header")
		}
		if r.Header.Get("Content-Encoding") != "aes128gcm" {
			t.Errorf("unexpected content encoding %q", r.Header.Get("Content-Encoding"))
		}
		if r.URL.Path == "/gone" {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	pub, priv := newTestKeys(t)
	file := filepath.Join(t.TempDir(), "subs.json")

	// Seed the subscriptions file directly: Subscribe() requires https
	// endpoints, but the test push service is plain http.
	subs, err := json.Marshal([]map[string]any{
		{"endpoint": srv.URL + "/ok", "keys": testSubKeys(t)},
		{"endpoint": srv.URL + "/gone", "keys": testSubKeys(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, subs, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := New(file, pub, priv, "mailto:postmaster@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 2 {
		t.Fatalf("want 2 subscriptions, got %d", s.Count())
	}

	s.Notify("新邮件", "alice@example.com — hello")

	if got != 2 {
		t.Fatalf("want 2 push requests, got %d", got)
	}
	if s.Count() != 1 {
		t.Fatalf("want 1 subscription after pruning the gone one, got %d", s.Count())
	}
}
