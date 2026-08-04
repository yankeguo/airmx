// Package smtpserver implements the inbound SMTP receiver.
package smtpserver

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"path/filepath"
	"strings"

	smtp "github.com/emersion/go-smtp"
	"github.com/yankeguo/airmx/internal/mailauth"
	"github.com/yankeguo/airmx/internal/maildir"
)

// MaxMessageBytes caps the size of a single DATA payload.
const MaxMessageBytes = 25 << 20

// Options carries everything the receiver needs from the configuration.
type Options struct {
	Domain           string
	Policy           mailauth.Policy
	AcceptsRecipient func(addr string) bool
	Store            *maildir.Store
	// TLSCertDir, if set, enables STARTTLS using <TLSCertDir>/<Domain>.crt
	// and <TLSCertDir>/<Domain>.key, reloaded automatically on renewal.
	TLSCertDir string
	// OnDeliver, if set, is called once after a message has been delivered
	// to all its recipients. It must be non-blocking.
	OnDeliver func(raw []byte)
}

type backend struct {
	opts Options
}

func (b *backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	var ip net.IP
	if addr, ok := c.Conn().RemoteAddr().(*net.TCPAddr); ok {
		ip = addr.IP
	}
	return &session{opts: b.opts, clientIP: ip}, nil
}

type session struct {
	opts     Options
	clientIP net.IP
	mailFrom string
	rcpts    []string
}

func (s *session) Reset() {
	s.mailFrom = ""
	s.rcpts = nil
}

func (s *session) Logout() error { return nil }

func (s *session) Mail(from string, _ *smtp.MailOptions) error {
	s.mailFrom = from
	return nil
}

func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	if !s.opts.AcceptsRecipient(to) {
		return &smtp.SMTPError{
			Code:         550,
			EnhancedCode: smtp.EnhancedCode{5, 1, 1},
			Message:      "recipient rejected",
		}
	}
	s.rcpts = append(s.rcpts, strings.ToLower(to))
	return nil
}

func (s *session) Data(r io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(r, MaxMessageBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > MaxMessageBytes {
		return &smtp.SMTPError{
			Code:         552,
			EnhancedCode: smtp.EnhancedCode{5, 3, 4},
			Message:      "message too large",
		}
	}

	res := mailauth.Check(s.clientIP, s.mailFrom, raw)
	action := s.opts.Policy.Decide(res)
	log.Printf("smtp: from=<%s> ip=%s spf=%s dkim=%s dmarc=%s -> %s",
		s.mailFrom, s.clientIP, res.SPF, res.DKIM, res.DMARC, action)

	if action == mailauth.ActionReject {
		return &smtp.SMTPError{
			Code:         554,
			EnhancedCode: smtp.EnhancedCode{5, 7, 1},
			Message:      fmt.Sprintf("message rejected by policy (%s)", res.SpamStatusHeader(action)),
		}
	}

	// The action is recorded in the injected X-Spam-Status header; the store
	// reads it back for listings.
	raw = injectHeaders(raw, s.opts.Domain, res, action)

	for _, rcpt := range s.rcpts {
		if _, err := s.opts.Store.Deliver(rcpt, raw); err != nil {
			log.Printf("smtp: deliver to %s failed: %v", rcpt, err)
			return &smtp.SMTPError{
				Code:         451,
				EnhancedCode: smtp.EnhancedCode{4, 3, 0},
				Message:      "internal delivery error",
			}
		}
	}
	if s.opts.OnDeliver != nil {
		s.opts.OnDeliver(raw)
	}
	return nil
}

// injectHeaders prepends Authentication-Results and X-Spam-Status to the
// stored copy of the message.
func injectHeaders(raw []byte, domain string, res mailauth.Results, action mailauth.Action) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "Authentication-Results: %s\r\n", res.AuthResultsHeader(domain))
	fmt.Fprintf(&b, "X-Spam-Status: %s\r\n", res.SpamStatusHeader(action))
	b.Write(raw)
	return b.Bytes()
}

// ListenAndServe runs the SMTP server on addr until the server is closed.
func ListenAndServe(addr string, opts Options) error {
	s := smtp.NewServer(&backend{opts: opts})
	s.Addr = addr
	s.Domain = opts.Domain
	s.MaxMessageBytes = MaxMessageBytes
	s.MaxRecipients = 50
	if opts.TLSCertDir != "" {
		cr, err := NewCertReloader(
			filepath.Join(opts.TLSCertDir, opts.Domain+".crt"),
			filepath.Join(opts.TLSCertDir, opts.Domain+".key"),
		)
		if err != nil {
			return fmt.Errorf("tls: %w", err)
		}
		s.TLSConfig = &tls.Config{
			GetCertificate: cr.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		}
	}
	// AUTH is not advertised because session does not implement AuthSession;
	// this server only receives mail.
	log.Printf("smtp: listening on %s", addr)
	return s.ListenAndServe()
}
