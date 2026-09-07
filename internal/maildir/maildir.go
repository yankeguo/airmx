// Package maildir implements storage of received messages in maildir layout.
//
// Layout: <root>/<recipient>/{tmp,new,cur}
//
// There is no inbox/spam directory split: the spam classification travels in
// the X-Spam-Status header injected at delivery time, and listings read it
// back from there.
package maildir

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	htmlcharset "golang.org/x/net/html/charset"
)

// Message is a summary of a stored message for listing.
type Message struct {
	ID          string // unique filename, safe for URLs
	Recipient   string
	Subject     string
	From        string
	Date        time.Time
	Unread      bool
	Spam        bool   // from the X-Spam-Status header (action=spam)
	SpamStatus  string // X-Spam-Status header value, if any
	AuthResults string // Authentication-Results header value, if any
}

type Store struct {
	root string
}

func New(root string) *Store { return &Store{root: root} }

// Deliver atomically stores msg for recipient and returns the message ID.
func (s *Store) Deliver(recipient string, msg []byte) (string, error) {
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	if recipient == "" || strings.ContainsAny(recipient, `/\`) {
		return "", fmt.Errorf("invalid recipient %q", recipient)
	}
	base := filepath.Join(s.root, recipient)
	for _, sub := range []string{"tmp", "new", "cur"} {
		if err := os.MkdirAll(filepath.Join(base, sub), 0o755); err != nil {
			return "", err
		}
	}
	var randPart [4]byte
	if _, err := rand.Read(randPart[:]); err != nil {
		return "", err
	}
	host, _ := os.Hostname()
	id := fmt.Sprintf("%d.%d.%s.%s", time.Now().UnixNano(), os.Getpid(), host, hex.EncodeToString(randPart[:]))

	tmpPath := filepath.Join(base, "tmp", id)
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(msg); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, filepath.Join(base, "new", id)); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	return id, nil
}

// headerPeekBytes bounds how much of a message List reads to extract the
// summary headers; bodies (and attachments) never need to be loaded.
const headerPeekBytes = 64 << 10

// List returns summaries of all messages across all recipients, newest
// first.
func (s *Store) List() ([]Message, error) {
	var msgs []Message
	recipients, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for _, rec := range recipients {
		if !rec.IsDir() {
			continue
		}
		base := filepath.Join(s.root, rec.Name())
		for _, sub := range []struct {
			dir    string
			unread bool
		}{{"new", true}, {"cur", false}} {
			entries, err := os.ReadDir(filepath.Join(base, sub.dir))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				m := Message{ID: e.Name(), Recipient: rec.Name(), Unread: sub.unread}
				if f, err := os.Open(filepath.Join(base, sub.dir, e.Name())); err == nil {
					// ReadMessage stops consuming at the end of the header
					// block; the limit only guards against pathological
					// header sections. A truncated body is never read.
					if msg, err := mail.ReadMessage(io.LimitReader(f, headerPeekBytes)); err == nil {
						fillHeaders(&m, msg.Header)
					}
					f.Close()
				}
				msgs = append(msgs, m)
			}
		}
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Date.After(msgs[j].Date) })
	return msgs, nil
}

// headerDecoder decodes RFC 2047 encoded-words in headers, including
// non-UTF-8 charsets such as GB2312/Big5 (stdlib alone handles only UTF-8).
var headerDecoder = &mime.WordDecoder{CharsetReader: htmlcharset.NewReaderLabel}

func decodeHeader(s string) string {
	if d, err := headerDecoder.DecodeHeader(s); err == nil {
		return d
	}
	return s
}

func fillHeaders(m *Message, h mail.Header) {
	m.Subject = decodeHeader(h.Get("Subject"))
	if addr, err := mail.ParseAddress(h.Get("From")); err == nil {
		name := decodeHeader(addr.Name)
		if name != "" {
			m.From = name + " <" + addr.Address + ">"
		} else {
			m.From = addr.Address
		}
	} else {
		// Even when the address structure is unparseable, the raw header
		// may still contain decodable encoded-words.
		m.From = decodeHeader(h.Get("From"))
	}
	// Unparseable or missing Date leaves the zero time; the message then
	// sorts oldest, which is acceptable for malformed mail.
	if t, err := mail.ParseDate(h.Get("Date")); err == nil {
		m.Date = t
	}
	m.SpamStatus = h.Get("X-Spam-Status")
	m.Spam = strings.Contains(m.SpamStatus, "action=spam")
	m.AuthResults = h.Get("Authentication-Results")
}

// locate finds the full path of message id, scanning all recipients.
func (s *Store) locate(id string) (path string, unread bool, err error) {
	if !validID(id) {
		return "", false, errors.New("invalid message reference")
	}
	recipients, err := os.ReadDir(s.root)
	if err != nil {
		return "", false, err
	}
	for _, rec := range recipients {
		if !rec.IsDir() {
			continue
		}
		for _, sub := range []string{"new", "cur"} {
			p := filepath.Join(s.root, rec.Name(), sub, id)
			if _, err := os.Stat(p); err == nil {
				return p, sub == "new", nil
			}
		}
	}
	return "", false, fs.ErrNotExist
}

// Open returns the raw bytes of a message. Unread messages are moved from
// new/ to cur/ (marked as read).
func (s *Store) Open(id string) ([]byte, error) {
	p, unread, err := s.locate(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if unread {
		_ = os.Rename(p, filepath.Join(filepath.Dir(filepath.Dir(p)), "cur", id))
	}
	return raw, nil
}

// Delete removes a message.
func (s *Store) Delete(id string) error {
	p, _, err := s.locate(id)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

func validID(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\`) && id != "." && id != ".."
}

// Reader opens a message for streaming (used for attachments).
func (s *Store) Reader(id string) (io.ReadCloser, error) {
	p, _, err := s.locate(id)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}
