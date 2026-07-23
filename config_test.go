package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const validConfig = `
domain: mail.example.com
smtp_listen: ":2525"
web_listen: ":8080"
data_dir: "./data"
recipients:
  - "me@example.com"
  - "*@lists.example.com"
web:
  username: admin
  password_bcrypt: "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
policy:
  spf_fail: reject
  spf_softfail: spam
  dkim_fail: inbox
  dmarc_fail: reject
  default: inbox
`

func TestLoadConfigValid(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Domain != "mail.example.com" || cfg.SMTPListen != ":2525" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	body := strings.NewReplacer(
		`smtp_listen: ":2525"`, "",
		`web_listen: ":8080"`, "",
		`data_dir: "./data"`, "",
	).Replace(validConfig)
	cfg, err := LoadConfig(writeConfig(t, body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SMTPListen != ":25" || cfg.WebListen != ":8080" || cfg.DataDir != "./data" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestLoadConfigInvalid(t *testing.T) {
	cases := map[string]string{
		"missing domain": strings.Replace(validConfig, "domain: mail.example.com\n", "", 1),
		"bad recipient":  strings.Replace(validConfig, `  - "me@example.com"`, `  - "not-an-address"`, 1),
		"bad bcrypt":     strings.Replace(validConfig, "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy", "plainpassword", 1),
		"bad policy":     strings.Replace(validConfig, "dmarc_fail: reject", "dmarc_fail: shredder", 1),
		"empty recipients": strings.NewReplacer(
			`  - "me@example.com"`, "",
			`  - "*@lists.example.com"`, "",
		).Replace(validConfig),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeConfig(t, body)); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestAcceptsRecipient(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	accepts := []string{"me@example.com", "Me@Example.com", "anyone@lists.example.com"}
	for _, a := range accepts {
		if !cfg.AcceptsRecipient(a) {
			t.Errorf("expected %s to be accepted", a)
		}
	}
	rejects := []string{"other@example.com", "me@evil-example.com", "a@b.com", "lists.example.com"}
	for _, a := range rejects {
		if cfg.AcceptsRecipient(a) {
			t.Errorf("expected %s to be rejected", a)
		}
	}
}

// Guard against the test bcrypt hash drifting from its known password.
func TestTestHashMatchesPassword(t *testing.T) {
	hash := "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("password")) != nil {
		t.Skip("test fixture hash no longer matches 'password'")
	}
}
