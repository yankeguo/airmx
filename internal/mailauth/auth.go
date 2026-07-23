// Package mailauth verifies inbound messages against SPF, DKIM and DMARC,
// and maps the results to a delivery action through a configurable policy.
package mailauth

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"strings"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/emersion/go-msgauth/dmarc"
	"github.com/zaccone/spf"
	"golang.org/x/net/publicsuffix"
)

// Status is the outcome of a single authentication check.
type Status string

const (
	StatusPass     Status = "pass"
	StatusFail     Status = "fail"
	StatusSoftfail Status = "softfail" // SPF only: weak fail (~all), suspicious but not definitive
	StatusNone     Status = "none"     // no record/signature, neutral, or lookup error
)

// Action is the delivery decision for a message.
type Action string

const (
	ActionReject Action = "reject" // refuse at end of DATA with a 5xx reply
	ActionSpam   Action = "spam"   // deliver into the spam folder
	ActionInbox  Action = "inbox"  // deliver into the inbox
)

func (a Action) Valid() bool {
	switch a {
	case ActionReject, ActionSpam, ActionInbox:
		return true
	}
	return false
}

// Policy maps check failures to delivery actions.
type Policy struct {
	SPFFail     Action `yaml:"spf_fail"`
	SPFSoftfail Action `yaml:"spf_softfail"`
	DKIMFail    Action `yaml:"dkim_fail"`
	DMARCFail   Action `yaml:"dmarc_fail"`
	Default     Action `yaml:"default"`
}

// Results holds the outcome of all checks for one message.
type Results struct {
	SPF         Status
	SPFDetail   string
	SPFDomain   string // domain the SPF check ran against
	DKIM        Status
	DKIMDetail  string
	DKIMDomain  string // domain of the passing signature
	DMARC       Status
	DMARCDetail string
	DMARCPolicy string // policy published by the From domain, if any
	FromDomain  string
}

// Check runs SPF, DKIM and DMARC verification for a message.
//
// clientIP is the remote SMTP peer, mailFrom the envelope sender (possibly
// empty for bounces), raw the complete message as received in DATA.
func Check(clientIP net.IP, mailFrom string, raw []byte) Results {
	var r Results
	r.SPF, r.SPFDetail, r.SPFDomain = checkSPF(clientIP, mailFrom)
	r.DKIM, r.DKIMDetail, r.DKIMDomain = checkDKIM(raw)
	r.FromDomain = fromDomain(raw)
	r.DMARC, r.DMARCDetail, r.DMARCPolicy = checkDMARC(r.FromDomain, r)
	return r
}

func checkSPF(ip net.IP, mailFrom string) (Status, string, string) {
	if ip == nil {
		return StatusNone, "no client IP", ""
	}
	sender := strings.TrimSpace(mailFrom)
	if sender == "" {
		return StatusNone, "empty envelope sender (bounce)", ""
	}
	at := strings.LastIndex(sender, "@")
	if at < 0 || at == len(sender)-1 {
		return StatusNone, "malformed envelope sender", ""
	}
	domain := strings.ToLower(sender[at+1:])
	res, _, err := spf.CheckHost(ip, domain, sender)
	if err != nil {
		// DNS/parse errors must not penalize the sender.
		return StatusNone, fmt.Sprintf("error: %v", err), domain
	}
	switch res {
	case spf.Pass:
		return StatusPass, "", domain
	case spf.Fail:
		return StatusFail, res.String(), domain
	case spf.Softfail:
		return StatusSoftfail, res.String(), domain
	default: // none, neutral, temperror, permerror
		return StatusNone, res.String(), domain
	}
}

func checkDKIM(raw []byte) (Status, string, string) {
	vers, err := dkim.Verify(bytes.NewReader(raw))
	if err != nil {
		if errors.Is(err, dkim.ErrTooManySignatures) {
			// Verify still returns the results it computed; use them.
		} else {
			return StatusNone, fmt.Sprintf("error: %v", err), ""
		}
	}
	if len(vers) == 0 {
		return StatusNone, "no signature", ""
	}
	var firstErr error
	for _, v := range vers {
		if v.Err == nil {
			return StatusPass, "", v.Domain
		}
		if firstErr == nil {
			firstErr = v.Err
		}
	}
	return StatusFail, fmt.Sprintf("invalid signature: %v", firstErr), ""
}

