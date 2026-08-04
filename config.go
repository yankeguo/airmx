package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/yankeguo/airmx/internal/mailauth"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

type Policy = mailauth.Policy

type WebConfig struct {
	Username       string      `yaml:"username"`
	PasswordBcrypt string      `yaml:"password_bcrypt"`
	Push           *PushConfig `yaml:"push"`
}

// PushConfig enables Web Push notifications for new mail. Generate the key
// pair with `airmx genpushkey`.
type PushConfig struct {
	VapidPublicKey  string `yaml:"vapid_public_key"`
	VapidPrivateKey string `yaml:"vapid_private_key"`
}

type Config struct {
	Domain     string    `yaml:"domain"`
	SMTPListen string    `yaml:"smtp_listen"`
	WebListen  string    `yaml:"web_listen"`
	DataDir    string    `yaml:"data_dir"`
	TLSCertDir string    `yaml:"tls_cert_dir"`
	Recipients []string  `yaml:"recipients"`
	Web        WebConfig `yaml:"web"`
	Policy     Policy    `yaml:"policy"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	var errs []error
	if c.Domain == "" {
		errs = append(errs, errors.New("domain is required"))
	}
	if c.SMTPListen == "" {
		c.SMTPListen = ":25"
	}
	if c.WebListen == "" {
		c.WebListen = ":8080"
	}
	if c.DataDir == "" {
		c.DataDir = "./data"
	}
	if len(c.Recipients) == 0 {
		errs = append(errs, errors.New("recipients must not be empty"))
	}
	for i, r := range c.Recipients {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" || !strings.Contains(r, "@") {
			errs = append(errs, fmt.Errorf("recipients[%d]: %q is not a valid address or pattern", i, r))
			continue
		}
		c.Recipients[i] = r
	}
	if c.Web.Username == "" {
		errs = append(errs, errors.New("web.username is required"))
	}
	if c.Web.PasswordBcrypt == "" {
		errs = append(errs, errors.New("web.password_bcrypt is required"))
	} else if _, err := bcrypt.Cost([]byte(c.Web.PasswordBcrypt)); err != nil {
		errs = append(errs, fmt.Errorf("web.password_bcrypt is not a valid bcrypt hash: %v", err))
	}
	if c.Web.Push != nil {
		if c.Web.Push.VapidPublicKey == "" {
			errs = append(errs, errors.New("web.push.vapid_public_key is required"))
		}
		if c.Web.Push.VapidPrivateKey == "" {
			errs = append(errs, errors.New("web.push.vapid_private_key is required"))
		}
	}
	for name, a := range map[string]mailauth.Action{
		"policy.spf_fail":     c.Policy.SPFFail,
		"policy.spf_softfail": c.Policy.SPFSoftfail,
		"policy.dkim_fail":    c.Policy.DKIMFail,
		"policy.dmarc_fail":   c.Policy.DMARCFail,
		"policy.default":      c.Policy.Default,
	} {
		if !a.Valid() {
			errs = append(errs, fmt.Errorf("%s must be one of reject/spam/inbox, got %q", name, a))
		}
	}
	return errors.Join(errs...)
}

// AcceptsRecipient reports whether addr matches the configured recipient
// allowlist. Entries may be exact addresses or "*@domain" wildcards.
func (c *Config) AcceptsRecipient(addr string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	for _, r := range c.Recipients {
		if r == addr {
			return true
		}
		if strings.HasPrefix(r, "*@") && strings.HasSuffix(addr, r[1:]) {
			return true
		}
	}
	return false
}
