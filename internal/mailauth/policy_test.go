package mailauth

import (
	"testing"

	"github.com/emersion/go-msgauth/dmarc"
)

func TestPolicyDecide(t *testing.T) {
	p := Policy{
		SPFFail:     ActionReject,
		SPFSoftfail: ActionSpam,
		DKIMFail:    ActionInbox,
		DMARCFail:   ActionReject,
		Default:     ActionInbox,
	}
	cases := []struct {
		name string
		r    Results
		want Action
	}{
		{"all pass", Results{SPF: StatusPass, DKIM: StatusPass, DMARC: StatusPass}, ActionInbox},
		{"dmarc fail wins", Results{SPF: StatusFail, DKIM: StatusFail, DMARC: StatusFail}, ActionReject},
		{"spf fail", Results{SPF: StatusFail, DKIM: StatusPass, DMARC: StatusNone}, ActionReject},
		{"spf softfail", Results{SPF: StatusSoftfail, DKIM: StatusPass, DMARC: StatusNone}, ActionSpam},
		{"dkim fail", Results{SPF: StatusPass, DKIM: StatusFail, DMARC: StatusNone}, ActionInbox},
		{"none treated as default", Results{SPF: StatusNone, DKIM: StatusNone, DMARC: StatusNone}, ActionInbox},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.Decide(c.r); got != c.want {
				t.Fatalf("Decide() = %s, want %s", got, c.want)
			}
		})
	}
}

func TestAligned(t *testing.T) {
	cases := []struct {
		a, b    string
		relaxed bool
		want    bool
	}{
		{"example.com", "example.com", true, true},
		{"mail.example.com", "example.com", true, true},
		{"mail.example.com", "example.com", false, false},
		{"example.com", "other.com", true, false},
		{"", "example.com", true, false},
		{"mail.example.co.uk", "example.co.uk", true, true},
	}
	for _, c := range cases {
		mode := dmarc.AlignmentStrict
		if c.relaxed {
			mode = dmarc.AlignmentRelaxed
		}
		if got := aligned(c.a, c.b, mode); got != c.want {
			t.Errorf("aligned(%q, %q, %s) = %v, want %v", c.a, c.b, mode, got, c.want)
		}
	}
}
