package web

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func testServer() *Server {
	return &Server{
		username:     "admin",
		passwordHash: []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"),
	}
}

func TestSealOpenRoundtrip(t *testing.T) {
	s := testServer()
	v, err := s.seal("admin|9999999999")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	p, err := s.open(v)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if p != "admin|9999999999" {
		t.Fatalf("payload = %q", p)
	}
}

func TestOpenRejectsTampering(t *testing.T) {
	s := testServer()
	v, err := s.seal("admin|9999999999")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.open(v[:len(v)-2] + "xx"); err == nil {
		t.Fatal("expected error for tampered value")
	}
	if _, err := s.open("not-base64!!"); err == nil {
		t.Fatal("expected error for garbage")
	}
	// A different key must not decrypt.
	other := testServer()
	other.passwordHash = []byte("$2a$10$differenthashvalue0123456789012345678901234567890123")
	if _, err := other.open(v); err == nil {
		t.Fatal("expected error with different key")
	}
}

func TestValidSession(t *testing.T) {
	s := testServer()
	future := time.Now().Add(time.Hour).Unix()
	past := time.Now().Add(-time.Hour).Unix()

	good, _ := s.seal(fmt.Sprintf("admin|%d", future))
	if !s.validSession(good) {
		t.Fatal("expected valid session")
	}
	expired, _ := s.seal(fmt.Sprintf("admin|%d", past))
	if s.validSession(expired) {
		t.Fatal("expired session should be rejected")
	}
	wrongUser, _ := s.seal(fmt.Sprintf("root|%d", future))
	if s.validSession(wrongUser) {
		t.Fatal("wrong user should be rejected")
	}
	malformed, _ := s.seal("no-separator")
	if s.validSession(malformed) {
		t.Fatal("malformed payload should be rejected")
	}
	if s.validSession(strings.Repeat("a", 8)) {
		t.Fatal("garbage should be rejected")
	}
}
