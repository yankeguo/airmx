package maildir

import (
	"strings"
	"testing"
)

const testMessage = "From: Alice <alice@example.com>\r\n" +
	"To: me@example.com\r\n" +
	"Subject: Hello\r\n" +
	"Date: Tue, 01 Jul 2025 10:00:00 +0000\r\n" +
	"\r\n" +
	"body text\r\n"

func TestDeliverListOpenDelete(t *testing.T) {
	s := New(t.TempDir())

	id, err := s.Deliver("me@example.com", []byte(testMessage))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	msgs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("List returned %d messages, want 1", len(msgs))
	}
	m := msgs[0]
	if m.ID != id || m.Subject != "Hello" || !m.Unread || m.Spam {
		t.Fatalf("unexpected summary: %+v", m)
	}
	if !strings.Contains(m.From, "alice@example.com") {
		t.Fatalf("unexpected From: %q", m.From)
	}

	raw, err := s.Open(id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !strings.Contains(string(raw), "body text") {
		t.Fatal("Open returned wrong content")
	}
	// Open marks the message as read.
	if mustList(t, s)[0].Unread {
		t.Fatal("message should be marked read after Open")
	}

	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(mustList(t, s)) != 0 {
		t.Fatal("message should be gone after Delete")
	}
}

func TestSpamFlagFromHeader(t *testing.T) {
	s := New(t.TempDir())
	spamMsg := "X-Spam-Status: action=spam spf=fail dkim=none dmarc=fail\r\n" + testMessage
	if _, err := s.Deliver("me@example.com", []byte(spamMsg)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Deliver("me@example.com", []byte(testMessage)); err != nil {
		t.Fatal(err)
	}
	msgs := mustList(t, s)
	if len(msgs) != 2 {
		t.Fatalf("List returned %d messages, want 2", len(msgs))
	}
	var spamSeen bool
	for _, m := range msgs {
		if strings.Contains(m.SpamStatus, "action=spam") {
			spamSeen = true
			if !m.Spam {
				t.Error("message with action=spam should have Spam=true")
			}
		} else if m.Spam {
			t.Error("plain message should have Spam=false")
		}
	}
	if !spamSeen {
		t.Error("spam message missing from List")
	}
}

func mustList(t *testing.T, s *Store) []Message {
	t.Helper()
	msgs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestEncodedWordFrom(t *testing.T) {
	raw := "From: =?utf-8?B?WS4tSy4gR3Vv?= <hi@guoyk.com>\r\n" +
		"Subject: x\r\n" +
		"\r\nhi\r\n"
	var m Message
	fillHeaders(&m, []byte(raw))
	if m.From != "Y.-K. Guo <hi@guoyk.com>" {
		t.Fatalf("From = %q", m.From)
	}
}

func TestGB2312EncodedHeaders(t *testing.T) {
	raw := "From: =?gb2312?B?suLK1NPKvP4=?= <noreply@example.cn>\r\n" +
		"Subject: =?gb2312?B?suLK1NPKvP4=?=\r\n" +
		"\r\nhi\r\n"
	var m Message
	fillHeaders(&m, []byte(raw))
	if m.Subject != "测试邮件" {
		t.Fatalf("Subject = %q", m.Subject)
	}
	if m.From != "测试邮件 <noreply@example.cn>" {
		t.Fatalf("From = %q", m.From)
	}
}

func TestInvalidReferences(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Deliver("bad/recipient@b.com", []byte("x")); err == nil {
		t.Fatal("expected error for invalid recipient")
	}
	for _, id := range []string{"../escape", "..", "a/b", ""} {
		if _, err := s.Open(id); err == nil {
			t.Errorf("expected error for id %q", id)
		}
		if err := s.Delete(id); err == nil {
			t.Errorf("expected error for id %q", id)
		}
	}
}
