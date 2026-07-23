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

	id, err := s.Deliver("me@example.com", FolderInbox, []byte(testMessage))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	msgs, err := s.List(FolderInbox)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("List returned %d messages, want 1", len(msgs))
	}
	m := msgs[0]
	if m.ID != id || m.Subject != "Hello" || !m.Unread {
		t.Fatalf("unexpected summary: %+v", m)
	}
	if !strings.Contains(m.From, "alice@example.com") {
		t.Fatalf("unexpected From: %q", m.From)
	}
	if len(s.mustList(t, FolderSpam)) != 0 {
		t.Fatal("spam folder should be empty")
	}

	raw, err := s.Open(FolderInbox, id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !strings.Contains(string(raw), "body text") {
		t.Fatal("Open returned wrong content")
	}
	// Open marks the message as read.
	if s.mustList(t, FolderInbox)[0].Unread {
		t.Fatal("message should be marked read after Open")
	}

	if err := s.Delete(FolderInbox, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(s.mustList(t, FolderInbox)) != 0 {
		t.Fatal("message should be gone after Delete")
	}
}

func (s *Store) mustList(t *testing.T, f Folder) []Message {
	t.Helper()
	msgs, err := s.List(f)
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

func TestInvalidReferences(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Deliver("a@b.com", Folder("bogus"), []byte("x")); err == nil {
		t.Fatal("expected error for invalid folder")
	}
	if _, err := s.Deliver("bad/recipient@b.com", FolderInbox, []byte("x")); err == nil {
		t.Fatal("expected error for invalid recipient")
	}
	for _, id := range []string{"../escape", "..", "a/b", ""} {
		if _, err := s.Open(FolderInbox, id); err == nil {
			t.Errorf("expected error for id %q", id)
		}
		if err := s.Delete(FolderInbox, id); err == nil {
			t.Errorf("expected error for id %q", id)
		}
	}
}