// fromDomain extracts the domain of the first From: header address.
func fromDomain(raw []byte) string {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	addrs, err := msg.Header.AddressList("From")
	if err != nil || len(addrs) == 0 {
		return ""
	}
	at := strings.LastIndex(addrs[0].Address, "@")
	if at < 0 {
		return ""
	}
	return strings.ToLower(addrs[0].Address[at+1:])
}

func checkDMARC(from string, r Results) (Status, string, string) {
	if from == "" {
		return StatusNone, "no From domain", ""
	}
	rec, err := dmarc.Lookup(from)
	if err != nil {
		return StatusNone, fmt.Sprintf("lookup error: %v", err), ""
	}
	if rec == nil {
		return StatusNone, "no DMARC record", ""
	}
	pol := string(rec.Policy)

	spfOK := r.SPF == StatusPass && aligned(r.SPFDomain, from, rec.SPFAlignment)
	dkimOK := r.DKIM == StatusPass && aligned(r.DKIMDomain, from, rec.DKIMAlignment)
	if spfOK || dkimOK {
		return StatusPass, "", pol
	}
	return StatusFail, "no aligned SPF or DKIM pass", pol
}

// aligned reports whether domain a is aligned with domain b per RFC 7489
// section 3.1. Relaxed alignment compares organizational domains.
func aligned(a, b string, mode dmarc.AlignmentMode) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = strings.ToLower(a), strings.ToLower(b)
	if mode == dmarc.AlignmentStrict {
		return a == b
	}
	if a == b {
		return true
	}
	oa, errA := publicsuffix.EffectiveTLDPlusOne(a)
	ob, errB := publicsuffix.EffectiveTLDPlusOne(b)
	return errA == nil && errB == nil && oa == ob
}

// Decide maps check results to a delivery action. DMARC failure takes
// precedence, then SPF (hard fail, then softfail), then DKIM.
func (p Policy) Decide(r Results) Action {
	switch {
	case r.DMARC == StatusFail:
		return p.DMARCFail
	case r.SPF == StatusFail:
		return p.SPFFail
	case r.SPF == StatusSoftfail:
		return p.SPFSoftfail
	case r.DKIM == StatusFail:
		return p.DKIMFail
	default:
		return p.Default
	}
}

// AuthResultsHeader renders an Authentication-Results header value (RFC 8601)
// for injection into the stored message.
func (r Results) AuthResultsHeader(authservID string) string {
	var b strings.Builder
	b.WriteString(authservID)
	fmt.Fprintf(&b, ";\n\tspf=%s", r.SPF)
	if r.SPFDetail != "" {
		fmt.Fprintf(&b, " reason=%q", r.SPFDetail)
	}
	if r.SPFDomain != "" {
		fmt.Fprintf(&b, " smtp.mailfrom=%s", r.SPFDomain)
	}
	fmt.Fprintf(&b, ";\n\tdkim=%s", r.DKIM)
	if r.DKIMDetail != "" {
		fmt.Fprintf(&b, " reason=%q", r.DKIMDetail)
	}
	if r.DKIMDomain != "" {
		fmt.Fprintf(&b, " header.d=%s", r.DKIMDomain)
	}
	fmt.Fprintf(&b, ";\n\tdmarc=%s", r.DMARC)
	if r.DMARCDetail != "" {
		fmt.Fprintf(&b, " reason=%q", r.DMARCDetail)
	}
	if r.DMARCPolicy != "" {
		fmt.Fprintf(&b, " policy.published=%s", r.DMARCPolicy)
	}
	if r.FromDomain != "" {
		fmt.Fprintf(&b, " header.from=%s", r.FromDomain)
	}
	return b.String()
}

// SpamStatusHeader renders a short X-Spam-Status style summary.
func (r Results) SpamStatusHeader(action Action) string {
	return fmt.Sprintf("action=%s spf=%s dkim=%s dmarc=%s", action, r.SPF, r.DKIM, r.DMARC)
}
